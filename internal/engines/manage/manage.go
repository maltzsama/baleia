// Package manage stores direct engines (#364): it lists them, creates one
// with the native catalogs of its connection, enables or disables it, deletes
// it when nothing depends on it, and lists its bindings. The caller checks
// access, and pings the connection and reads its native catalogs before
// Create.
package manage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/galileostudio/baleia/internal/db"
)

// Reader reads engines and bindings; *repo.Repo implements it.
type Reader interface {
	ListEngines(ctx context.Context) ([]db.Engine, error)
	ListEnginesVisibleToUser(ctx context.Context, userID db.UUID) ([]db.Engine, error)
	ListBindingsByEngine(ctx context.Context, engineID db.UUID) ([]db.CatalogBinding, error)
	ListBindingsByCatalog(ctx context.Context, catalogID db.UUID) ([]db.CatalogBinding, error)
	CountEngineBlockers(ctx context.Context, engineID db.UUID) (db.CountEngineBlockersRow, error)
}

// Writer changes an engine; *repo.Repo implements it.
type Writer interface {
	UpdateEngine(ctx context.Context, arg db.UpdateEngineParams) (db.Engine, error)
}

// Ops reads what an engine reports and its reconcile runs; *repo.Repo
// implements it.
type Ops interface {
	GetEngineByID(ctx context.Context, id db.UUID) (db.Engine, error)
	ListEngineObservations(ctx context.Context, engineID db.UUID) ([]db.EngineCatalogObservation, error)
	CountEngineImpact(ctx context.Context, engineID db.UUID) (db.CountEngineImpactRow, error)
	GetReconcileRun(ctx context.Context, arg db.GetReconcileRunParams) (db.CatalogReconcileRun, error)
	ListReconcileRuns(ctx context.Context, arg db.ListReconcileRunsParams) ([]db.CatalogReconcileRun, error)
}

// Store is Reader, Writer and Ops.
type Store interface {
	Reader
	Writer
	Ops
}

// TxStore creates and deletes an engine with its native catalogs, in one
// transaction; db.Querier implements it.
type TxStore interface {
	CreateEngine(ctx context.Context, arg db.CreateEngineParams) (db.Engine, error)
	UpsertNativeCatalog(ctx context.Context, arg db.UpsertNativeCatalogParams) (db.Catalog, error)
	UpsertNativeBinding(ctx context.Context, arg db.UpsertNativeBindingParams) (db.CatalogBinding, error)
	DeleteNativeCatalogsOfEngine(ctx context.Context, engineID db.UUID) error
	DeleteEngine(ctx context.Context, id db.UUID) error
}

// Tx runs fn in one transaction.
type Tx func(ctx context.Context, fn func(TxStore) error) error

// NameRe mirrors the engines.name CHECK.
var NameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// Reconcile run listing limits.
const (
	DefaultRuns = 20
	MaxRuns     = 100
)

var (
	// ErrNotFound: no engine has the id.
	ErrNotFound = errors.New("engine not found")
	// ErrRunNotFound: the engine has no reconcile run with the id.
	ErrRunNotFound = errors.New("reconcile run not found")
	// ErrInvalidName: the name does not match NameRe.
	ErrInvalidName = errors.New("must match " + NameRe.String())
	// ErrNameTaken: another engine has the name.
	ErrNameTaken = errors.New("an engine with this name already exists")
	// ErrConnectionTaken: the connection already backs an engine.
	ErrConnectionTaken = errors.New("this connection already backs an engine")
)

// InUseError: something still depends on the engine.
type InUseError struct{ Msg string }

func (e *InUseError) Error() string { return e.Msg }

// Service manages engines.
type Service struct {
	store Store
	tx    Tx
}

// New builds the service.
func New(store Store, tx Tx) *Service { return &Service{store: store, tx: tx} }

// CheckName refuses a name the database would.
func CheckName(name string) error {
	if !NameRe.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}

// NormalizeName derives an engine name from a connection name: lower case, runs
// of characters outside [a-z0-9_-] become one underscore, edge separators are
// trimmed and the result is cut to 62 characters; one that does not start with a
// letter gets an "e_" prefix. It returns "" when nothing usable is left. The
// result always satisfies CheckName.
func NormalizeName(value string) string {
	var b strings.Builder
	pendingSep := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			if pendingSep {
				b.WriteByte('_')
				pendingSep = false
			}
			b.WriteRune(r)
		default:
			pendingSep = true
		}
	}
	slug := strings.Trim(b.String(), "_-")
	if len(slug) > 62 {
		slug = slug[:62]
	}
	if slug == "" {
		return ""
	}
	if !NameRe.MatchString(slug) {
		slug = ("e_" + slug)
		if len(slug) > 63 {
			slug = slug[:63]
		}
	}
	return slug
}

// List returns every engine to an admin, and to anyone else the engines
// they may see.
func (s *Service) List(ctx context.Context, userID db.UUID, admin bool) ([]db.Engine, error) {
	if admin {
		return s.store.ListEngines(ctx)
	}
	return s.store.ListEnginesVisibleToUser(ctx, userID)
}

// NewEngine is a direct engine to create over a connection.
type NewEngine struct {
	Name         string
	Kind         string
	ConnectionID db.UUID
	CreatedBy    db.UUID
	// Natives are the connection's native catalogs, each bound to the
	// engine under its own name.
	Natives []string
}

// NewFederatedEngine registers an engine-family endpoint without creating
// native catalogs. Source catalogs are bound to the engine separately.
type NewFederatedEngine struct {
	Name         string
	Kind         string
	ConnectionID db.UUID
	CreatedBy    db.UUID
}

// CreateFederatedIn writes a federated engine through q, which the caller has
// already bound to a transaction. It opens no transaction, so an engine-family
// connection created by the caller commits or rolls back with the engine.
func CreateFederatedIn(ctx context.Context, q TxStore, n NewFederatedEngine) (db.Engine, error) {
	return q.CreateEngine(ctx, db.CreateEngineParams{
		Name: n.Name, Kind: n.Kind, Class: "federated", ConnectionID: n.ConnectionID,
		IdentityMode: "connection", CreatedBy: n.CreatedBy,
	})
}

// Create stores the engine with its native catalogs and their bindings. It
// returns the native catalogs, whose metadata the caller syncs.
func (s *Service) Create(ctx context.Context, n NewEngine) (db.Engine, []db.UUID, error) {
	if err := CheckName(n.Name); err != nil {
		return db.Engine{}, nil, err
	}
	var created db.Engine
	var natives []db.UUID
	err := s.tx(ctx, func(q TxStore) error {
		var err error
		created, natives, err = CreateIn(ctx, q, n)
		return err
	})
	if err != nil {
		return db.Engine{}, nil, CreateError(err)
	}
	return created, natives, nil
}

// CreateIn writes the engine, its native catalogs and their bindings through q,
// which the caller has already bound to a transaction: it opens none, so a
// connection created in the same transaction commits or rolls back with it. The
// name is not checked here; use CheckName or NormalizeName first. A unique
// violation is returned as it came; CreateError turns it into ErrNameTaken or
// ErrConnectionTaken.
func CreateIn(ctx context.Context, q TxStore, n NewEngine) (db.Engine, []db.UUID, error) {
	eng, err := q.CreateEngine(ctx, db.CreateEngineParams{
		Name:         n.Name,
		Kind:         n.Kind,
		Class:        "direct",
		ConnectionID: n.ConnectionID,
		IdentityMode: "connection", // v1: the connection's credentials (docs/adr/0011)
		CreatedBy:    n.CreatedBy,
	})
	if err != nil {
		return db.Engine{}, nil, err
	}
	natives := make([]db.UUID, 0, len(n.Natives))
	for _, name := range n.Natives {
		cat, err := q.UpsertNativeCatalog(ctx, db.UpsertNativeCatalogParams{ConnectionID: n.ConnectionID, Name: name})
		if err != nil {
			return db.Engine{}, nil, err
		}
		natives = append(natives, cat.ID)
		if _, err := q.UpsertNativeBinding(ctx, db.UpsertNativeBindingParams{CatalogID: cat.ID, EngineID: eng.ID, EngineCatalogName: name}); err != nil {
			return db.Engine{}, nil, err
		}
	}
	return eng, natives, nil
}

// CreateError maps the unique violations CreateIn can hit to ErrConnectionTaken
// (the connection already backs an engine) and ErrNameTaken (another engine has
// the name). Any other error is returned unchanged.
func CreateError(err error) error {
	if pgErr, ok := db.AsPGError(err); ok && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "engines_connection_id_key" {
			return ErrConnectionTaken
		}
		return ErrNameTaken
	}
	return err
}

// SetEnabled enables or disables an engine; nil keeps it as it is.
func (s *Service) SetEnabled(ctx context.Context, id db.UUID, enabled *bool) (db.Engine, error) {
	return s.store.UpdateEngine(ctx, db.UpdateEngineParams{ID: id, Enabled: enabled})
}

// SetCostRouting opts an engine into cost-based classification (#550).
func (s *Service) SetCostRouting(ctx context.Context, id db.UUID, enabled *bool, threshold *int64) (db.Engine, error) {
	return s.store.UpdateEngine(ctx, db.UpdateEngineParams{
		ID:                 id,
		CostRoutingEnabled: enabled,
		CostThresholdBytes: threshold,
	})
}

// Update applies every engine field in one statement, so a refused or failed
// request cannot leave a partial change (#591).
func (s *Service) Update(ctx context.Context, p db.UpdateEngineParams) (db.Engine, error) {
	return s.store.UpdateEngine(ctx, p)
}

// Delete removes an engine and its native catalogs. Managed bindings,
// running executions or any other reference make it an InUseError.
func (s *Service) Delete(ctx context.Context, id db.UUID) error {
	blockers, err := s.store.CountEngineBlockers(ctx, id)
	if err != nil {
		return err
	}
	if blockers.ManagedBindings > 0 || blockers.RunningExecutions > 0 {
		return &InUseError{Msg: fmt.Sprintf("the engine has %d managed bindings and %d running executions", blockers.ManagedBindings, blockers.RunningExecutions)}
	}
	err = s.tx(ctx, func(q TxStore) error {
		if err := q.DeleteNativeCatalogsOfEngine(ctx, id); err != nil {
			return err
		}
		return q.DeleteEngine(ctx, id)
	})
	if IsFKViolation(err) {
		return &InUseError{Msg: "the engine is still referenced"}
	}
	return err
}

// Bindings lists an engine's bindings.
func (s *Service) Bindings(ctx context.Context, engineID db.UUID) ([]db.CatalogBinding, error) {
	return s.store.ListBindingsByEngine(ctx, engineID)
}

// CatalogBindings lists the bindings of a catalog.
func (s *Service) CatalogBindings(ctx context.Context, catalogID db.UUID) ([]db.CatalogBinding, error) {
	return s.store.ListBindingsByCatalog(ctx, catalogID)
}

// Observations lists the catalogs the engine reported, present or gone.
func (s *Service) Observations(ctx context.Context, engineID db.UUID) ([]db.EngineCatalogObservation, error) {
	return s.store.ListEngineObservations(ctx, engineID)
}

// Impact counts what depends on an engine.
func (s *Service) Impact(ctx context.Context, engineID db.UUID) (db.CountEngineImpactRow, error) {
	return s.store.CountEngineImpact(ctx, engineID)
}

// ReconcileRun returns one reconcile run of an engine.
func (s *Service) ReconcileRun(ctx context.Context, engineID, runID db.UUID) (db.CatalogReconcileRun, error) {
	if err := s.exists(ctx, engineID); err != nil {
		return db.CatalogReconcileRun{}, err
	}
	run, err := s.store.GetReconcileRun(ctx, db.GetReconcileRunParams{EngineID: engineID, ID: runID})
	if errors.Is(err, db.ErrNoRows) {
		return db.CatalogReconcileRun{}, ErrRunNotFound
	}
	return run, err
}

// ReconcileRuns lists an engine's latest reconcile runs. A limit outside
// 1..MaxRuns is DefaultRuns.
func (s *Service) ReconcileRuns(ctx context.Context, engineID db.UUID, limit int) ([]db.CatalogReconcileRun, error) {
	if err := s.exists(ctx, engineID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > MaxRuns {
		limit = DefaultRuns
	}
	return s.store.ListReconcileRuns(ctx, db.ListReconcileRunsParams{EngineID: engineID, Limit: int32(limit)})
}

func (s *Service) exists(ctx context.Context, id db.UUID) error {
	_, err := s.store.GetEngineByID(ctx, id)
	if errors.Is(err, db.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// IsFKViolation reports a delete blocked by a foreign key: 23503 for NO
// ACTION, 23001 for RESTRICT.
func IsFKViolation(err error) bool {
	e, ok := db.AsPGError(err)
	return ok && (e.Code == "23503" || e.Code == "23001")
}
