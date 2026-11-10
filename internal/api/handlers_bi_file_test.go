package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TestWorkspaceBIFile covers the BI data-source file (#713): the file points
// at the gateway, carries no credentials, and is refused while the gateway or
// the workspace has BI off, for an outsider, and for a non-Trino target.
func TestWorkspaceBIFile(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := repo.New(pool)
	ctx := context.Background()
	gateway := BIGatewaySettings{Enabled: true, URL: "https://bi.example.test"}
	h := testAPI(handlerFields{
		queries: q, store: pool, tx: storeTx{db: pool},
		catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute),
		settings:     Settings{BIGateway: gateway},
	})

	user := func(email string) db.User {
		pw := "x"
		u, err := q.CreateUser(ctx, db.CreateUserParams{Name: email, Email: email, PasswordHash: &pw, Role: "member", AuthProvider: "local"})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner, outsider := user("bi-file-owner@t"), user("bi-file-outsider@t")
	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "BI file", Type: "personal", PolarisCatalogName: "ws_bi_file", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !ws.BIEnabled {
		t.Fatal("a new workspace must allow BI")
	}

	// mkTarget creates a connection, engine, catalog, binding and grant on the
	// workspace; the engine catalog is the name the file must point at.
	mkTarget := func(kind, engineCatalog string) openapi_types.UUID {
		now := time.Now().UTC()
		conn := model.CatalogConnection{ID: model.UUID(repo.NewUUID()), Name: model.Text("bi-file-conn-" + repo.NewUUID()[:8]), Type: kind, URL: "http://trino.test", CreatedAt: now, UpdatedAt: now}
		if err := pool.Create(&conn).Error; err != nil {
			t.Fatal(err)
		}
		engine := model.Engine{ID: model.UUID(repo.NewUUID()), Name: model.Text("bi-file-" + repo.NewUUID()[:8]), Kind: kind, Class: "federated", ConnectionID: conn.ID, CreatedAt: now, UpdatedAt: now}
		if err := pool.Create(&engine).Error; err != nil {
			t.Fatal(err)
		}
		catalog := model.Catalog{ID: model.UUID(repo.NewUUID()), ConnectionID: conn.ID, Name: model.Text("bi_file_" + repo.NewUUID()[:8]), Origin: "native", Enabled: true, CreatedAt: now, UpdatedAt: now}
		if err := pool.Create(&catalog).Error; err != nil {
			t.Fatal(err)
		}
		if err := pool.Create(&model.CatalogBinding{
			ID: model.UUID(repo.NewUUID()), CatalogID: catalog.ID, EngineID: engine.ID,
			EngineCatalogName: model.Text(engineCatalog), Mode: "native", Enabled: true, LifecycleState: "active",
			CreatedAt: now, UpdatedAt: now, StateChangedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := pool.Create(&model.WorkspaceCatalogGrant{WorkspaceID: model.UUID(repo.UUIDString(ws.ID)), CatalogID: catalog.ID, GrantedBy: model.UUID(repo.UUIDString(owner.ID)), GrantedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		return openapi_types.UUID(repo.PGUUID(string(engine.ID)).Bytes)
	}
	trinoID := mkTarget("trino", "tpch_test")
	postgresID := mkTarget("postgresql", "public")

	call := func(caller db.User, tool string, engineID openapi_types.UUID, catalog string, schema, table *string) GetWorkspaceBIFileResponseObject {
		t.Helper()
		resp, err := h.GetWorkspaceBIFile(ctxAs(idString(caller.ID), caller.Role), GetWorkspaceBIFileRequestObject{
			Id: PathId(ws.ID.Bytes), Tool: tool,
			Params: GetWorkspaceBIFileParams{EngineId: engineID, Catalog: catalog, Schema: schema, Table: table},
		})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	biPtr := func(s string) *string { return &s }

	// Tableau: the file names the gateway, the engine catalog, schema and table.
	resp := call(owner, "tableau", trinoID, "tpch_test", biPtr("tpch"), biPtr("orders"))
	file, ok := resp.(biFileResponse)
	if !ok {
		t.Fatalf("tableau = %T, want a file", resp)
	}
	for _, want := range []string{
		"server='bi.example.test'", "port='443'", "catalog='tpch_test'", "schema='tpch'",
		"table='tpch.orders'", "class='trino'",
	} {
		if !strings.Contains(file.body, want) {
			t.Errorf("tableau body missing %q:\n%s", want, file.body)
		}
	}
	if strings.Contains(strings.ToLower(file.body), "password") {
		t.Error("the file must carry no credentials")
	}
	if file.filename != "orders.tds" {
		t.Errorf("filename = %q", file.filename)
	}
	rec := httptest.NewRecorder()
	if err := file.VisitGetWorkspaceBIFileResponse(rec); err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="orders.tds"` {
		t.Errorf("Content-Disposition = %q", got)
	}

	// Power BI: DirectQuery against the same gateway and catalog.
	resp = call(owner, "powerbi", trinoID, "tpch_test", biPtr("tpch"), nil)
	file, ok = resp.(biFileResponse)
	if !ok {
		t.Fatalf("powerbi = %T, want a file", resp)
	}
	var doc struct {
		Connections []struct {
			Details struct {
				Protocol string `json:"protocol"`
				Address  struct {
					Host string `json:"host"`
					Port string `json:"port"`
				} `json:"address"`
				Database string `json:"database"`
				Schema   string `json:"schema"`
			} `json:"details"`
			Mode string `json:"mode"`
		} `json:"connections"`
	}
	if err := json.Unmarshal([]byte(file.body), &doc); err != nil {
		t.Fatalf("powerbi body: %v\n%s", err, file.body)
	}
	conn := doc.Connections[0]
	if conn.Details.Protocol != "trino" || conn.Details.Address.Host != "bi.example.test" || conn.Details.Address.Port != "443" ||
		conn.Details.Database != "tpch_test" || conn.Details.Schema != "tpch" || conn.Mode != "DirectQuery" {
		t.Errorf("powerbi = %+v", conn)
	}
	if file.filename != "tpch_test.pbids" {
		t.Errorf("filename = %q", file.filename)
	}

	// A name with XML and header metacharacters is escaped and sanitized.
	resp = call(owner, "tableau", trinoID, "tpch_test", nil, biPtr("orders' onclick='x"))
	file, ok = resp.(biFileResponse)
	if !ok {
		t.Fatalf("escaped = %T", resp)
	}
	if !strings.Contains(file.body, "&#39;") || strings.Contains(file.body, "onclick='x'") {
		t.Errorf("the quote must be escaped so it cannot open an attribute: %s", file.body)
	}
	if strings.ContainsAny(file.filename, " '\"") {
		t.Errorf("filename = %q", file.filename)
	}

	// Gates and scoping.
	if got := call(outsider, "tableau", trinoID, "tpch_test", nil, nil); !isNotFound(got) {
		t.Errorf("outsider = %T, want 404", got)
	}
	if got := call(owner, "tableau", postgresID, "public", nil, nil); !isUnprocessable(got) {
		t.Errorf("non-trino = %T, want 422", got)
	}
	if got := call(owner, "tableau", trinoID, "not_granted", nil, nil); !isNotFound(got) {
		t.Errorf("catalog outside the scope = %T, want 404", got)
	}

	off := false
	if _, err := q.UpdateWorkspace(ctx, db.UpdateWorkspaceParams{ID: ws.ID, Name: ws.Name, Description: ws.Description, BIEnabled: &off}); err != nil {
		t.Fatal(err)
	}
	if got := call(owner, "tableau", trinoID, "tpch_test", nil, nil); !isForbidden(got) {
		t.Errorf("workspace BI off = %T, want 403", got)
	}
	on := true
	if _, err := q.UpdateWorkspace(ctx, db.UpdateWorkspaceParams{ID: ws.ID, Name: ws.Name, Description: ws.Description, BIEnabled: &on}); err != nil {
		t.Fatal(err)
	}

	if _, err := q.SetBIGatewayEnabled(ctx, db.SetBIGatewayEnabledParams{Enabled: false, UpdatedBy: owner.ID}); err != nil {
		t.Fatal(err)
	}
	if got := call(owner, "tableau", trinoID, "tpch_test", nil, nil); !isForbidden(got) {
		t.Errorf("global switch off = %T, want 403", got)
	}
	if _, err := q.SetBIGatewayEnabled(ctx, db.SetBIGatewayEnabledParams{Enabled: true, UpdatedBy: owner.ID}); err != nil {
		t.Fatal(err)
	}

	h.settings.BIGateway = gateway
	if !h.features(ctx).BiGateway {
		t.Error("/auth/me must advertise BI files while the gateway and the global switch are on")
	}
	if _, err := q.SetBIGatewayEnabled(ctx, db.SetBIGatewayEnabledParams{Enabled: false, UpdatedBy: owner.ID}); err != nil {
		t.Fatal(err)
	}
	if h.features(ctx).BiGateway {
		t.Error("/auth/me must hide BI files while the global switch is off")
	}
	if _, err := q.SetBIGatewayEnabled(ctx, db.SetBIGatewayEnabledParams{Enabled: true, UpdatedBy: owner.ID}); err != nil {
		t.Fatal(err)
	}

	h.settings.BIGateway = BIGatewaySettings{}
	if got := call(owner, "tableau", trinoID, "tpch_test", nil, nil); !isUnprocessable(got) {
		t.Errorf("gateway disabled = %T, want 422", got)
	}
	h.settings.BIGateway = BIGatewaySettings{Enabled: true, URL: "https://bi.example.test/prefix"}
	if got := call(owner, "tableau", trinoID, "tpch_test", nil, nil); !isUnprocessable(got) {
		t.Errorf("public URL with a path = %T, want 422", got)
	}
	h.settings.BIGateway = gateway
}

func isNotFound(resp GetWorkspaceBIFileResponseObject) bool {
	_, ok := resp.(GetWorkspaceBIFile404JSONResponse)
	return ok
}

func isForbidden(resp GetWorkspaceBIFileResponseObject) bool {
	_, ok := resp.(GetWorkspaceBIFile403JSONResponse)
	return ok
}

func isUnprocessable(resp GetWorkspaceBIFileResponseObject) bool {
	_, ok := resp.(GetWorkspaceBIFile422JSONResponse)
	return ok
}
