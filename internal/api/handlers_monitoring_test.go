package api

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/catalogs/topology"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/fake"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// detailFake answers the snapshot lookup with snap.
type detailFake struct {
	snapshotFake
	snap db.TrinoQuerySnapshot
}

func (q detailFake) GetTrinoQuerySnapshotByEngine(context.Context, db.GetTrinoQuerySnapshotByEngineParams) (db.TrinoQuerySnapshot, error) {
	return q.snap, nil
}

// resourcesEngine reports res, or err, for any query.
type resourcesEngine struct {
	queryengine.Engine
	res queryengine.QueryResources
	err error
}

func (e resourcesEngine) QueryResources(context.Context, string) (queryengine.QueryResources, error) {
	return e.res, e.err
}

// TestMonitoringQuery_DetailCarriesResources: the detail carries the list's
// CPU, time and I/O, plus what the engine reports the query used; an engine
// that no longer knows the query leaves the resources out (#407).
func TestMonitoringQuery_DetailCarriesResources(t *testing.T) {
	t.Parallel()
	h, q, _ := newAuthzHandlers(t)
	admin := ctxAs(ownerID, "admin")
	cpu, peak := int32(840), int64(1<<20)
	h.queries = detailFake{snapshotFake: snapshotFake{authzFake: q}, snap: db.TrinoQuerySnapshot{TrinoQueryID: "q1", State: "FINISHED", User: "ada", CpuMs: &cpu}}
	h.access = wsaccess.New(h.queries)
	h.topology = topology.New(h.queries)
	h.monitor = monitoring.New(h.queries)
	req := GetMonitoringQueryRequestObject{Id: [16]byte{0xE1}, QueryId: "q1"}

	h.engines = fixedEngines{resourcesEngine{Engine: fake.New(fake.Options{}), res: queryengine.QueryResources{PeakMemoryBytes: &peak}}}
	resp, err := h.GetMonitoringQuery(admin, req)
	detail, ok := resp.(GetMonitoringQuery200JSONResponse)
	if err != nil || !ok {
		t.Fatalf("detail = %T, %v", resp, err)
	}
	if detail.CpuMs == nil || *detail.CpuMs != 840 {
		t.Errorf("cpu_ms = %v, want the snapshot's 840", detail.CpuMs)
	}
	if detail.Resources == nil || detail.Resources.PeakMemoryBytes == nil || *detail.Resources.PeakMemoryBytes != peak {
		t.Errorf("resources = %+v, want the engine's peak memory", detail.Resources)
	}

	h.engines = fixedEngines{resourcesEngine{Engine: fake.New(fake.Options{}), err: queryengine.ErrQueryUnknown}}
	resp, err = h.GetMonitoringQuery(admin, req)
	if detail, ok := resp.(GetMonitoringQuery200JSONResponse); err != nil || !ok || detail.Resources != nil {
		t.Errorf("forgotten query = %T %v, want 200 without resources", resp, err)
	}
}
