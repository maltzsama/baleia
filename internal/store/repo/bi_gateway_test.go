package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestBIGatewayStore covers the execution origin, the running counters and
// the global switch (#711).
func TestBIGatewayStore(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	ctx := context.Background()
	r := New(gdb)
	user := createAPIKeyUser(t, gdb, "bi-gateway@example.test")

	now := time.Now().UTC()
	conn := model.CatalogConnection{ID: model.UUID(NewUUID()), Name: model.Text("bi-gw-" + NewUUID()[:8]), Type: "trino", URL: "http://trino.test", CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&conn).Error; err != nil {
		t.Fatal(err)
	}
	engine := model.Engine{ID: model.UUID(NewUUID()), Name: model.Text("bi-gw-" + NewUUID()[:8]), Kind: "trino", Class: "federated", ConnectionID: conn.ID, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&engine).Error; err != nil {
		t.Fatal(err)
	}
	ws, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "BI", Type: "personal", PolarisCatalogName: "ws_bi_gw", OwnerUserID: PGUUID(string(user.ID))})
	if err != nil {
		t.Fatal(err)
	}
	key, err := r.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID: PGUUID(string(user.ID)), Kind: "bi", WorkspaceID: ws.ID, EngineID: PGUUID(string(engine.ID)),
		Name: "key", KeyHash: "hash-bi-gw", Prefix: "bal_", Scopes: []string{"bi"},
	})
	if err != nil {
		t.Fatal(err)
	}

	exec, err := r.CreateQueryExecution(ctx, db.CreateQueryExecutionParams{
		WorkspaceID: ws.ID, UserID: key.UserID, Status: "running", Origin: "bi", BIKeyID: key.ID,
		Sql: "SELECT 1", EngineID: key.EngineID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if exec.Origin != "bi" || exec.BIKeyID.Bytes != key.ID.Bytes {
		t.Fatalf("execution = %+v", exec)
	}
	if n, err := r.CountRunningBIExecutionsByKey(ctx, key.ID); err != nil || n != 1 {
		t.Fatalf("key count = %d, %v", n, err)
	}
	if n, err := r.CountRunningBIExecutionsByWorkspace(ctx, ws.ID); err != nil || n != 1 {
		t.Fatalf("workspace count = %d, %v", n, err)
	}
	if _, err := r.UpdateQueryExecution(ctx, db.UpdateQueryExecutionParams{
		ID: exec.ID, Status: "finished", FinishedAt: db.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.CountRunningBIExecutionsByKey(ctx, key.ID); n != 0 {
		t.Fatalf("key count after finish = %d", n)
	}

	setting, err := r.GetBIGatewaySetting(ctx)
	if err != nil || !setting.Enabled {
		t.Fatalf("seeded setting = %+v, %v", setting, err)
	}
	if got, err := r.SetBIGatewayEnabled(ctx, db.SetBIGatewayEnabledParams{Enabled: false, UpdatedBy: key.UserID}); err != nil || got.Enabled {
		t.Fatalf("set off = %+v, %v", got, err)
	}
	if got, err := r.GetBIGatewaySetting(ctx); err != nil || got.Enabled {
		t.Fatalf("get off = %+v, %v", got, err)
	}
	if got, err := r.SetBIGatewayEnabled(ctx, db.SetBIGatewayEnabledParams{Enabled: true, UpdatedBy: key.UserID}); err != nil || !got.Enabled {
		t.Fatalf("set on = %+v, %v", got, err)
	}
}
