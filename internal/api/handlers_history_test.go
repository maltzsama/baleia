package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"gorm.io/gorm"
)

// historyFixture is a workspace owned by `owner` with `member` and `stranger`
// as non-owners, plus the catalog/engine/binding the executions ran on.
type historyFixture struct {
	pool     *gorm.DB
	gdb      *gorm.DB
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
	gdb := pool
	q := repo.New(gdb)
	f := &historyFixture{pool: pool, gdb: gdb}
	create := func(row any) {
		t.Helper()
		if err := gdb.Create(row).Error; err != nil {
			t.Fatalf("create %T: %v", row, err)
		}
	}
	openapiID := func(id model.UUID) openapi_types.UUID {
		return openapi_types.UUID(repo.PGUUID(string(id)).Bytes)
	}
	user := func(name, email string) openapi_types.UUID {
		password := "x"
		row := model.User{
			ID: model.UUID(repo.NewUUID()), Name: name, Email: model.Text(email), PasswordHash: &password,
		}
		create(&row)
		return openapiID(row.ID)
	}
	f.owner, f.member, f.stranger = user("owner", "owner@x"), user("member", "member@x"), user("stranger", "stranger@x")
	// A shared workspace: the owner owns the team, the member and the admin are
	// plain team members (the admin gets visibility from their global role).
	owner := model.UUID(f.owner.String())
	team := model.Team{ID: model.UUID(repo.NewUUID()), Name: "team", CreatedBy: owner}
	create(&team)
	for _, m := range []struct {
		user model.UUID
		role model.Text
	}{
		{owner, "owner"}, {model.UUID(f.member.String()), "member"}, {model.UUID(f.stranger.String()), "member"},
	} {
		create(&model.TeamMember{
			ID: model.UUID(repo.NewUUID()), TeamID: team.ID, UserID: m.user, Role: m.role,
		})
	}
	ws := model.Workspace{
		ID: model.UUID(repo.NewUUID()), Name: "w", Type: "shared",
		PolarisCatalogName: "p", OwnerTeamID: &team.ID,
	}
	create(&ws)
	f.ws = openapiID(ws.ID)
	addDefaultSpace(t, gdb, ws.ID)

	conn, err := q.CreateCatalogConnection(context.Background(), db.CreateCatalogConnectionParams{
		ID: repo.PGUUID(repo.NewUUID()), Name: "tri", Type: "trino", Url: "http://t",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := q.CreateEngine(context.Background(), db.CreateEngineParams{
		Name: "trino", Kind: "trino", Class: "federated", ConnectionID: conn.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.engine = openapiID(model.UUID(engine.ID.String()))
	catalog := model.Catalog{
		ID: model.UUID(repo.NewUUID()), ConnectionID: model.UUID(conn.ID.String()),
		Name: "lake", Origin: "native",
	}
	create(&catalog)
	f.catalog = openapiID(catalog.ID)
	binding := model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: catalog.ID,
		EngineID: model.UUID(engine.ID.String()), EngineCatalogName: "lake_t", Mode: "adopted",
	}
	create(&binding)
	f.binding = openapiID(binding.ID)

	f.h = testAPI(handlerFields{queries: q})
	return f
}

func pgUUID(u openapi_types.UUID) db.UUID {
	return db.UUID{Bytes: u, Valid: u != openapi_types.UUID{}}
}

// addExecution inserts one execution with the fixture's target.
func (f *historyFixture) addExecution(t *testing.T, user openapi_types.UUID, status, sql string, started time.Time) openapi_types.UUID {
	t.Helper()
	rows, duration := int64(10), int64(100)
	engine := model.UUID(f.engine.String())
	catalog := model.UUID(f.catalog.String())
	binding := model.UUID(f.binding.String())
	snapshot := model.JSON(`{}`)
	row := model.QueryExecution{
		ID: model.UUID(repo.NewUUID()), WorkspaceID: model.UUID(f.ws.String()),
		UserID: model.UUID(user.String()), Status: model.Text(status), SQL: model.Text(sql),
		StartedAt: started, FinishedAt: &started, RowsReturned: &rows, DurationMs: &duration,
		EngineID: &engine, TargetCatalogID: &catalog, TargetBindingID: &binding, TargetSnapshot: &snapshot,
	}
	if err := f.gdb.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return openapi_types.UUID(repo.PGUUID(string(row.ID)).Bytes)
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
	at := base.Add(time.Minute)
	if err := f.gdb.Model(&model.QueryExecution{}).Where("id = ?", heavy.String()).
		Updates(map[string]any{
			"queue_class": "heavy", "queue_reason": "rule:x",
			"queued_at": at, "admitted_at": at, "queue_wait_ms": 1500,
		}).Error; err != nil {
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
