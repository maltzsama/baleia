package trino

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// listPayload is a trimmed /v1/query response captured from Trino 482.
const listPayload = `[
 {"queryId":"20261013_174812_00001_6bjsn","session":{"queryId":"20261013_174812_00001_6bjsn","user":"u_alice","catalog":"tpch_test","schema":null,"timeZone":"UTC"},
  "resourceGroupId":["global"],"state":"FINISHED","query":"SELECT 1","queryType":"SELECT",
  "queryStats":{"createTime":"2026-10-13T17:48:12.601894504Z","endTime":"2026-10-13T17:48:13.038751501Z","elapsedTime":"436.86ms","totalCpuTime":"18.00ms","physicalInputDataSize":"469B","fullyBlocked":true},
  "errorCode":null,"errorType":null},
 {"queryId":"q2","session":{"user":"u_bob","catalog":null,"schema":null},"state":"FAILED","query":"SELECT bad","queryType":"SELECT",
  "queryStats":{"createTime":"2026-10-13T17:49:00Z","elapsedTime":"1.50s","totalCpuTime":"2.00m","physicalInputDataSize":"1.50kB"},
  "errorCode":{"code":58,"name":"TYPE_MISMATCH","type":"USER_ERROR","fatal":true},"errorType":"USER_ERROR"},
 {"queryId":"q3","session":{"user":"u_bob"},"state":"RUNNING","query":"SELECT slow","queryStats":{"fullyBlocked":true}},
 {"queryId":"q4","session":{"user":"u_bob"},"state":"QUEUED","query":"SELECT queued","queryStats":{}}
]`

func TestQueryInfoDecodesTrino482(t *testing.T) {
	var qs []QueryInfo
	if err := json.Unmarshal([]byte(listPayload), &qs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ok := qs[0]
	if ok.User() != "u_alice" || ok.Catalog() != "tpch_test" || ok.Schema() != "" {
		t.Fatalf("session = %q %q %q", ok.User(), ok.Catalog(), ok.Schema())
	}
	if v := ok.ElapsedMs(); v == nil || *v != 437 {
		t.Fatalf("ElapsedMs = %v, want 437", v)
	}
	if v := ok.CPUMs(); v == nil || *v != 18 {
		t.Fatalf("CPUMs = %v, want 18", v)
	}
	if v := ok.PhysicalInputBytes(); v == nil || *v != 469 {
		t.Fatalf("PhysicalInputBytes = %v, want 469", v)
	}
	if at, good := ok.CreatedAt(); !good || at.UTC().Format(time.RFC3339) != "2026-10-13T17:48:12Z" {
		t.Fatalf("CreatedAt = %v, %v", at, good)
	}
	if ok.FailureMessage() != "" {
		t.Fatalf("healthy query has failure message %q", ok.FailureMessage())
	}

	bad := qs[1]
	if bad.FailureMessage() != "TYPE_MISMATCH" {
		t.Fatalf("FailureMessage = %q", bad.FailureMessage())
	}
	if v := bad.ElapsedMs(); v == nil || *v != 1500 {
		t.Fatalf("ElapsedMs = %v, want 1500", v)
	}
	if v := bad.CPUMs(); v == nil || *v != 120000 {
		t.Fatalf("CPUMs = %v, want 120000", v)
	}
	if v := bad.PhysicalInputBytes(); v == nil || *v != 1536 {
		t.Fatalf("PhysicalInputBytes = %v, want 1536", v)
	}

	// Missing stats decode to nil rather than failing the whole list.
	if qs[3].ElapsedMs() != nil || qs[3].PhysicalInputBytes() != nil {
		t.Fatal("absent stats must be nil")
	}
	if _, good := qs[3].CreatedAt(); good {
		t.Fatal("absent createTime must not parse")
	}
}

func TestParseUnits(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"0.00ns": 0, "5us": 5 * time.Microsecond, "18.00ms": 18 * time.Millisecond,
		"1.50s": 1500 * time.Millisecond, "2.00m": 2 * time.Minute, "1.00h": time.Hour, "1d": 24 * time.Hour,
	} {
		if got, ok := ParseDuration(in); !ok || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for in, want := range map[string]int64{"0B": 0, "469B": 469, "1kB": 1024, "1.50kB": 1536, "10MB": 10 << 20, "2GB": 2 << 30} {
		if got, ok := ParseDataSize(in); !ok || got != want {
			t.Errorf("ParseDataSize(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "ms", "12", "1.5xyz"} {
		if _, ok := ParseDuration(bad); ok {
			t.Errorf("ParseDuration(%q) accepted", bad)
		}
		if _, ok := ParseDataSize(bad); ok {
			t.Errorf("ParseDataSize(%q) accepted", bad)
		}
	}
}

// TestGetClusterStats drives the client against a fake coordinator: query
// counts come from /v1/query, the worker count from the nodes statement.
func TestGetClusterStats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/query":
			_, _ = w.Write([]byte(listPayload))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/statement":
			_, _ = w.Write([]byte(`{"id":"nodes","columns":[{"name":"_col0","type":"bigint"}],"data":[[3]],"stats":{"state":"FINISHED"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cs, err := New(srv.URL, "baleia_admin").GetClusterStats(context.Background())
	if err != nil {
		t.Fatalf("GetClusterStats: %v", err)
	}
	// q3 is RUNNING and fully blocked, q4 is QUEUED, the others are done.
	if cs.RunningQueries != 1 || cs.QueuedQueries != 1 || cs.BlockedQueries != 1 || cs.ActiveWorkers != 3 {
		t.Fatalf("stats = %+v", cs)
	}
}
