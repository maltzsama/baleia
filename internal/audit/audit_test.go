package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/stdlib"
)

type recordedInserter struct {
	rows  []db.InsertAuditEventParams
	err   error
	block bool
}

func (r *recordedInserter) InsertAuditEvent(ctx context.Context, p db.InsertAuditEventParams) error {
	if r.block {
		<-ctx.Done()
		return ctx.Err()
	}
	r.rows = append(r.rows, p)
	return r.err
}

func userCtx() context.Context {
	ctx := context.WithValue(context.Background(), auth.ContextKeyUserID, "11111111-1111-1111-1111-111111111111")
	ctx = context.WithValue(ctx, middleware.RequestIDKey, "req-42")
	return WithRequest(ctx, RequestInfo{ClientIP: netip.MustParseAddr("10.1.2.3"), UserAgent: "curl/8"})
}

// TestRecorderFillsContext: actor, auth method, request id and client come
// from the context.
func TestRecorderFillsContext(t *testing.T) {
	q := &recordedInserter{}
	r := NewPostgres(q, nil, nil)
	err := r.Record(userCtx(), Event{Action: CatalogCreate, ResourceType: "catalog", ResourceID: "c1", WorkspaceID: "22222222-2222-2222-2222-222222222222", Outcome: Allowed})
	if err != nil {
		t.Fatal(err)
	}
	row := q.rows[0]
	if row.ActorKind != "user" || *row.ActorID != "11111111-1111-1111-1111-111111111111" || *row.AuthMethod != AuthAccessToken ||
		*row.RequestID != "req-42" || row.ClientIp.String() != "10.1.2.3" || *row.UserAgent != "curl/8" ||
		row.Action != "catalog.create" || row.Outcome != "allowed" || !row.WorkspaceID.Valid || row.Changes != nil {
		t.Fatalf("row = %+v", row)
	}

	login := WithAuthMethod(userCtx(), AuthPassword)
	_ = r.Record(login, Event{Action: AuthLogin, ResourceType: "user", Outcome: Allowed})
	if *q.rows[1].AuthMethod != AuthPassword {
		t.Fatalf("auth method = %v", *q.rows[1].AuthMethod)
	}
	_ = r.Record(WithService(context.Background(), "worker"), Event{Action: MetadataSync, ResourceType: "catalog", Outcome: Allowed})
	if q.rows[2].ActorKind != "service" || *q.rows[2].ActorID != "worker" || q.rows[2].AuthMethod != nil {
		t.Fatalf("service row = %+v", q.rows[2])
	}
	_ = r.Record(context.Background(), Event{Action: AuthLogin, ResourceType: "user", Outcome: Denied, Reason: ReasonForbidden})
	if q.rows[3].ActorKind != "anonymous" || q.rows[3].ActorID != nil {
		t.Fatalf("anonymous row = %+v", q.rows[3])
	}
}

// TestRedactsSecrets: declared and conventionally named secret fields are
// stored as "***"; other changes as they are.
func TestRedactsSecrets(t *testing.T) {
	q := &recordedInserter{}
	r := NewPostgres(q, []string{"sas"}, nil)
	err := r.Record(userCtx(), Event{Action: ConnectionUpdate, ResourceType: "connection", Outcome: Allowed, Changes: map[string]Change{
		"client_secret": {From: "old", To: "new"},
		"sas":           {From: nil, To: "sig"},
		"url":           {From: "http://a", To: "http://b"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]Change
	if err := json.Unmarshal(q.rows[0].Changes, &got); err != nil {
		t.Fatal(err)
	}
	if got["client_secret"].From != "***" || got["client_secret"].To != "***" || got["sas"].From != nil || got["sas"].To != "***" || got["url"].To != "http://b" {
		t.Fatalf("changes = %+v", got)
	}
	if strings.Contains(string(q.rows[0].Changes), "old") || strings.Contains(string(q.rows[0].Changes), "sig") {
		t.Fatalf("a secret value was stored: %s", q.rows[0].Changes)
	}
}

// TestRecordFailureDoesNotFailRequest: a stuck or failing database makes
// Record return an error within the write timeout and count the failure;
// the caller goes on.
func TestRecordFailureDoesNotFailRequest(t *testing.T) {
	failures := 0
	r := NewPostgres(&recordedInserter{block: true}, nil, func() { failures++ })
	start := time.Now()
	err := r.Record(userCtx(), Event{Action: CatalogDelete, ResourceType: "catalog", Outcome: Allowed})
	if err == nil || time.Since(start) > time.Second || failures != 1 {
		t.Fatalf("err %v after %s, failures %d", err, time.Since(start), failures)
	}
	r = NewPostgres(&recordedInserter{err: errors.New("down")}, nil, func() { failures++ })
	if err := r.Record(userCtx(), Event{Action: CatalogDelete, ResourceType: "catalog", Outcome: Allowed}); err == nil || failures != 2 {
		t.Fatalf("err %v failures %d", err, failures)
	}
	// A cancelled request still gets its event written.
	q := &recordedInserter{}
	ctx, cancel := context.WithCancel(userCtx())
	cancel()
	if err := NewPostgres(q, nil, nil).Record(ctx, Event{Action: ExecutionCancel, ResourceType: "execution", Outcome: Allowed}); err != nil || len(q.rows) != 1 {
		t.Fatalf("cancelled request: %v, %d rows", err, len(q.rows))
	}
}

func TestUnknownActionIsAnError(t *testing.T) {
	failures := 0
	r := NewPostgres(&recordedInserter{}, nil, func() { failures++ })
	if err := r.Record(userCtx(), Event{Action: "catalog.explode", ResourceType: "catalog", Outcome: Allowed}); err == nil || failures != 1 {
		t.Fatalf("err %v failures %d", err, failures)
	}
}

// TestActionsAreBounded: the vocabulary is unique and every action matches
// the table's CHECK.
func TestActionsAreBounded(t *testing.T) {
	re := regexp.MustCompile(`^[a-z_]+\.[a-z_]+$`)
	seen := map[Action]bool{}
	for _, a := range Actions() {
		if !re.MatchString(string(a)) || seen[a] {
			t.Errorf("action %q is malformed or duplicated", a)
		}
		seen[a] = true
	}
}

func TestMiddlewareRecordsClient(t *testing.T) {
	var got RequestInfo
	h := Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = requestOf(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::ffff:192.0.2.7]:5555"
	req.Header.Set("User-Agent", "baleia-ui")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.ClientIP.String() != "192.0.2.7" || got.UserAgent != "baleia-ui" {
		t.Fatalf("request info = %+v", got)
	}
}

// TestAppendOnly: the application role cannot update or delete audit rows.
func TestAppendOnly(t *testing.T) {
	pool := testdb.New(t)
	q := db.New(pool)
	if err := NewPostgres(q, nil, nil).Record(userCtx(), Event{Action: CatalogCreate, ResourceType: "catalog", Outcome: Allowed}); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`UPDATE audit_events SET outcome = 'denied'`, `DELETE FROM audit_events`} {
		_, err := pool.Exec(context.Background(), stmt)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: %v, want the trigger's exception", stmt, err)
		}
	}
}

// TestKeysetPagination: pages over equal timestamps are stable, ordered and
// without duplicates.
func TestKeysetPagination(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	for i := range 5 {
		if _, err := pool.Exec(ctx, `INSERT INTO audit_events (occurred_at, actor_kind, action, resource_type, resource_id, outcome)
			VALUES ($1, 'service', 'metadata.sync', 'catalog', $2, 'allowed')`, at, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	q := db.New(pool)
	var ids []string
	p := db.ListAuditEventsParams{Lim: 2}
	for page := 0; page < 5; page++ {
		rows, err := q.ListAuditEvents(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			ids = append(ids, *r.ResourceID)
		}
		if len(rows) < 2 {
			break
		}
		last := rows[len(rows)-1]
		p.CursorAt, p.CursorID = last.OccurredAt, &last.ID
	}
	if strings.Join(ids, ",") != "4,3,2,1,0" {
		t.Fatalf("pages = %v", ids)
	}
}

// TestEnsurePartitions: partitions are created ahead, idempotently; an
// event outside them fails instead of landing in a default partition.
func TestEnsurePartitions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	for range 2 {
		if err := q.EnsureAuditPartitions(ctx, 3); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_inherits WHERE inhparent = 'audit_events'::regclass`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("partitions = %d, %v", n, err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO audit_events (occurred_at, actor_kind, action, resource_type, outcome)
		VALUES (now() + interval '2 years', 'service', 'metadata.sync', 'catalog', 'allowed')`)
	if err == nil {
		t.Fatal("an event without a partition must fail")
	}
}

// TestPrune drops only the partitions before the cut, never the current
// month.
func TestPrune(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -14, 0)
	for i := range 2 {
		m := old.AddDate(0, i, 0)
		if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE audit_events_%s PARTITION OF audit_events FOR VALUES FROM ('%s') TO ('%s')`,
			m.Format("200601"), m.Format("2006-01-02"), m.AddDate(0, 1, 0).Format("2006-01-02"))); err != nil {
			t.Fatal(err)
		}
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	if _, err := Prune(ctx, sqlDB, now.Format("2006-01"), now); err == nil {
		t.Fatal("pruning the current month must be refused")
	}
	dropped, err := Prune(ctx, sqlDB, old.AddDate(0, 1, 0).Format("2006-01"), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0] != "audit_events_"+old.Format("200601") {
		t.Fatalf("dropped = %v", dropped)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_inherits WHERE inhparent = 'audit_events'::regclass`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("partitions left = %d, %v (one old + this month + 2 ahead)", n, err)
	}
}
