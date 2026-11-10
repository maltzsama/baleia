package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestBIGatewaySwitch covers the global BI gateway switch (#711): admins read
// and change it; a member is refused; a missing singleton is enabled.
func TestBIGatewaySwitch(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := repo.New(pool)
	ctx := context.Background()
	h := testAPI(handlerFields{queries: q, store: pool, tx: storeTx{db: pool}, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})

	pw := "x"
	admin, err := q.CreateUser(ctx, db.CreateUserParams{Name: "admin", Email: "bi-switch-admin@t", PasswordHash: &pw, Role: "admin", AuthProvider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := q.CreateUser(ctx, db.CreateUserParams{Name: "member", Email: "bi-switch-member@t", PasswordHash: &pw, Role: "member", AuthProvider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := ctxAs(idString(admin.ID), admin.Role)
	memberCtx := ctxAs(idString(member.ID), member.Role)

	if got, err := h.GetBIGateway(adminCtx, GetBIGatewayRequestObject{}); err != nil {
		t.Fatal(err)
	} else if resp, ok := got.(GetBIGateway200JSONResponse); !ok || !resp.Enabled {
		t.Fatalf("initial switch = %+v, want enabled", got)
	}
	if _, ok := mustGetBIGateway(memberCtx, t, h).(GetBIGateway403JSONResponse); !ok {
		t.Fatal("a member may not read the global BI switch")
	}

	off := false
	got, err := h.PutBIGateway(adminCtx, PutBIGatewayRequestObject{Body: &UpdateBIGatewayRequest{Enabled: off}})
	if err != nil {
		t.Fatal(err)
	}
	if resp, ok := got.(PutBIGateway200JSONResponse); !ok || resp.Enabled {
		t.Fatalf("put off = %+v", got)
	}
	setting, err := q.GetBIGatewaySetting(ctx)
	if err != nil || setting.Enabled {
		t.Fatalf("stored setting = %+v, %v", setting, err)
	}
	if resp, ok := mustGetBIGateway(adminCtx, t, h).(GetBIGateway200JSONResponse); !ok || resp.Enabled {
		t.Fatalf("get after off = %+v", resp)
	}

	on := true
	if got, err := h.PutBIGateway(adminCtx, PutBIGatewayRequestObject{Body: &UpdateBIGatewayRequest{Enabled: on}}); err != nil {
		t.Fatal(err)
	} else if resp, ok := got.(PutBIGateway200JSONResponse); !ok || !resp.Enabled {
		t.Fatalf("put on = %+v", got)
	}
}

func mustGetBIGateway(ctx context.Context, t *testing.T, h *API) GetBIGatewayResponseObject {
	t.Helper()
	got, err := h.GetBIGateway(ctx, GetBIGatewayRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestWorkspaceBISwitch: the workspace update exposes and persists bi_enabled.
func TestWorkspaceBISwitch(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := repo.New(pool)
	ctx := context.Background()
	h := testAPI(handlerFields{queries: q, store: pool, tx: storeTx{db: pool}, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})

	pw := "x"
	owner, err := q.CreateUser(ctx, db.CreateUserParams{Name: "owner", Email: "bi-ws-owner@t", PasswordHash: &pw, Role: "member", AuthProvider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "BI", Type: "personal", PolarisCatalogName: "ws_bi_switch", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	off := false
	got, err := h.UpdateWorkspace(ctxAs(idString(owner.ID), owner.Role), UpdateWorkspaceRequestObject{
		Id: PathId(ws.ID.Bytes), Body: &UpdateWorkspaceJSONRequestBody{BiEnabled: &off},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := got.(UpdateWorkspace200JSONResponse)
	if !ok || resp.BiEnabled == nil || *resp.BiEnabled {
		t.Fatalf("update = %+v, want bi_enabled false", got)
	}
	current, err := q.GetWorkspaceByID(ctx, ws.ID)
	if err != nil || current.BIEnabled {
		t.Fatalf("stored workspace = %+v, %v", current, err)
	}
}
