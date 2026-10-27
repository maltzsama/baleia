package queryevents

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// recorder keeps the snapshots it is asked to write.
type recorder struct {
	rows []db.UpsertTrinoQuerySnapshotParams
}

func (r *recorder) UpsertTrinoQuerySnapshot(_ context.Context, arg db.UpsertTrinoQuerySnapshotParams) (db.TrinoQuerySnapshot, error) {
	r.rows = append(r.rows, arg)
	return db.TrinoQuerySnapshot{}, nil
}

var engineID = pgtype.UUID{Bytes: [16]byte{0xE1}, Valid: true}

func ingester(r *recorder, token string) *Ingester {
	return New(nil, r, func(context.Context) (pgtype.UUID, error) { return engineID, nil }, token)
}

// fixture is an event captured from Trino 483's HTTP event listener, with
// its plans and operator statistics trimmed.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestIngest_CompletedEvents: a completed event becomes the poller's row,
// with the event's figures; the stored payload is never the event (#406).
func TestIngest_CompletedEvents(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	i := ingester(r, "")
	for _, name := range []string{"completed_finished.json", "completed_failed.json"} {
		if err := i.Ingest(context.Background(), bytes.NewReader(fixture(t, name))); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(r.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(r.rows))
	}

	ok := r.rows[0]
	if ok.TrinoQueryID != "20261027_170648_00000_xnycr" || ok.State != "finished" || ok.User != "trino" || ok.EngineID != engineID {
		t.Errorf("finished row = %+v", ok)
	}
	if ok.ElapsedMs == nil || *ok.ElapsedMs != 2273 || ok.CpuMs == nil || *ok.CpuMs != 1498 {
		t.Errorf("finished times = %v elapsed, %v cpu; want 2273 and 1498 ms", ok.ElapsedMs, ok.CpuMs)
	}
	if ok.OutputRows == nil || *ok.OutputRows != 1 || ok.ErrorMessage != nil {
		t.Errorf("finished output = %v rows, error %v", ok.OutputRows, ok.ErrorMessage)
	}
	if strings.Contains(string(ok.RawTrinoPayload), "plan") || strings.Contains(string(ok.RawTrinoPayload), "SELECT") {
		t.Errorf("stored payload carries the event: %s", ok.RawTrinoPayload)
	}

	failed := r.rows[1]
	if failed.State != "failed" || failed.ErrorMessage == nil || !strings.Contains(*failed.ErrorMessage, "no_such_column") {
		t.Errorf("failed row = state %q, error %v", failed.State, failed.ErrorMessage)
	}
	if failed.Catalog == nil || *failed.Catalog != "tpch" {
		t.Errorf("failed row catalog = %v, want tpch", failed.Catalog)
	}
}

// TestIngest_Dropped: a created event and a statement that is not user
// activity are accepted and not recorded; a body that is not an event is
// ErrMalformed.
func TestIngest_Dropped(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	i := ingester(r, "")
	if err := i.Ingest(context.Background(), bytes.NewReader(fixture(t, "created.json"))); err != nil {
		t.Errorf("created event = %v, want it accepted", err)
	}
	ddl := strings.Replace(string(fixture(t, "completed_finished.json")),
		`"query": "SELECT count(*) FROM tpch.tiny.orders"`,
		`"query": "CREATE CATALOG lake USING iceberg WITH (\"s3.aws-secret-key\" = 'x')"`, 1)
	if !strings.Contains(ddl, "CREATE CATALOG") {
		t.Fatal("the fixture's query did not change")
	}
	if err := i.Ingest(context.Background(), strings.NewReader(ddl)); err != nil {
		t.Errorf("catalog DDL = %v, want it accepted", err)
	}
	if len(r.rows) != 0 {
		t.Errorf("recorded %d rows, want none", len(r.rows))
	}
	if err := i.Ingest(context.Background(), strings.NewReader(`{"hello": 1}`)); !errors.Is(err, ErrMalformed) {
		t.Errorf("not an event = %v, want ErrMalformed", err)
	}
}

// TestHandler: only the listener's token is accepted, and the feature is off
// without a token.
func TestHandler(t *testing.T) {
	t.Parallel()
	post := func(h http.Handler, auth string, body []byte) int {
		req := httptest.NewRequest(http.MethodPost, "/internal/trino/query-events", bytes.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	r := &recorder{}
	h := ingester(r, "s3cr3t-token")
	event := fixture(t, "completed_finished.json")
	for name, tc := range map[string]struct {
		auth string
		body []byte
		want int
	}{
		"no token":    {"", event, http.StatusUnauthorized},
		"wrong token": {"Bearer nope", event, http.StatusUnauthorized},
		"not bearer":  {"s3cr3t-token", event, http.StatusUnauthorized},
		"not event":   {"Bearer s3cr3t-token", []byte("[]"), http.StatusBadRequest},
		"event":       {"Bearer s3cr3t-token", event, http.StatusNoContent},
	} {
		if got := post(h, tc.auth, tc.body); got != tc.want {
			t.Errorf("%s = %d, want %d", name, got, tc.want)
		}
	}
	if len(r.rows) != 1 {
		t.Errorf("recorded %d rows, want only the authorized event", len(r.rows))
	}
	if got := post(ingester(&recorder{}, ""), "Bearer ", event); got != http.StatusNotFound {
		t.Errorf("without a token = %d, want 404 (feature off)", got)
	}
}
