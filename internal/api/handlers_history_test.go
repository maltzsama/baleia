package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// historyFixture is a workspace owned by `owner` with `member` and `stranger`
// as non-owners, plus the catalog/engine/binding the executions ran on.
type historyFixture struct {
	pool     *pgxpool.Pool
	h        *API
	owner    openapi_types.UUID
	member   openapi_types.UUID
	stranger openapi_types.UUID
	ws       openapi_types.UUID
	engine   openapi_types.UUID
	catalog  openapi_types.UUID
	binding  openapi_types.UUID
}

func newHistoryFixture(t *testing.T) *historyFixture {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	f := &historyFixture{pool: pool}

	scan := func(dst *openapi_types.UUID, q string, args ...any) {
		t.Helper()
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, q, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		*dst = openapi_types.UUID(id.Bytes)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	scan(&f.owner, `INSERT INTO users (name, email, password_hash) VALUES ('owner','owner@x','x') RETURNING id`)
	scan(&f.member, `INSERT INTO users (name, email, password_hash) VALUES ('member','member@x','x') RETURNING id`)
	scan(&f.stranger, `INSERT INTO users (name, email, password_hash) VALUES ('stranger','stranger@x','x') RETURNING id`)
	// A shared workspace: the owner owns the team, the member and the admin are
	// plain team members (the admin gets visibility from their global role).
	var team openapi_types.UUID
	scan(&team, `INSERT INTO teams (name, created_by) VALUES ('team',$1) RETURNING id`, f.owner)
	exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1,$2,'owner'), ($1,$3,'member'), ($1,$4,'member')`,
		team, f.owner, f.member, f.stranger)
	scan(&f.ws, `INSERT INTO workspaces (name, type, polaris_catalog_name, owner_team_id)
		VALUES ('w','shared','p',$1) RETURNING id`, team)
	addDefaultSpace(t, pool, f.ws)

	var conn openapi_types.UUID
	scan(&conn, `INSERT INTO catalog_connections (name, type, url) VALUES ('tri','trino','http://t') RETURNING id`)
	scan(&f.engine, `INSERT INTO engines (name, kind, class, connection_id) VALUES ('trino','trino','federated',$1) RETURNING id`, conn)
	scan(&f.catalog, `INSERT INTO catalogs (connection_id, name, origin) VALUES ($1,'lake','native') RETURNING id`, conn)
	scan(&f.binding, `INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, mode) VALUES ($1,$2,'lake_t','adopted') RETURNING id`, f.catalog, f.engine)

	f.h = testAPI(handlerFields{queries: repo.New(testdb.GORM(t, pool))})
	return f
}

func pgUUID(u openapi_types.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: u != openapi_types.UUID{}}
}

// addExecution inserts one execution with the fixture's target.
func (f *historyFixture) addExecution(t *testing.T, user openapi_types.UUID, status, sql string, started time.Time) openapi_types.UUID {
	t.Helper()
	var id pgtype.UUID
	err := f.pool.QueryRow(context.Background(), `
		INSERT INTO query_executions
		  (workspace_id, user_id, status, sql, started_at, finished_at, rows_returned, duration_ms,
		   engine_id, target_catalog_id, target_binding_id, target_snapshot)
		VALUES ($1,$2,$3,$4,$5,$5,10,100,$6,$7,$8,'{}'::jsonb)
		RETURNING id`,
		f.ws, user, status, sql, started,
		pgUUID(f.engine), pgUUID(f.catalog), pgUUID(f.binding),
	).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return openapi_types.UUID(id.Bytes)
}

func (f *historyFixture) list(t *testing.T, user string, role string, params ListExecutionsParams) ListExecutions200JSONResponse {
	t.Helper()
	res, err := f.h.ListExecutions(ctxAs(user, role), ListExecutionsRequestObject{Id: f.ws, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	page, ok := res.(ListExecutions200JSONResponse)
	if !ok {
		t.Fatalf("response = %T", res)
	}
	return page
}

func executionIDs(page ListExecutions200JSONResponse) []string {
	out := make([]string, 0, len(page.Data))
	for _, execution := range page.Data {
		out = append(out, execution.Id.String())
	}
	return out
}

func stringPtr(value string) *string { return &value }

func statusPtr(value ListExecutionsParamsStatus) *ListExecutionsParamsStatus { return &value }

func TestHistory_Filters(t *testing.T) {
	t.Parallel()
	f := newHistoryFixture(t)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	first := f.addExecution(t, f.member, "finished", "SELECT a FROM orders", base)
	second := f.addExecution(t, f.member, "failed", "SELECT b FROM customers", base.Add(time.Hour))
	third := f.addExecution(t, f.owner, "finished", "SELECT c FROM revenue", base.Add(2*time.Hour))

	all := f.list(t, f.owner.String(), "member", ListExecutionsParams{})
	if got := executionIDs(all); len(got) != 3 {
		t.Fatalf("no filter: %v", got)
	}

	failed := f.list(t, f.owner.String(), "member", ListExecutionsParams{
		Status: statusPtr(ListExecutionsParamsStatusFailed),
	})
	if got := executionIDs(failed); len(got) != 1 || got[0] != second.String() {
		t.Fatalf("status: %v", got)
	}

	mine := f.list(t, f.owner.String(), "member", ListExecutionsParams{UserId: stringPtr(f.member.String())})
	if got := executionIDs(mine); len(got) != 2 || got[0] != second.String() || got[1] != first.String() {
		t.Fatalf("user: %v", got)
	}

	search := f.list(t, f.owner.String(), "member", ListExecutionsParams{Q: stringPtr("customers")})
	if got := executionIDs(search); len(got) != 1 || got[0] != second.String() {
		t.Fatalf("q: %v", got)
	}

	from := base.Add(30 * time.Minute)
	window := f.list(t, f.owner.String(), "member", ListExecutionsParams{From: &from})
	if got := executionIDs(window); len(got) != 2 || got[0] != third.String() {
		t.Fatalf("from: %v", got)
	}

	to := base.Add(30 * time.Minute)
	window = f.list(t, f.owner.String(), "member", ListExecutionsParams{To: &to})
	if got := executionIDs(window); len(got) != 1 || got[0] != first.String() {
		t.Fatalf("to: %v", got)
	}

	catalogID := f.catalog
	byCatalog := f.list(t, f.owner.String(), "member", ListExecutionsParams{CatalogId: &catalogID})
	if got := executionIDs(byCatalog); len(got) != 3 {
		t.Fatalf("catalog: %v", got)
	}
	engineID := f.engine
	byEngine := f.list(t, f.owner.String(), "member", ListExecutionsParams{EngineId: &engineID})
	if got := executionIDs(byEngine); len(got) != 3 {
		t.Fatalf("engine: %v", got)
	}

	otherCatalog := openapi_types.UUID{0xFF}
	empty := f.list(t, f.owner.String(), "member", ListExecutionsParams{CatalogId: &otherCatalog})
	if got := executionIDs(empty); len(got) != 0 {
		t.Fatalf("unknown catalog: %v", got)
	}
}

func TestHistory_KeysetCursor(t *testing.T) {
	t.Parallel()
	f := newHistoryFixture(t)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	oldest := f.addExecution(t, f.member, "finished", "SELECT 1", base)
	middle := f.addExecution(t, f.member, "finished", "SELECT 2", base.Add(time.Minute))
	newest := f.addExecution(t, f.member, "finished", "SELECT 3", base.Add(2*time.Minute))

	limit := 2
	page := f.list(t, f.member.String(), "member", ListExecutionsParams{Limit: &limit})
	if got := executionIDs(page); len(got) != 2 || got[0] != newest.String() || got[1] != middle.String() {
		t.Fatalf("first page: %v", got)
	}
	if page.NextCursor == nil {
		t.Fatal("expected a next cursor")
	}

	next := f.list(t, f.member.String(), "member", ListExecutionsParams{
		Limit: &limit, Cursor: page.NextCursor,
	})
	if got := executionIDs(next); len(got) != 1 || got[0] != oldest.String() {
		t.Fatalf("second page: %v", got)
	}
	if next.NextCursor != nil {
		t.Fatalf("unexpected next cursor on the last page: %v", *next.NextCursor)
	}
}

func TestHistory_Visibility(t *testing.T) {
	t.Parallel()
	f := newHistoryFixture(t)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	f.addExecution(t, f.member, "finished", "SELECT 1", base)
	f.addExecution(t, f.member, "failed", "SELECT 2", base.Add(time.Minute))
	f.addExecution(t, f.owner, "finished", "SELECT 3", base.Add(2*time.Minute))

	member := executionIDs(f.list(t, f.member.String(), "member", ListExecutionsParams{}))
	if len(member) != 2 {
		t.Fatalf("a member sees only their own: %v", member)
	}

	// A member asking for the owner's rows still gets only their own.
	member = executionIDs(f.list(t, f.member.String(), "member", ListExecutionsParams{
		UserId: stringPtr(f.owner.String()),
	}))
	if len(member) != 2 {
		t.Fatalf("a member cannot widen visibility: %v", member)
	}

	owner := executionIDs(f.list(t, f.owner.String(), "member", ListExecutionsParams{}))
	if len(owner) != 3 {
		t.Fatalf("the owner sees everyone: %v", owner)
	}

	admin := executionIDs(f.list(t, f.stranger.String(), "admin", ListExecutionsParams{}))
	if len(admin) != 3 {
		t.Fatalf("an admin sees everyone: %v", admin)
	}
}

func classPtr(value ListExecutionsParamsClass) *ListExecutionsParamsClass { return &value }

// TestHistory_FiltersByWorkloadClass: the class filter narrows the list and the
// row carries the queue wait (#549).
func TestHistory_FiltersByWorkloadClass(t *testing.T) {
	t.Parallel()
	f := newHistoryFixture(t)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	f.addExecution(t, f.member, "finished", "SELECT 1", base)
	heavy := f.addExecution(t, f.member, "finished", "SELECT 2", base.Add(time.Minute))
	if _, err := f.pool.Exec(context.Background(), `
		UPDATE query_executions
		   SET queue_class = 'heavy', queue_reason = 'rule:x',
		       queued_at = $2, admitted_at = $2, queue_wait_ms = 1500
		 WHERE id = $1`, pgUUID(heavy), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	page := f.list(t, f.owner.String(), "member", ListExecutionsParams{
		Class: classPtr(ListExecutionsParamsClassHeavy),
	})
	if got := executionIDs(page); len(got) != 1 || got[0] != heavy.String() {
		t.Fatalf("class filter = %v, want just the heavy run", got)
	}
	queue := page.Data[0].Queue
	if queue == nil || queue.Class != ExecutionQueueClassHeavy || queue.WaitMs != 1500 {
		t.Fatalf("queue = %+v, want heavy with a 1500 ms wait", queue)
	}
	if queue.Reason == nil || *queue.Reason != "rule:x" {
		t.Fatalf("queue reason = %v, want rule:x", queue.Reason)
	}
}

// TestHistory_RejectsUnknownClass: the class filter only accepts the closed
// enum; a bogus value is a validation error, not an empty page (#589).
func TestHistory_RejectsUnknownClass(t *testing.T) {
	t.Parallel()
	f := newHistoryFixture(t)
	resp, err := f.h.ListExecutions(ctxAs(f.owner.String(), "member"), ListExecutionsRequestObject{
		Id:     f.ws,
		Params: ListExecutionsParams{Class: classPtr(ListExecutionsParamsClass("bogus"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ListExecutions422JSONResponse); !ok {
		t.Fatalf("response = %T, want 422", resp)
	}
}
