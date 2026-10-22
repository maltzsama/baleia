package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func id(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

// targetRow is binding 0xB<binding> of catalog 0xC<catalog> on an engine of
// the given kind.
func targetRow(binding, catalog byte, kind, permission string, enabled bool) db.ListWorkspaceTargetsRow {
	return db.ListWorkspaceTargetsRow{
		BindingID: id(0xB0 + binding), EngineCatalogName: "eng_cat_" + string('0'+binding), Mode: "managed", BindingEnabled: enabled,
		EngineID: id(0xE0 + binding), EngineName: kind + "-engine", EngineKind: kind, EngineEnabled: true,
		CatalogID: id(0xC0 + catalog), CatalogName: "cat" + string('0'+catalog), CatalogEnabled: true,
		Permission: permission,
	}
}

func tuuid(b byte) *openapi_types.UUID { u := openapi_types.UUID(id(b).Bytes); return &u }

func TestResolveTarget(t *testing.T) {
	one := targetRow(1, 1, "trino", "write", true)
	twoEngines := []db.ListWorkspaceTargetsRow{targetRow(1, 1, "trino", "write", true), targetRow(2, 1, "postgresql", "write", true)}
	schema := "sales"

	cases := []struct {
		name     string
		ws       db.Workspace
		rows     []db.ListWorkspaceTargetsRow
		req      *TargetRequest
		binding  byte // expected binding (0xB?) on success
		code     string
		readOnly bool
		schema   string
	}{
		{name: "explicit binding", rows: twoEngines, req: &TargetRequest{BindingId: tuuid(0xB2), Schema: &schema}, binding: 0xB2, schema: "sales"},
		{name: "catalog with one binding", rows: []db.ListWorkspaceTargetsRow{one}, req: &TargetRequest{CatalogId: tuuid(0xC1)}, binding: 0xB1},
		{name: "catalog with two bindings needs an engine", rows: twoEngines, req: &TargetRequest{CatalogId: tuuid(0xC1)}, code: "engine_required"},
		{name: "catalog plus engine", rows: twoEngines, req: &TargetRequest{CatalogId: tuuid(0xC1), EngineId: tuuid(0xE2)}, binding: 0xB2},
		{name: "workspace default", ws: db.Workspace{DefaultBindingID: id(0xB2), DefaultSchema: &schema}, rows: twoEngines, binding: 0xB2, schema: "sales"},
		{name: "default no longer usable", ws: db.Workspace{DefaultBindingID: id(0xB9)}, rows: twoEngines, code: "target_unavailable"},
		{name: "the single target", rows: []db.ListWorkspaceTargetsRow{one, targetRow(3, 2, "trino", "write", false)}, binding: 0xB1},
		{name: "ambiguous", rows: []db.ListWorkspaceTargetsRow{one, targetRow(2, 2, "trino", "write", true)}, code: "target_required"},
		{name: "no target at all", code: "target_required"},
		{name: "binding not granted", rows: []db.ListWorkspaceTargetsRow{one}, req: &TargetRequest{BindingId: tuuid(0xB7)}, code: "not_found"},
		{name: "catalog not granted", rows: []db.ListWorkspaceTargetsRow{one}, req: &TargetRequest{CatalogId: tuuid(0xC7)}, code: "not_found"},
		{name: "disabled binding", rows: []db.ListWorkspaceTargetsRow{targetRow(1, 1, "trino", "write", false)}, req: &TargetRequest{BindingId: tuuid(0xB1)}, code: "target_unavailable"},
		{name: "read grant is read-only", rows: []db.ListWorkspaceTargetsRow{targetRow(1, 1, "trino", "read", true)}, binding: 0xB1, readOnly: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &Handlers{queries: &fakeRunQueryQuerier{targets: c.rows}}
			got, err := h.ResolveTarget(context.Background(), c.ws, c.req)
			if c.code != "" {
				te, ok := asTargetError(err)
				if !ok || string(te.code) != c.code {
					t.Fatalf("err = %v, want %s", err, c.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.BindingID != id(c.binding) || got.ReadOnly != c.readOnly || got.Schema != c.schema {
				t.Fatalf("target = %+v", got)
			}
		})
	}
}

func runQuery(t *testing.T, fake *fakeRunQueryQuerier, target *TargetRequest) *httptest.ResponseRecorder {
	t.Helper()
	rec, _ := runQueryWith(t, fake, target)
	return rec
}

func runQueryWith(t *testing.T, fake *fakeRunQueryQuerier, target *TargetRequest) (*httptest.ResponseRecorder, *fakeTemporalClient) {
	t.Helper()
	tc := &fakeTemporalClient{}
	h := &Handlers{queries: fake, tx: directTx{q: fake}, cfg: &config.Config{StatementTimeout: time.Minute}, temporalClient: tc, kinds: testKinds(t, nil), dialects: dialect.NewRegistry()}
	resp, err := h.RunQuery(testContext(), RunQueryRequestObject{Id: PathId{0xAA}, Body: &RunQueryJSONRequestBody{Sql: "SELECT 1", Target: target}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitRunQueryResponse(rec); err != nil {
		t.Fatal(err)
	}
	return rec, tc
}

func TestRunQuery_StoresTarget(t *testing.T) {
	fake := &fakeRunQueryQuerier{
		exec:    db.QueryExecution{ID: id(0xBB), Status: "running"},
		targets: []db.ListWorkspaceTargetsRow{targetRow(1, 1, "trino", "read", true)},
	}
	schema := "tiny"
	rec, tc := runQueryWith(t, fake, &TargetRequest{BindingId: tuuid(0xB1), Schema: &schema})
	if rec.Code != 202 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	// The V2 workflow gets the resolved target and the caller's identity.
	if len(tc.started) != 1 {
		t.Fatalf("workflow args = %v", tc.started)
	}
	in, ok := tc.started[0].(workflows.QueryExecutionInput)
	if !ok {
		t.Fatalf("workflow input = %T", tc.started[0])
	}
	if in.Target.EngineID != id(0xE1) || in.Target.EngineCatalog != "eng_cat_1" || in.Target.Schema != "tiny" || !in.Target.ReadOnly ||
		in.Identity.UserID == "" || in.StatementTimeout != time.Minute || in.SQL != "SELECT 1" || in.Dialect != dialect.Trino {
		t.Fatalf("workflow input = %+v", in)
	}
	p := fake.created[0]
	if p.EngineID != id(0xE1) || p.TargetBindingID != id(0xB1) || p.TargetCatalogID != id(0xC1) || *p.TargetSchema != "tiny" || *p.TrinoCatalog != "eng_cat_1" {
		t.Fatalf("params = %+v", p)
	}
	var snap map[string]any
	if err := json.Unmarshal(p.TargetSnapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if snap["engine_kind"] != "trino" || snap["catalog_name"] != "cat1" || snap["engine_catalog"] != "eng_cat_1" || snap["schema"] != "tiny" || snap["read_only"] != true {
		t.Fatalf("snapshot = %v", snap)
	}
}

func TestRunQuery_TargetErrors(t *testing.T) {
	cases := map[string]struct {
		rows   []db.ListWorkspaceTargetsRow
		target *TargetRequest
		status int
		code   string
	}{
		"ambiguous":       {[]db.ListWorkspaceTargetsRow{targetRow(1, 1, "trino", "write", true), targetRow(2, 2, "trino", "write", true)}, nil, 422, "target_required"},
		"not granted":     {[]db.ListWorkspaceTargetsRow{targetRow(1, 1, "trino", "write", true)}, &TargetRequest{BindingId: tuuid(0xB9)}, 404, "not_found"},
		"direct engine":   {[]db.ListWorkspaceTargetsRow{targetRow(1, 1, "postgresql", "write", true)}, nil, 422, "engine_unsupported"},
		"disabled target": {[]db.ListWorkspaceTargetsRow{targetRow(1, 1, "trino", "write", false)}, &TargetRequest{BindingId: tuuid(0xB1)}, 422, "target_unavailable"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeRunQueryQuerier{targets: c.rows}
			rec := runQuery(t, fake, c.target)
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), `"code":"`+c.code+`"`) {
				t.Fatalf("response = %d %s", rec.Code, rec.Body)
			}
			if len(fake.created) != 0 {
				t.Fatal("an execution was created")
			}
		})
	}
}

func TestExecutionTarget(t *testing.T) {
	e := db.QueryExecution{
		EngineID:       id(0xE1),
		TargetSnapshot: []byte(`{"engine_kind":"trino","engine_name":"default","catalog_name":"lake","engine_catalog":"lake","schema":null,"read_only":false}`),
	}
	got := executionTarget(e)
	if got == nil || *got.EngineKind != "trino" || *got.CatalogName != "lake" || got.EngineId == nil || got.BindingId != nil {
		t.Fatalf("target = %+v", got)
	}
	if executionTarget(db.QueryExecution{}) != nil {
		t.Fatal("an execution without a snapshot has no target")
	}
}

func TestListWorkspaceTargets(t *testing.T) {
	fake := &fakeRunQueryQuerier{
		workspace: db.Workspace{DefaultBindingID: id(0xB2)},
		targets: []db.ListWorkspaceTargetsRow{
			targetRow(1, 1, "trino", "write", true),
			targetRow(2, 1, "postgresql", "read", true),
			targetRow(3, 2, "trino", "write", false),
		},
	}
	resp, err := (&Handlers{queries: fake, tx: directTx{q: fake}}).ListWorkspaceTargets(testContext(), ListWorkspaceTargetsRequestObject{Id: openapi_types.UUID{0xAA}})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.(ListWorkspaceTargets200JSONResponse)
	if len(got) != 2 {
		t.Fatalf("targets = %+v, want the two enabled ones", got)
	}
	if got[0].IsDefault || !got[1].IsDefault || got[1].Permission != "read" || got[1].EngineKind != "postgresql" {
		t.Fatalf("targets = %+v", got)
	}
}
