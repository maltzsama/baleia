package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// queuesAPI builds handlers over a real pool and transaction, so the switch,
// the capacities and the published resource groups commit together, with the
// resource groups written under environment.
func queuesAPI(pool *pgxpool.Pool, environment string) *API {
	settings := Settings{Execution: ExecutionSettings{TrinoResourceGroupsEnvironment: environment}}
	return testAPI(handlerFields{queries: db.New(pool), pool: pool, tx: poolTx{pool: pool}, settings: settings})
}

// queuesEngine inserts an engine of kind over its own connection.
func queuesEngine(t *testing.T, pool *pgxpool.Pool, name, kind string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var conn pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO catalog_connections (name, type, url) VALUES ($1, $2, 'http://engine') RETURNING id`, name+"-conn", kind).Scan(&conn); err != nil {
		t.Fatal(err)
	}
	var engine pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO engines (name, kind, class, connection_id) VALUES ($1, $2, 'federated', $3) RETURNING id`, name, kind, conn).Scan(&engine); err != nil {
		t.Fatal(err)
	}
	return engine
}

// queuesBody sets every class; background is closed when closeBackground.
func queuesBody(handover *bool, closeBackground bool) *PutEngineQueuesJSONRequestBody {
	background := 2
	if closeBackground {
		background = 0
	}
	return &PutEngineQueuesJSONRequestBody{
		TrinoResourceGroupsEnabled: handover,
		Classes: []UpdateEngineQueue{
			{Class: UpdateEngineQueueClass("interactive"), MaxRunning: 4, MaxQueued: 20, QueueTimeoutMs: 60000},
			{Class: UpdateEngineQueueClass("heavy"), MaxRunning: 1, MaxQueued: 10, QueueTimeoutMs: 60000},
			{Class: UpdateEngineQueueClass("background"), MaxRunning: background, MaxQueued: 5, QueueTimeoutMs: 60000},
		},
	}
}

// queuesAdminCtx is an administrator principal; it needs no user row.
func queuesAdminCtx() context.Context {
	ctx := context.WithValue(context.Background(), auth.ContextKeyRole, authzRoleAdmin)
	return context.WithValue(ctx, auth.ContextKeyUserID, "00000000-0000-0000-0000-0000000000a1")
}

func putQueues(t *testing.T, h *API, engine pgtype.UUID, body *PutEngineQueuesJSONRequestBody) PutEngineQueuesResponseObject {
	t.Helper()
	resp, err := h.PutEngineQueues(queuesAdminCtx(), PutEngineQueuesRequestObject{Id: engine.Bytes, Body: body})
	if err != nil {
		t.Fatalf("PutEngineQueues = %v", err)
	}
	return resp
}

func is200(resp PutEngineQueuesResponseObject) bool {
	_, ok := resp.(PutEngineQueues200JSONResponse)
	return ok
}

// refusalCode is the error code of a 422, or "" for any other response.
func refusalCode(resp PutEngineQueuesResponseObject) ErrorCode {
	if r, ok := resp.(PutEngineQueues422JSONResponse); ok {
		return r.Code
	}
	return ""
}

// resourceGroupNames lists the group names published under environment.
func resourceGroupNames(t *testing.T, pool *pgxpool.Pool, environment string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT name FROM resource_groups WHERE environment = $1 ORDER BY name`, environment)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func resourceGroupRevisions(t *testing.T, pool *pgxpool.Pool, engine pgtype.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM trino_resource_group_revisions WHERE engine_id = $1`, engine).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func engineHandover(t *testing.T, pool *pgxpool.Pool, engine pgtype.UUID) bool {
	t.Helper()
	eng, err := db.New(pool).GetEngineByID(context.Background(), engine)
	if err != nil {
		t.Fatal(err)
	}
	return eng.TrinoResourceGroupsEnabled
}

// TestPutEngineQueues_PublishesResourceGroups: turning the handover on
// publishes the groups generated from the capacities, and a later capacity
// change publishes a new revision (#635).
func TestPutEngineQueues_PublishesResourceGroups(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	h := queuesAPI(pool, "staging")
	engine := queuesEngine(t, pool, "trino", "trino")

	if resp := putQueues(t, h, engine, queuesBody(ptr(true), false)); !is200(resp) {
		t.Fatalf("enable = %#v, want 200", resp)
	}
	if got := resourceGroupNames(t, pool, "staging"); len(got) != 4 {
		t.Fatalf("groups after enabling = %v, want root + 3 classes", got)
	}
	if got := resourceGroupNames(t, pool, "production"); len(got) != 0 {
		t.Fatalf("groups outside the configured environment = %v", got)
	}

	// The switch is omitted: it stays on, and the closed class drops its group.
	if resp := putQueues(t, h, engine, queuesBody(nil, true)); !is200(resp) {
		t.Fatalf("capacity change = %#v, want 200", resp)
	}
	if got := resourceGroupNames(t, pool, "staging"); len(got) != 3 {
		t.Fatalf("groups after closing background = %v, want root + 2 classes", got)
	}
	if got := resourceGroupRevisions(t, pool, engine); got != 2 {
		t.Fatalf("revisions = %d, want 2", got)
	}
}

// TestPutEngineQueues_OffPublishesNothing: without the handover Baleia's own
// queue is the authority and Trino's tables are left alone.
func TestPutEngineQueues_OffPublishesNothing(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	h := queuesAPI(pool, "")
	engine := queuesEngine(t, pool, "trino", "trino")

	if resp := putQueues(t, h, engine, queuesBody(ptr(false), false)); !is200(resp) {
		t.Fatalf("save = %#v, want 200", resp)
	}
	if got := resourceGroupNames(t, pool, defaultResourceGroupsEnvironment); len(got) != 0 {
		t.Fatalf("groups with the handover off = %v", got)
	}
	if got := resourceGroupRevisions(t, pool, engine); got != 0 {
		t.Fatalf("revisions = %d, want 0", got)
	}
}

// TestPutEngineQueues_ResourceGroupsRefusals: only a Trino engine can hand
// over, and only one engine at a time; a refusal changes nothing.
func TestPutEngineQueues_ResourceGroupsRefusals(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	h := queuesAPI(pool, "")

	direct := queuesEngine(t, pool, "warehouse", "postgresql")
	if resp := putQueues(t, h, direct, queuesBody(ptr(true), false)); refusalCode(resp) != ErrorCodeEngineUnsupported {
		t.Fatalf("non-Trino handover = %#v, want 422 engine_unsupported", resp)
	}
	if engineHandover(t, pool, direct) {
		t.Fatal("a refused handover flipped the switch")
	}

	first := queuesEngine(t, pool, "trino-a", "trino")
	second := queuesEngine(t, pool, "trino-b", "trino")
	if resp := putQueues(t, h, first, queuesBody(ptr(true), false)); !is200(resp) {
		t.Fatalf("first engine's handover = %#v, want 200", resp)
	}
	if resp := putQueues(t, h, second, queuesBody(ptr(true), false)); refusalCode(resp) != ErrorCodeConflict {
		t.Fatalf("second engine's handover = %#v, want 422 conflict", resp)
	}
	if engineHandover(t, pool, second) || resourceGroupRevisions(t, pool, second) != 0 {
		t.Fatal("a refused handover changed the second engine")
	}

	// Once the first engine lets go, the second one can take over.
	if resp := putQueues(t, h, first, queuesBody(ptr(false), false)); !is200(resp) {
		t.Fatalf("turning the first engine's handover off = %#v, want 200", resp)
	}
	if resp := putQueues(t, h, second, queuesBody(ptr(true), false)); !is200(resp) {
		t.Fatalf("second engine's handover after the first let go = %#v, want 200", resp)
	}
}
