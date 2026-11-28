package api

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maltzsama/baleia/internal/db"
	"github.com/maltzsama/baleia/internal/store/model"
	"github.com/maltzsama/baleia/internal/store/repo"
	"github.com/maltzsama/baleia/internal/testdb"
)

// configuredBIGateway is a fully usable gateway configuration for the tests.
func configuredBIGateway() Settings {
	return Settings{BIGateway: BIGatewaySettings{
		Enabled:            true,
		URL:                "https://bi.example.com",
		ListenerConfigured: true,
		SecretConfigured:   true,
		URLReady:           true,
		MaxPerKey:          5,
		MaxPerWorkspace:    20,
	}}
}

// TestBIGatewayStatusReadiness covers the global readiness contract (#1102):
// the admin can tell configured, enabled, ready and blocked apart, with stable
// blocker codes and without any secret.
func TestBIGatewayStatusReadiness(t *testing.T) {
	t.Parallel()

	adminCtx := ctxAs("00000000-0000-0000-0000-000000000001", "admin")
	ctx := context.Background()
	cases := []struct {
		name       string
		settings   Settings
		disabled   bool
		wantState  BIGatewayStatusState
		configured bool
		blockers   []BIGatewayBlocker
	}{
		{
			name: "ready", settings: configuredBIGateway(),
			wantState: BIGatewayStatusStateReady, configured: true,
		},
		{
			name: "disabled switch", settings: configuredBIGateway(), disabled: true,
			wantState: BIGatewayStatusStateDisabled, configured: true,
			blockers: []BIGatewayBlocker{BIGatewayBlockerGatewayDisabled},
		},
		{
			name: "missing listener",
			settings: Settings{BIGateway: BIGatewaySettings{
				SecretConfigured: true, URL: "https://bi.example.com", URLReady: true,
			}},
			wantState: BIGatewayStatusStateBlocked,
			blockers:  []BIGatewayBlocker{BIGatewayBlockerListenerNotConfigured},
		},
		{
			name: "missing secret",
			settings: Settings{BIGateway: BIGatewaySettings{
				ListenerConfigured: true, URL: "https://bi.example.com", URLReady: true,
			}},
			wantState: BIGatewayStatusStateBlocked,
			blockers:  []BIGatewayBlocker{BIGatewayBlockerSecretNotConfigured},
		},
		{
			name: "missing public URL",
			settings: Settings{BIGateway: BIGatewaySettings{
				ListenerConfigured: true, SecretConfigured: true,
			}},
			wantState: BIGatewayStatusStateBlocked,
			blockers:  []BIGatewayBlocker{BIGatewayBlockerPublicUrlNotConfigured},
		},
		{
			name: "invalid public URL",
			settings: Settings{BIGateway: BIGatewaySettings{
				ListenerConfigured: true, SecretConfigured: true, URL: "https://bi.example.com/path",
			}},
			wantState: BIGatewayStatusStateBlocked,
			blockers:  []BIGatewayBlocker{BIGatewayBlockerPublicUrlInvalid},
		},
		{
			name: "public URL with credentials",
			settings: Settings{BIGateway: BIGatewaySettings{
				ListenerConfigured: true, SecretConfigured: true, URL: "https://operator:secret@bi.example.com",
			}},
			wantState: BIGatewayStatusStateBlocked,
			blockers:  []BIGatewayBlocker{BIGatewayBlockerPublicUrlInvalid},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pool := testdb.New(t) //nolint:contextcheck // the throwaway database opens its own background context
			q := repo.New(pool)
			if tc.disabled {
				if _, err := q.SetBIGatewayEnabled(ctx, db.SetBIGatewayEnabledParams{Enabled: false}); err != nil {
					t.Fatal(err)
				}
			}
			h := testAPI(handlerFields{queries: q, store: pool, settings: tc.settings, tx: storeTx{db: pool}})
			resp, err := h.GetBIGateway(adminCtx, GetBIGatewayRequestObject{})
			if err != nil {
				t.Fatal(err)
			}
			got, ok := resp.(GetBIGateway200JSONResponse)
			if !ok {
				t.Fatalf("resp = %T", resp)
			}
			if got.State != tc.wantState {
				t.Fatalf("state = %q, want %q", got.State, tc.wantState)
			}
			if got.Configured != tc.configured {
				t.Fatalf("configured = %v, want %v", got.Configured, tc.configured)
			}
			if !slices.Equal(got.BlockedBy, tc.blockers) {
				t.Fatalf("blocked_by = %v, want %v", got.BlockedBy, tc.blockers)
			}
			wantHealth := BIGatewayHealthStatusUnknown
			if tc.settings.BIGateway.ListenerConfigured {
				wantHealth = BIGatewayHealthStatusOk
			}
			if got.Health.Status != wantHealth {
				t.Fatalf("health = %q, want %q", got.Health.Status, wantHealth)
			}
			if got.Concurrency.MaxPerKey != tc.settings.BIGateway.MaxPerKey {
				t.Fatalf("max_per_key = %d", got.Concurrency.MaxPerKey)
			}
			if got.PublicUrl != nil {
				if tc.settings.BIGateway.URL == "" {
					t.Fatalf("public_url = %q for an unset URL", *got.PublicUrl)
				}
				if strings.Contains(*got.PublicUrl, "@") || strings.Contains(*got.PublicUrl, "secret") {
					t.Fatalf("public_url leaks userinfo: %q", *got.PublicUrl)
				}
			}
		})
	}
}

// TestBIGatewayPublicURLReady pins the accepted public URL shape: an http(s)
// origin with no path and no credentials.
func TestBIGatewayPublicURLReady(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"":                                       false,
		"https://bi.example.com":                 true,
		"https://bi.example.com/":                true,
		"http://bi.example.com:8443":             true,
		"https://bi.example.com/path":            false,
		"https://operator:secret@bi.example.com": false,
		"ftp://bi.example.com":                   false,
		"bi.example.com":                         false,
	}
	for raw, want := range cases {
		if got := biGatewayPublicURLReady(raw); got != want {
			t.Errorf("biGatewayPublicURLReady(%q) = %v, want %v", raw, got, want)
		}
	}
}

// TestWorkspaceBIStatus covers the workspace readiness contract (#1102): the
// workspace switch, the available Trino target, the caller's permissions and
// the blockers, with no secret.
func TestWorkspaceBIStatus(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := repo.New(pool)
	ctx := context.Background()
	h := testAPI(handlerFields{
		queries: q, store: pool, settings: configuredBIGateway(),
		tx: storeTx{db: pool}, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute),
	})

	pw := "x"
	mkUser := func(email string) db.User {
		u, err := q.CreateUser(ctx, db.CreateUserParams{Name: email, Email: email, PasswordHash: &pw, Role: "member", AuthProvider: "local"})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner, member, outsider := mkUser("bi-status-owner@t"), mkUser("bi-status-member@t"), mkUser("bi-status-outsider@t")
	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "BI", Type: "personal", PolarisCatalogName: "ws_bi_status", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{WorkspaceID: ws.ID, UserID: member.ID, Role: "editor", GrantedBy: owner.ID}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	conn := model.CatalogConnection{
		ID: model.UUID(repo.NewUUID()), Name: model.Text("bi-status-conn-" + repo.NewUUID()[:8]), Type: "trino",
		URL: "http://trino.test", CreatedAt: now, UpdatedAt: now,
	}
	if err := pool.Create(&conn).Error; err != nil {
		t.Fatal(err)
	}
	engine := model.Engine{
		ID: model.UUID(repo.NewUUID()), Name: model.Text("bi-status-trino-" + repo.NewUUID()[:8]), Kind: "trino", Class: "federated",
		ConnectionID: conn.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := pool.Create(&engine).Error; err != nil {
		t.Fatal(err)
	}
	catalog := model.Catalog{
		ID: model.UUID(repo.NewUUID()), ConnectionID: conn.ID, Name: model.Text("bi_status_lake"), Origin: "native",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := pool.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	if err := pool.Create(&model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: catalog.ID, EngineID: engine.ID,
		EngineCatalogName: "tpch_status", Mode: "native", Enabled: true, LifecycleState: "active",
		CreatedAt: now, UpdatedAt: now, StateChangedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := pool.Create(&model.WorkspaceCatalogGrant{
		WorkspaceID: model.UUID(repo.UUIDString(ws.ID)), CatalogID: catalog.ID,
		GrantedBy: model.UUID(repo.UUIDString(owner.ID)), GrantedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	status := func(caller db.User, workspace db.UUID) (WorkspaceBIStatus, GetWorkspaceBIStatusResponseObject) {
		t.Helper()
		resp, err := h.GetWorkspaceBIStatus(ctxAs(idString(caller.ID), caller.Role), GetWorkspaceBIStatusRequestObject{Id: PathId(workspace.Bytes)})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := resp.(GetWorkspaceBIStatus200JSONResponse)
		if !ok {
			return WorkspaceBIStatus{}, resp
		}
		return WorkspaceBIStatus(got), resp
	}

	// A workspace admin: ready, every key visible, keys creatable.
	admin, resp := status(owner, ws.ID)
	if _, ok := resp.(GetWorkspaceBIStatus200JSONResponse); !ok {
		t.Fatalf("owner status = %T", resp)
	}
	if !admin.WorkspaceBiEnabled || admin.State != WorkspaceBIStatusStateReady || len(admin.BlockedBy) != 0 {
		t.Fatalf("admin status = %+v", admin)
	}
	if admin.Gateway.State != BIGatewayReadinessStateReady {
		t.Fatalf("gateway = %+v", admin.Gateway)
	}
	if !admin.TrinoTargets.Available || admin.TrinoTargets.Count != 1 {
		t.Fatalf("trino targets = %+v", admin.TrinoTargets)
	}
	if !admin.CanCreateKeys || !admin.CanManageKeys {
		t.Fatalf("admin permissions = create:%v manage:%v", admin.CanCreateKeys, admin.CanManageKeys)
	}

	// A member: same readiness, keys for themselves only.
	memberStatus, _ := status(member, ws.ID)
	if !memberStatus.CanCreateKeys || memberStatus.CanManageKeys {
		t.Fatalf("member permissions = create:%v manage:%v", memberStatus.CanCreateKeys, memberStatus.CanManageKeys)
	}

	// An outsider cannot reach the workspace.
	if _, outsiderResp := status(outsider, ws.ID); outsiderResp == nil {
		t.Fatal("outsider status = nil")
	} else if _, ok := outsiderResp.(GetWorkspaceBIStatus404JSONResponse); !ok {
		t.Fatalf("outsider status = %T", outsiderResp)
	}

	// Disabling BI for the workspace blocks it with a stable code.
	off := false
	if _, err := h.UpdateWorkspace(ctxAs(idString(owner.ID), owner.Role), UpdateWorkspaceRequestObject{
		Id: PathId(ws.ID.Bytes), Body: &UpdateWorkspaceJSONRequestBody{BiEnabled: &off},
	}); err != nil {
		t.Fatal(err)
	}
	disabled, _ := status(owner, ws.ID)
	if disabled.State != WorkspaceBIStatusStateBlocked || !slices.Contains(disabled.BlockedBy, WorkspaceBIGatewayBlockerWorkspaceBiDisabled) {
		t.Fatalf("workspace disabled = %+v", disabled)
	}

	// Re-enabling it and turning the global switch off blocks on the gateway.
	on := true
	if _, err := h.UpdateWorkspace(ctxAs(idString(owner.ID), owner.Role), UpdateWorkspaceRequestObject{
		Id: PathId(ws.ID.Bytes), Body: &UpdateWorkspaceJSONRequestBody{BiEnabled: &on},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetBIGatewayEnabled(ctx, db.SetBIGatewayEnabledParams{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	globalOff, _ := status(owner, ws.ID)
	if globalOff.State != WorkspaceBIStatusStateBlocked || !slices.Contains(globalOff.BlockedBy, WorkspaceBIGatewayBlockerGatewayDisabled) {
		t.Fatalf("global off = %+v", globalOff)
	}
	if _, err := q.SetBIGatewayEnabled(ctx, db.SetBIGatewayEnabledParams{Enabled: true}); err != nil {
		t.Fatal(err)
	}

	// A workspace with no Trino target cannot issue a key.
	emptyWS, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Empty", Type: "personal", PolarisCatalogName: "ws_bi_empty", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	empty, _ := status(owner, emptyWS.ID)
	if empty.State != WorkspaceBIStatusStateBlocked || !slices.Contains(empty.BlockedBy, WorkspaceBIGatewayBlockerNoTrinoTargets) {
		t.Fatalf("empty workspace = %+v", empty)
	}
	if empty.CanCreateKeys {
		t.Fatal("a workspace with no Trino target cannot issue keys")
	}

	// An unconfigured gateway blocks readiness without exposing a secret.
	hUnconfigured := testAPI(handlerFields{
		queries: q, store: pool, tx: storeTx{db: pool},
		catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute),
	})
	respUnconfigured, err := hUnconfigured.GetWorkspaceBIStatus(ctxAs(idString(owner.ID), owner.Role), GetWorkspaceBIStatusRequestObject{Id: PathId(ws.ID.Bytes)})
	if err != nil {
		t.Fatal(err)
	}
	unconfigured, ok := respUnconfigured.(GetWorkspaceBIStatus200JSONResponse)
	if !ok {
		t.Fatalf("unconfigured = %T", respUnconfigured)
	}
	for _, blocker := range []WorkspaceBIGatewayBlocker{
		WorkspaceBIGatewayBlockerListenerNotConfigured,
		WorkspaceBIGatewayBlockerSecretNotConfigured,
		WorkspaceBIGatewayBlockerPublicUrlNotConfigured,
	} {
		if !slices.Contains(unconfigured.BlockedBy, blocker) {
			t.Fatalf("unconfigured blockers = %v, missing %q", unconfigured.BlockedBy, blocker)
		}
	}
}
