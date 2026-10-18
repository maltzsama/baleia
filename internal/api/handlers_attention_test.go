package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type attentionFixture struct {
	pool      *pgxpool.Pool
	h         *Handlers
	admin     context.Context
	owner     context.Context
	stranger  context.Context
	ownerID   string
	catalogID pgtype.UUID
	engineID  pgtype.UUID
	bindingID pgtype.UUID
	wsID      pgtype.UUID
	cache     *TTLStore[[]AttentionItem]
}

func newAttentionFixture(t *testing.T) *attentionFixture {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	f := &attentionFixture{pool: pool}

	user := func(email, role string) string {
		var id pgtype.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (name, email, role, password_hash) VALUES ($1, $1, $2, 'x') RETURNING id`,
			email, role).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return idString(id)
	}
	f.ownerID = user("owner@attention.test", "member")
	strangerID := user("stranger@attention.test", "member")
	adminID := user("admin@attention.test", "admin")
	f.admin = ctxAs(adminID, "admin")
	f.owner = ctxAs(f.ownerID, "member")
	f.stranger = ctxAs(strangerID, "member")

	conn := func(name, owner string) pgtype.UUID {
		var id pgtype.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO catalog_connections (name, type, url, created_by) VALUES ($1, 'polaris', 'http://polaris.test', $2) RETURNING id`,
			name, owner).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	catConn := conn("attention-source", f.ownerID)
	engConn := conn("attention-engine", f.ownerID)

	if err := pool.QueryRow(ctx,
		`INSERT INTO catalogs (connection_id, name, trino_catalog_name, cluster_id, warehouse, connector)
		 VALUES ($1, 'attention_catalog', 'attention_catalog', (SELECT id FROM trino_clusters LIMIT 1), 'wh', 'iceberg')
		 RETURNING id`, catConn).Scan(&f.catalogID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO engines (name, kind, class, connection_id, identity_mode, created_by)
		 VALUES ('attention_engine', 'trino', 'federated', $1, 'connection', $2) RETURNING id`,
		engConn, f.ownerID).Scan(&f.engineID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, mode, enabled, lifecycle_state, state_changed_at)
		 VALUES ($1, $2, 'attention_catalog', 'managed', true, 'error', now() - interval '1 hour') RETURNING id`,
		f.catalogID, f.engineID).Scan(&f.bindingID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id)
		 VALUES ('attention-ws', 'personal', 'attention_polaris', $1) RETURNING id`, f.ownerID).Scan(&f.wsID); err != nil {
		t.Fatal(err)
	}

	f.cache = NewTTLStore[[]AttentionItem](time.Hour)
	f.h = &Handlers{
		queries:        db.New(pool),
		pool:           pool,
		cfg:            &config.Config{AuditExportURL: "https://opensearch.test"},
		attentionCache: f.cache,
	}
	return f
}

func (f *attentionFixture) list(t *testing.T, ctx context.Context) []AttentionItem {
	t.Helper()
	got, err := f.h.ListAttention(ctx, ListAttentionRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := got.(ListAttention200JSONResponse)
	if !ok {
		t.Fatalf("list attention = %T", got)
	}
	return resp
}

func hasKind(items []AttentionItem, kind AttentionKind) bool {
	for _, it := range items {
		if it.Kind == kind {
			return true
		}
	}
	return false
}

// TestAttention_Scoped: the owner of a broken binding sees it; another member
// with no access does not.
func TestAttention_Scoped(t *testing.T) {
	f := newAttentionFixture(t)

	items := f.list(t, f.owner)
	if !hasKind(items, BindingError) {
		t.Fatalf("owner items = %+v, want binding_error", items)
	}
	found := false
	for _, it := range items {
		if it.Kind != BindingError {
			continue
		}
		found = true
		if it.Resource.Id != idString(f.bindingID) || it.Resource.Type != AttentionResourceTypeBinding {
			t.Errorf("binding resource = %+v", it.Resource)
		}
		if it.Severity != AttentionSeverityError || it.Href == nil {
			t.Errorf("binding item = %+v", it)
		}
	}
	if !found {
		t.Fatal("binding_error not found")
	}

	if got := f.list(t, f.stranger); hasKind(got, BindingError) {
		t.Errorf("stranger sees another user's binding error: %+v", got)
	}
}

// TestAttention_AdminOnlyKinds: drift and export lag are admin-only.
func TestAttention_AdminOnlyKinds(t *testing.T) {
	f := newAttentionFixture(t)
	ctx := context.Background()

	if _, err := f.pool.Exec(ctx,
		`INSERT INTO catalog_reconcile_runs (engine_id, started_at, finished_at, dry_run, actions)
		 VALUES ($1, now() - interval '5 minutes', now(), true, '[{"catalog":"c","kind":"create"}]'::jsonb)`,
		f.engineID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO audit_events (occurred_at, actor_kind, action, resource_type, outcome)
		 VALUES (now() - interval '30 minutes', 'user', 'catalog.update', 'catalog', 'allowed')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO audit_export_cursor (sink, occurred_at, id)
		 VALUES ('opensearch', now() - interval '1 hour', 0)`); err != nil {
		t.Fatal(err)
	}

	member := f.list(t, f.owner)
	if hasKind(member, ReconcileDrift) || hasKind(member, AuditExportLagging) || hasKind(member, ScheduleMissing) {
		t.Errorf("member sees admin-only kinds: %+v", member)
	}

	admin := f.list(t, f.admin)
	if !hasKind(admin, ReconcileDrift) {
		t.Errorf("admin does not see reconcile_drift: %+v", admin)
	}
	if !hasKind(admin, AuditExportLagging) {
		t.Errorf("admin does not see audit_export_lagging: %+v", admin)
	}
}

// TestAttention_Cache: the endpoint returns the 30 s cache and recomputes
// after it is cleared.
func TestAttention_Cache(t *testing.T) {
	f := newAttentionFixture(t)

	if !hasKind(f.list(t, f.owner), BindingError) {
		t.Fatal("binding_error not found")
	}

	// Fix it behind the cache's back.
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE catalog_bindings SET lifecycle_state = 'active' WHERE id = $1`, f.bindingID); err != nil {
		t.Fatal(err)
	}
	if !hasKind(f.list(t, f.owner), BindingError) {
		t.Fatal("the cached response changed before it expired")
	}

	// Force a recompute.
	f.cache.Clear()
	if hasKind(f.list(t, f.owner), BindingError) {
		t.Fatal("binding_error survived the cache clear")
	}
}

// TestAttention_ClearsWhenFixed: an item disappears when its condition clears.
func TestAttention_ClearsWhenFixed(t *testing.T) {
	f := newAttentionFixture(t)
	ctx := context.Background()

	if _, err := f.pool.Exec(ctx,
		`INSERT INTO metadata_sync_runs (catalog_id, status, finished_at, error_code, source_path)
		 VALUES ($1, 'failed', now() - interval '2 hours', 'engine_unreachable', 'provider')`, f.catalogID); err != nil {
		t.Fatal(err)
	}
	if !hasKind(f.list(t, f.owner), MetadataSyncFailed) {
		t.Fatalf("metadata_sync_failed not found")
	}

	// A newer successful pass clears it.
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO metadata_sync_runs (catalog_id, status, finished_at, source_path)
		 VALUES ($1, 'finished', now(), 'provider')`, f.catalogID); err != nil {
		t.Fatal(err)
	}
	f.cache.Clear()
	if hasKind(f.list(t, f.owner), MetadataSyncFailed) {
		t.Fatal("metadata_sync_failed survived a successful pass")
	}
}
