package api

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/catalogs/topology"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// snapshotFake answers the query snapshot lookup with err.
type snapshotFake struct {
	*authzFake
	err error
}

// GetEngineByID puts the engine on the fake's connection, so the engine
// access check passes.
func (q snapshotFake) GetEngineByID(_ context.Context, id pgtype.UUID) (db.Engine, error) {
	return db.Engine{ID: id, Name: "default", Kind: "trino", ConnectionID: q.conn.ID, Enabled: true}, nil
}

func (q snapshotFake) GetTrinoQuerySnapshotByEngine(context.Context, db.GetTrinoQuerySnapshotByEngineParams) (db.TrinoQuerySnapshot, error) {
	return db.TrinoQuerySnapshot{}, q.err
}

// TestMonitoringQuery_LookupErrors: an unknown query is a 404, and a failed
// lookup is a server error, not "query not found".
func TestMonitoringQuery_LookupErrors(t *testing.T) {
	h, q, _ := newAuthzHandlers(t)
	admin := ctxAs(ownerID, "admin")
	engine := [16]byte{0xE1}

	h.queries = snapshotFake{authzFake: q, err: pgx.ErrNoRows}
	h.access = wsaccess.New(h.queries)
	h.topology = topology.New(h.queries)
	h.monitor = monitoring.New(h.queries)
	if resp, err := h.GetMonitoringQuery(admin, GetMonitoringQueryRequestObject{Id: engine, QueryId: "q1"}); err != nil || !isType[GetMonitoringQuery404JSONResponse](resp) {
		t.Errorf("unknown query = %T, %v; want 404", resp, err)
	}
	if resp, err := h.KillMonitoringQuery(admin, KillMonitoringQueryRequestObject{Id: engine, QueryId: "q1"}); err != nil || !isType[KillMonitoringQuery404JSONResponse](resp) {
		t.Errorf("kill unknown query = %T, %v; want 404", resp, err)
	}

	h.queries = snapshotFake{authzFake: q, err: errors.New("connection reset")}
	h.access = wsaccess.New(h.queries)
	h.topology = topology.New(h.queries)
	h.monitor = monitoring.New(h.queries)
	if _, err := h.GetMonitoringQuery(admin, GetMonitoringQueryRequestObject{Id: engine, QueryId: "q1"}); err == nil {
		t.Error("GetMonitoringQuery answered a store failure with a response")
	}
	if _, err := h.KillMonitoringQuery(admin, KillMonitoringQueryRequestObject{Id: engine, QueryId: "q1"}); err == nil {
		t.Error("KillMonitoringQuery answered a store failure with a response")
	}
}

// brokenCatalogLookup fails the catalog lookup.
type brokenCatalogLookup struct{ *authzFake }

func (brokenCatalogLookup) GetCatalogByID(context.Context, pgtype.UUID) (db.Catalog, error) {
	return db.Catalog{}, errors.New("connection reset")
}

// TestCatalogForMonitoring_LookupFailureIsAnError: table health and
// maintenance answer a failed catalog lookup with a server error, not
// "catalog not found".
func TestCatalogForMonitoring_LookupFailureIsAnError(t *testing.T) {
	h, q, _ := newAuthzHandlers(t)
	h.queries = brokenCatalogLookup{q}
	h.access = wsaccess.New(h.queries)
	h.topology = topology.New(h.queries)
	h.monitor = monitoring.New(h.queries)
	admin := ctxAs(ownerID, "admin")
	cat := q.cat.ID.Bytes
	if _, err := h.ListTableHealth(admin, ListTableHealthRequestObject{CatalogId: cat}); err == nil {
		t.Error("ListTableHealth answered a store failure with a response")
	}
	if _, err := h.ListMaintenanceRuns(admin, ListMaintenanceRunsRequestObject{CatalogId: cat}); err == nil {
		t.Error("ListMaintenanceRuns answered a store failure with a response")
	}
	if _, err := h.CreateMaintenanceRun(admin, CreateMaintenanceRunRequestObject{CatalogId: cat}); err == nil {
		t.Error("CreateMaintenanceRun answered a store failure with a response")
	}
}

// catalogBindings lists one enabled binding for the fake's catalog.
type catalogBindings struct{ *authzFake }

func (q catalogBindings) ListBindingsByCatalog(_ context.Context, catalogID pgtype.UUID) ([]db.CatalogBinding, error) {
	return []db.CatalogBinding{{ID: pgtype.UUID{Bytes: [16]byte{0xB0}, Valid: true}, CatalogID: catalogID, EngineID: pgtype.UUID{Bytes: [16]byte{0xE1}, Valid: true}, Enabled: true}}, nil
}

// TestCreateMaintenanceRun_RejectsForeignBinding: an explicit binding_id that
// is not a binding of the catalog is a 422, and no run is recorded.
func TestCreateMaintenanceRun_RejectsForeignBinding(t *testing.T) {
	h, q, _ := newAuthzHandlers(t)
	h.monitor = monitoring.New(catalogBindings{q})
	admin := ctxAs(ownerID, "admin")
	foreign := openapi_types.UUID{0xB1}
	resp, err := h.CreateMaintenanceRun(admin, CreateMaintenanceRunRequestObject{
		CatalogId: q.cat.ID.Bytes,
		Body:      &CreateMaintenanceRunJSONRequestBody{Namespace: "n", Table: "t", Operations: []string{"optimize"}, BindingId: &foreign},
	})
	if err != nil || !isType[CreateMaintenanceRun422JSONResponse](resp) {
		t.Fatalf("foreign binding = %T, %v; want 422", resp, err)
	}
}
