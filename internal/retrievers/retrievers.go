// Package retrievers materializes a SQL definition into an Iceberg table on a
// cron schedule (#118). It validates definitions, destinations and schedules,
// stores retrievers and starts their refresh workflow. The caller checks
// access to the workspace.
package retrievers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/policy"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/retrievers/schedule"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/client"
)

// Retrievers stores retrievers; *db.Queries implements it.
type Retrievers interface {
	ListRetrieversByWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]db.Retriever, error)
	GetRetrieverByID(ctx context.Context, arg db.GetRetrieverByIDParams) (db.Retriever, error)
	CreateRetriever(ctx context.Context, arg db.CreateRetrieverParams) (db.Retriever, error)
	UpdateRetriever(ctx context.Context, arg db.UpdateRetrieverParams) (db.Retriever, error)
	DeleteRetriever(ctx context.Context, arg db.DeleteRetrieverParams) (int64, error)
}

// Refreshes reads refresh runs; *db.Queries implements it.
type Refreshes interface {
	ListRetrieverRefreshHistory(ctx context.Context, arg db.ListRetrieverRefreshHistoryParams) ([]db.RetrieverRefreshHistory, error)
	LatestRetrieverRefresh(ctx context.Context, retrieverID pgtype.UUID) (db.LatestRetrieverRefreshRow, error)
}

// Destinations reads where a retriever can write; *db.Queries implements it.
type Destinations interface {
	ListRetrieverDestinations(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListRetrieverDestinationsRow, error)
	GetBinding(ctx context.Context, id pgtype.UUID) (db.CatalogBinding, error)
	GetCatalogByID(ctx context.Context, id pgtype.UUID) (db.Catalog, error)
	GetEngineByID(ctx context.Context, id pgtype.UUID) (db.Engine, error)
}

// Store is every read and write the service needs.
type Store interface {
	Retrievers
	Refreshes
	Destinations
}

// Workflows starts Temporal workflows; client.Client implements it.
type Workflows interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error)
}

// Engines finds the engine behind a binding; the engine manager implements it.
type Engines interface {
	Get(ctx context.Context, engineID pgtype.UUID) (queryengine.Engine, error)
}

var (
	// ErrNotFound: no retriever has this id in the workspace.
	ErrNotFound = errors.New("retrievers: not found")
	// ErrRefreshNotSupported: only full refresh exists in v1.
	ErrRefreshNotSupported = errors.New("only 'full' refresh is supported in v1; 'incremental' requires a separate design")
	// ErrDestinationNotEligible: the destination is not an active Iceberg
	// binding on a federated engine that the workspace may write to.
	ErrDestinationNotEligible = errors.New("the destination must be an active Iceberg binding on a federated engine with a write grant")
	// ErrBindingNotFound: the destination binding is gone.
	ErrBindingNotFound = errors.New("retrievers: binding not found")
	// ErrEngineUnavailable: the destination engine cannot be reached.
	ErrEngineUnavailable = errors.New("the destination engine is not available")
	// ErrExplainNotSupported: the engine cannot explain the definition.
	ErrExplainNotSupported = errors.New("this engine cannot explain the definition")
)

// InvalidError is a rejected field, with the message the client sees.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(err error) error { return &InvalidError{Msg: err.Error()} }

// FullRefresh is the only refresh type in v1.
const FullRefresh = "full"

// TaskQueue is the worker queue the refresh workflow runs on.
const TaskQueue = "baleia-task-queue"

// Service manages retrievers.
type Service struct {
	store     Store
	workflows Workflows
	engines   Engines
	// trino splits definitions: retrievers refresh on the default Trino engine.
	trino dialect.Dialect
}

// New builds the service; trino is the Trino dialect.
func New(store Store, workflows Workflows, engines Engines, trino dialect.Dialect) *Service {
	return &Service{store: store, workflows: workflows, engines: engines, trino: trino}
}

// List returns the workspace's retrievers.
func (s *Service) List(ctx context.Context, ws pgtype.UUID) ([]db.Retriever, error) {
	return s.store.ListRetrieversByWorkspace(ctx, ws)
}

// Get returns one retriever of the workspace.
func (s *Service) Get(ctx context.Context, ws, id pgtype.UUID) (db.Retriever, error) {
	r, err := s.store.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{WorkspaceID: ws, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Retriever{}, ErrNotFound
	}
	return r, err
}

// Draft is a retriever to create. The destination is a binding id or, for
// legacy clients, a catalog name.
type Draft struct {
	Name          string
	DefinitionSQL string
	DestBindingID *pgtype.UUID
	DestCatalog   string
	DestNamespace string
	DestTable     string
	CronSchedule  string
	// RefreshType defaults to FullRefresh.
	RefreshType string
}

// Create validates and stores a retriever created by createdBy, scheduled
// for the next run of its cron.
func (s *Service) Create(ctx context.Context, ws, createdBy pgtype.UUID, in Draft) (db.Retriever, error) {
	for _, f := range []struct{ value, name string }{
		{in.DestNamespace, "dest_namespace"},
		{in.DestTable, "dest_table"},
	} {
		if err := ValidateIdentifier(f.value, f.name); err != nil {
			return db.Retriever{}, invalid(err)
		}
	}
	if err := s.ValidateDefinition(in.DefinitionSQL); err != nil {
		return db.Retriever{}, invalid(err)
	}
	refreshType := in.RefreshType
	if refreshType == "" {
		refreshType = FullRefresh
	}
	if refreshType != FullRefresh {
		return db.Retriever{}, ErrRefreshNotSupported
	}
	dest, err := s.resolveDestination(ctx, ws, in.DestBindingID, in.DestCatalog)
	if err != nil {
		return db.Retriever{}, err
	}
	sched, loc, err := schedule.Parse(in.CronSchedule)
	if err != nil {
		return db.Retriever{}, invalid(err)
	}
	return s.store.CreateRetriever(ctx, db.CreateRetrieverParams{
		WorkspaceID:   ws,
		CreatedBy:     createdBy,
		Name:          in.Name,
		DefinitionSql: in.DefinitionSQL,
		DestCatalog:   dest.EngineCatalogName,
		DestNamespace: in.DestNamespace,
		DestTable:     in.DestTable,
		DestBindingID: dest.BindingID,
		CronSchedule:  in.CronSchedule,
		RefreshType:   refreshType,
		NextRefreshAt: pgtype.Timestamptz{Time: sched.Next(time.Now().In(loc)), Valid: true},
	})
}

// Change is a partial update; nil fields keep their value.
type Change struct {
	Name          *string
	DefinitionSQL *string
	DestBindingID *pgtype.UUID
	DestNamespace *string
	DestTable     *string
	CronSchedule  *string
	RefreshType   *string
	Status        *string
}

// Update applies change to a retriever of the workspace. An edit with a
// valid destination clears a previous destination error.
func (s *Service) Update(ctx context.Context, ws, id pgtype.UUID, change Change) (db.Retriever, error) {
	current, err := s.Get(ctx, ws, id)
	if err != nil {
		return db.Retriever{}, err
	}
	next := current
	pick := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	pick(&next.Name, change.Name)
	pick(&next.DefinitionSql, change.DefinitionSQL)
	pick(&next.DestNamespace, change.DestNamespace)
	pick(&next.DestTable, change.DestTable)
	pick(&next.CronSchedule, change.CronSchedule)
	pick(&next.RefreshType, change.RefreshType)
	pick(&next.Status, change.Status)
	if change.DestBindingID != nil {
		next.DestBindingID = *change.DestBindingID
	}

	if next.RefreshType != FullRefresh {
		return db.Retriever{}, ErrRefreshNotSupported
	}
	if err := s.ValidateDefinition(next.DefinitionSql); err != nil {
		return db.Retriever{}, invalid(err)
	}
	if err := ValidateIdentifier(next.DestNamespace, "dest_namespace"); err != nil {
		return db.Retriever{}, invalid(err)
	}
	if err := ValidateIdentifier(next.DestTable, "dest_table"); err != nil {
		return db.Retriever{}, invalid(err)
	}
	// The current binding first, then the legacy catalog name.
	var dest db.ListRetrieverDestinationsRow
	if next.DestBindingID.Valid {
		dest, err = s.resolveDestination(ctx, ws, &next.DestBindingID, "")
	} else {
		dest, err = s.resolveDestination(ctx, ws, nil, current.DestCatalog)
	}
	if err != nil {
		return db.Retriever{}, err
	}
	sched, loc, err := schedule.Parse(next.CronSchedule)
	if err != nil {
		return db.Retriever{}, invalid(err)
	}
	if next.Status == "error" {
		next.Status = "active"
	}
	return s.store.UpdateRetriever(ctx, db.UpdateRetrieverParams{
		WorkspaceID:   ws,
		ID:            id,
		Name:          next.Name,
		DefinitionSql: next.DefinitionSql,
		DestCatalog:   dest.EngineCatalogName,
		DestNamespace: next.DestNamespace,
		DestTable:     next.DestTable,
		DestBindingID: dest.BindingID,
		CronSchedule:  next.CronSchedule,
		RefreshType:   next.RefreshType,
		Status:        next.Status,
		ErrorCode:     nil,
		NextRefreshAt: pgtype.Timestamptz{Time: sched.Next(time.Now().In(loc)), Valid: true},
	})
}

// Delete removes a retriever of the workspace.
func (s *Service) Delete(ctx context.Context, ws, id pgtype.UUID) error {
	n, err := s.store.DeleteRetriever(ctx, db.DeleteRetrieverParams{WorkspaceID: ws, ID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Refresh starts a refresh of a retriever of the workspace now. An empty
// refreshType uses the retriever's own.
func (s *Service) Refresh(ctx context.Context, ws, id pgtype.UUID, refreshType string) error {
	r, err := s.Get(ctx, ws, id)
	if err != nil {
		return err
	}
	if refreshType == "" {
		refreshType = r.RefreshType
	}
	if refreshType != FullRefresh {
		return ErrRefreshNotSupported
	}
	runID := fmt.Sprintf("%x-%s", id.Bytes, time.Now().Format("20060102150405"))
	_, err = s.workflows.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "retriever-refresh-" + runID, TaskQueue: TaskQueue},
		workflows.RetrieverRefreshWorkflow, uuid.UUID(r.ID.Bytes).String(), refreshType)
	return err
}

// HistoryLimit is how many refresh runs History returns.
const HistoryLimit = 50

// History returns the latest refresh runs of a retriever of the workspace;
// a retriever of another workspace is ErrNotFound.
func (s *Service) History(ctx context.Context, ws, id pgtype.UUID) ([]db.RetrieverRefreshHistory, error) {
	if _, err := s.Get(ctx, ws, id); err != nil {
		return nil, err
	}
	return s.store.ListRetrieverRefreshHistory(ctx, db.ListRetrieverRefreshHistoryParams{RetrieverID: id, Limit: HistoryLimit})
}

// LastRefreshStatus is the status of the latest refresh run, if any.
func (s *Service) LastRefreshStatus(ctx context.Context, id pgtype.UUID) (string, bool) {
	latest, err := s.store.LatestRetrieverRefresh(ctx, id)
	if err != nil {
		return "", false
	}
	return latest.Status, true
}

// Destinations lists where the workspace's retrievers can write.
func (s *Service) Destinations(ctx context.Context, ws pgtype.UUID) ([]db.ListRetrieverDestinationsRow, error) {
	return s.store.ListRetrieverDestinations(ctx, ws)
}

// Destination is the binding, catalog and engine a retriever writes to.
type Destination struct {
	Binding db.CatalogBinding
	Catalog db.Catalog
	Engine  db.Engine
}

// DestinationOf loads a retriever's destination; false when the binding,
// its catalog or its engine is gone.
func (s *Service) DestinationOf(ctx context.Context, bindingID pgtype.UUID) (Destination, bool) {
	binding, err := s.store.GetBinding(ctx, bindingID)
	if err != nil {
		return Destination{}, false
	}
	catalog, err := s.store.GetCatalogByID(ctx, binding.CatalogID)
	if err != nil {
		return Destination{}, false
	}
	engine, err := s.store.GetEngineByID(ctx, binding.EngineID)
	if err != nil {
		return Destination{}, false
	}
	return Destination{Binding: binding, Catalog: catalog, Engine: engine}, true
}

// resolveDestination picks the destination by binding id or, for legacy
// clients, by catalog name, among those the workspace may write to.
func (s *Service) resolveDestination(ctx context.Context, ws pgtype.UUID, bindingID *pgtype.UUID, catalogName string) (db.ListRetrieverDestinationsRow, error) {
	rows, err := s.store.ListRetrieverDestinations(ctx, ws)
	if err != nil {
		return db.ListRetrieverDestinationsRow{}, err
	}
	for _, row := range rows {
		switch {
		case bindingID != nil && row.BindingID.Bytes == bindingID.Bytes,
			bindingID == nil && catalogName != "" && (row.EngineCatalogName == catalogName || row.CatalogName == catalogName):
			return row, nil
		}
	}
	return db.ListRetrieverDestinationsRow{}, ErrDestinationNotEligible
}

// Validate checks a definition and explains it on the destination engine in
// a read-only session with identity, the caller's engine user (#118). An
// engine rejection is an *InvalidError.
func (s *Service) Validate(ctx context.Context, ws, bindingID pgtype.UUID, definition, identity string) error {
	if err := s.ValidateDefinition(definition); err != nil {
		return invalid(err)
	}
	if _, err := s.resolveDestination(ctx, ws, &bindingID, ""); err != nil {
		return err
	}
	if s.engines == nil {
		return errors.New("retriever validation: no engine registry")
	}
	binding, err := s.store.GetBinding(ctx, bindingID)
	if err != nil {
		return ErrBindingNotFound
	}
	eng, err := s.engines.Get(ctx, binding.EngineID)
	if err != nil {
		return ErrEngineUnavailable
	}
	explained, supported := eng.Dialect().ExplainSQL(definition)
	if !supported {
		return ErrExplainNotSupported
	}
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := queryengine.CollectStrings(vctx, eng, queryengine.SessionSpec{
		ReadOnly: true,
		Catalog:  binding.EngineCatalogName,
		Identity: queryengine.Identity{UserID: identity},
		Tags:     queryengine.Tags{Source: "retriever-validate"},
	}, explained, 1); err != nil {
		return invalid(err)
	}
	return nil
}

var identifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ValidateIdentifier checks a destination identifier (namespace or table)
// before it is quoted into SQL. v1 restricts it to the shape the UI needs.
func ValidateIdentifier(value, field string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if !identifier.MatchString(value) {
		return fmt.Errorf("%s must match ^[a-zA-Z_][a-zA-Z0-9_]*$", field)
	}
	return nil
}

// ValidateDefinition requires exactly one SELECT/WITH statement and rejects
// catalog DDL.
func (s *Service) ValidateDefinition(def string) error {
	stmts, err := s.trino.SplitStatements(def)
	if err != nil {
		return fmt.Errorf("definition_sql: %w", err)
	}
	if len(stmts) != 1 {
		return errors.New("definition_sql must be exactly one statement")
	}
	if policy.IsCatalogDDL(stmts[0]) {
		return errors.New("catalog management is not allowed in a retriever definition")
	}
	lower := strings.ToLower(strings.TrimSpace(stmts[0]))
	if !strings.HasPrefix(lower, "select ") && !strings.HasPrefix(lower, "with ") &&
		lower != "select" && lower != "with" {
		return errors.New("definition_sql must start with SELECT or WITH")
	}
	return nil
}
