package export

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubServer is a `_bulk` endpoint that records the documents it receives and
// can fail specific calls or items.
type stubServer struct {
	mu       sync.Mutex
	docs     []map[string]any
	ids      []string
	calls    int
	failCall int // 1-based; 0 = never
	failItem int // 1-based item index in that call; 0 = whole call
	srv      *httptest.Server
}

func newStub(t *testing.T) *stubServer {
	s := &stubServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.calls++
		raw, _ := io.ReadAll(r.Body)
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		n := len(lines) / 2
		if s.failCall == s.calls && s.failItem == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		items := make([]map[string]any, 0, n)
		errors := false
		for i := 0; i < n; i++ {
			var meta bulkItem
			_ = json.Unmarshal([]byte(lines[2*i]), &meta)
			s.ids = append(s.ids, meta.Index.ID)
			var doc map[string]any
			_ = json.Unmarshal([]byte(lines[2*i+1]), &doc)
			if s.failCall == s.calls && s.failItem == i+1 {
				errors = true
				items = append(items, map[string]any{"index": map[string]any{"status": 400, "error": map[string]string{"type": "mapper", "reason": "bad"}}})
				continue
			}
			s.docs = append(s.docs, doc)
			items = append(items, map[string]any{"index": map[string]any{"status": 201}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": errors, "items": items})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stubServer) snapshot() ([]map[string]any, []string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any{}, s.docs...), append([]string{}, s.ids...), s.calls
}

// fakeSource holds events and answers the safety-lag query.
type fakeSource struct{ events []Event }

func (f fakeSource) Events(_ context.Context, after Position, before time.Time, limit int) ([]Event, error) {
	var out []Event
	for _, e := range f.events {
		if !e.OccurredAt.Before(before) {
			continue // inside the safety lag
		}
		if !strictlyAfter(e, after) {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

func strictlyAfter(e Event, p Position) bool {
	if e.OccurredAt.After(p.OccurredAt) {
		return true
	}
	if e.OccurredAt.Equal(p.OccurredAt) {
		return e.ID > p.ID
	}
	return false
}

// fakeCursor is an in-memory cursor.
type fakeCursor struct {
	mu sync.Mutex
	p  Position
}

func (c *fakeCursor) Get(context.Context, string) (Position, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.p, nil
}

func (c *fakeCursor) Set(_ context.Context, _ string, p Position) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.p = p
	return nil
}

func events(n int, at time.Time) []Event {
	out := make([]Event, n)
	for i := range out {
		out[i] = Event{OccurredAt: at.Add(time.Duration(i) * time.Second), ID: int64(i + 1), ActorKind: "user", ActorID: "u", Action: "catalog.create", ResourceType: "catalog", Outcome: "allowed"}
	}
	return out
}

func newExporter(t *testing.T, src Source, cur Cursor, sink BulkSink, batch int, now time.Time) *Exporter {
	return &Exporter{Source: src, Cursor: cur, Sink: sink, Index: "baleia-audit", Batch: batch, Lag: DefaultSafetyLag, Now: func() time.Time { return now }}
}

// TestOrderedAndResumable: a transport failure after batch 2 leaves the cursor
// there; a fresh pass resumes and the union has every event, in order, no gaps.
func TestOrderedAndResumable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	src := fakeSource{events: events(4, at)}
	cur := &fakeCursor{}
	stub := newStub(t)
	stub.failCall = 3 // third batch of one event each

	sink, err := NewHTTPBulk(Config{URL: stub.srv.URL}, stub.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	e := newExporter(t, src, cur, sink, 1, now) // one event per batch
	if _, err := e.Run(context.Background()); err == nil {
		t.Fatal("expected a bulk error")
	}
	// Batch 3 failed: the cursor is at batch 2.
	pos, _ := cur.Get(context.Background(), Sink)
	if pos.ID != 2 {
		t.Fatalf("cursor = %+v, want id 2", pos)
	}
	// Restart with a healthy sink.
	stub.mu.Lock()
	stub.failCall = 0
	stub.mu.Unlock()
	e2 := newExporter(t, src, cur, sink, 1, now)
	if _, err := e2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	docs, _, _ := stub.snapshot()
	if len(docs) != len(src.events) {
		t.Fatalf("documents = %d, want %d", len(docs), len(src.events))
	}
	for i, d := range docs {
		want := Document(src.events[i])["@timestamp"]
		if d["@timestamp"] != want {
			t.Errorf("doc %d out of order: %v, want %v", i, d["@timestamp"], want)
		}
	}
}

// TestIdempotentIDs: the same event maps to the same `_id`, so re-sending a
// batch overwrites instead of duplicating.
func TestIdempotentIDs(t *testing.T) {
	t.Parallel()
	e := Event{OccurredAt: time.Unix(1_700_000_000, 123), ID: 7}
	a, b := DocumentID(e), DocumentID(e)
	if a != b {
		t.Fatalf("ids differ: %s vs %s", a, b)
	}
	// The id is the occurred_at in unix nanos and the row id.
	if !strings.Contains(a, "1700000000000000123") || !strings.HasSuffix(a, "-7") {
		t.Errorf("unexpected id %q", a)
	}
}

// TestPartialErrors: item 3 of 5 fails; the cursor advances only to item 2.
func TestPartialErrors(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	src := fakeSource{events: events(5, now.Add(-time.Hour))}
	cur := &fakeCursor{}
	stub := newStub(t)
	stub.failCall = 1
	stub.failItem = 3

	sink, _ := NewHTTPBulk(Config{URL: stub.srv.URL}, stub.srv.Client())
	e := newExporter(t, src, cur, sink, 5, now)
	n, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("accepted %d, want 2", n)
	}
	pos, _ := cur.Get(context.Background(), Sink)
	if pos.ID != 2 {
		t.Fatalf("cursor = %+v, want id 2", pos)
	}
}

// TestSafetyLag: events newer than the lag are not exported.
func TestSafetyLag(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	src := fakeSource{events: []Event{
		{OccurredAt: now.Add(-time.Second), ID: 1, Action: "a.b", Outcome: "allowed", ResourceType: "x"},
		{OccurredAt: now.Add(-time.Minute), ID: 2, Action: "a.b", Outcome: "allowed", ResourceType: "x"},
	}}
	cur := &fakeCursor{}
	stub := newStub(t)
	sink, _ := NewHTTPBulk(Config{URL: stub.srv.URL}, stub.srv.Client())
	e := newExporter(t, src, cur, sink, 10, now)
	n, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("accepted %d, want 1 (the recent event is inside the lag)", n)
	}
}

// TestNoSecretsInDocs: the mapping copies redacted changes only.
func TestNoSecretsInDocs(t *testing.T) {
	t.Parallel()
	e := Event{OccurredAt: time.Now(), ID: 1, Action: "catalog.create", Outcome: "allowed", ResourceType: "catalog",
		Changes: json.RawMessage(`{"password":{"from":"***","to":"***"}}`)}
	doc := Document(e)
	changes, ok := doc["changes"].(map[string]any)
	if !ok {
		t.Fatalf("changes missing: %v", doc)
	}
	pw, _ := changes["password"].(map[string]any)
	if pw["from"] != "***" || pw["to"] != "***" {
		t.Errorf("changes not copied redacted: %v", changes)
	}
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), "secret") {
		t.Errorf("document leaks a secret: %s", raw)
	}
}
