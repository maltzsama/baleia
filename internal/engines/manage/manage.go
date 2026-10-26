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

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reader reads engines and bindings; *db.Queries implements it.
type Reader interface {
	ListEngines(ctx context.Context) ([]db.Engine, error)
	ListEnginesVisibleToUser(ctx context.Context, userID pgtype.UUID) ([]db.Engine, error)
	ListBindingsByEngine(ctx context.Context, engineID pgtype.UUID) ([]db.CatalogBinding, error)
	ListBindingsByCatalog(ctx context.Context, catalogID pgtype.UUID) ([]db.CatalogBinding, error)
	CountEngineBlockers(ctx context.Context, engineID pgtype.UUID) (db.CountEngineBlockersRow, error)
}

// Writer changes an engine; *db.Queries implements it.
type Writer interface {
	UpdateEngine(ctx context.Context, arg db.UpdateEngineParams) (db.Engine, error)
}

// Ops reads what an engine reports and its reconcile runs; *db.Queries
// implements it.
type Ops interface {
	GetEngineByID(ctx context.Context, id pgtype.UUID) (db.Engine, error)
	ListEngineObservations(ctx context.Context, engineID pgtype.UUID) ([]db.EngineCatalogObservation, error)
	CountEngineImpact(ctx context.Context, engineID pgtype.UUID) (db.CountEngineImpactRow, error)
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
	DeleteNativeCatalogsOfEngine(ctx context.Context, engineID pgtype.UUID) error
	DeleteEngine(ctx context.Context, id pgtype.UUID) error
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

// List returns every engine to an admin, and to anyone else the engines
// they may see.
func (s *Service) List(ctx context.Context, userID pgtype.UUID, admin bool) ([]db.Engine, error) {
	if admin {
		return s.store.ListEngines(ctx)
	}
	return s.store.ListEnginesVisibleToUser(ctx, userID)
}

// NewEngine is a direct engine to create over a connection.
type NewEngine struct {
	Name         string
	Kind         string
	ConnectionID pgtype.UUID
	CreatedBy    pgtype.UUID
	// Natives are the connection's native catalogs, each bound to the
	// engine under its own name.
	Natives []string
}

// Create stores the engine with its native catalogs and their bindings. It
// returns the native catalogs, whose metadata the caller syncs.
func (s *Service) Create(ctx context.Context, n NewEngine) (db.Engine, []pgtype.UUID, error) {
	if err := CheckName(n.Name); err != nil {
		return db.Engine{}, nil, err
	}
	var created db.Engine
	var natives []pgtype.UUID
	err := s.tx(ctx, func(q TxStore) error {
		eng, err := q.CreateEngine(ctx, db.CreateEngineParams{
			Name:         n.Name,
			Kind:         n.Kind,
			Class:        "direct",
			ConnectionID: n.ConnectionID,
			IdentityMode: "connection", // v1: the connection's credentials (docs/adr/0011)
			CreatedBy:    n.CreatedBy,
		})
		if err != nil {
			return err
		}
		created = eng
		for _, name := range n.Natives {
			cat, err := q.UpsertNativeCatalog(ctx, db.UpsertNativeCatalogParams{ConnectionID: n.ConnectionID, Name: name})
			if err != nil {
				return err
			}
			natives = append(natives, cat.ID)
			if _, err := q.UpsertNativeBinding(ctx, db.UpsertNativeBindingParams{CatalogID: cat.ID, EngineID: eng.ID, EngineCatalogName: name}); err != nil {
				return err
			}
		}
		return nil
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "engines_connection_id_key" {
			return db.Engine{}, nil, ErrConnectionTaken
		}
		return db.Engine{}, nil, ErrNameTaken
	}
	if err != nil {
		return db.Engine{}, nil, err
	}
	return created, natives, nil
}

// SetEnabled enables or disables an engine; nil keeps it as it is.
func (s *Service) SetEnabled(ctx context.Context, id pgtype.UUID, enabled *bool) (db.Engine, error) {
	return s.store.UpdateEngine(ctx, db.UpdateEngineParams{ID: id, Enabled: enabled})
}

// Delete removes an engine and its native catalogs. Managed bindings,
// running executions or any other reference make it an InUseError.
func (s *Service) Delete(ctx context.Context, id pgtype.UUID) error {
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
func (s *Service) Bindings(ctx context.Context, engineID pgtype.UUID) ([]db.CatalogBinding, error) {
	return s.store.ListBindingsByEngine(ctx, engineID)
}

// CatalogBindings lists the bindings of a catalog.
func (s *Service) CatalogBindings(ctx context.Context, catalogID pgtype.UUID) ([]db.CatalogBinding, error) {
	return s.store.ListBindingsByCatalog(ctx, catalogID)
}

// Observations lists the catalogs the engine reported, present or gone.
func (s *Service) Observations(ctx context.Context, engineID pgtype.UUID) ([]db.EngineCatalogObservation, error) {
	return s.store.ListEngineObservations(ctx, engineID)
}

// Impact counts what depends on an engine.
func (s *Service) Impact(ctx context.Context, engineID pgtype.UUID) (db.CountEngineImpactRow, error) {
	return s.store.CountEngineImpact(ctx, engineID)
}

// ReconcileRun returns one reconcile run of an engine.
func (s *Service) ReconcileRun(ctx context.Context, engineID, runID pgtype.UUID) (db.CatalogReconcileRun, error) {
	if err := s.exists(ctx, engineID); err != nil {
		return db.CatalogReconcileRun{}, err
	}
	run, err := s.store.GetReconcileRun(ctx, db.GetReconcileRunParams{EngineID: engineID, ID: runID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.CatalogReconcileRun{}, ErrRunNotFound
	}
	return run, err
}

// ReconcileRuns lists an engine's latest reconcile runs. A limit outside
// 1..MaxRuns is DefaultRuns.
func (s *Service) ReconcileRuns(ctx context.Context, engineID pgtype.UUID, limit int) ([]db.CatalogReconcileRun, error) {
	if err := s.exists(ctx, engineID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > MaxRuns {
		limit = DefaultRuns
	}
	return s.store.ListReconcileRuns(ctx, db.ListReconcileRunsParams{EngineID: engineID, Limit: int32(limit)})
}

func (s *Service) exists(ctx context.Context, id pgtype.UUID) error {
	_, err := s.store.GetEngineByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// IsFKViolation reports a delete blocked by a foreign key: 23503 for NO
// ACTION, 23001 for RESTRICT.
func IsFKViolation(err error) bool {
	var e *pgconn.PgError
	return errors.As(err, &e) && (e.Code == "23503" || e.Code == "23001")
}
