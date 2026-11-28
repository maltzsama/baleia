package agentpolicy

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/maltzsama/baleia/internal/realtime"
)

func TestRedisGatesShareAgentBurst(t *testing.T) {
	t.Parallel()
	url := os.Getenv("BALEIA_TEST_REDIS_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_REDIS_URL not set")
	}
	prefix := "agent-test-" + uuid.NewString()
	a, b := newFixture(), newFixture()
	for _, f := range []*fixture{a, b} {
		client, err := realtime.Open(t.Context(), realtime.Options{URL: url, Prefix: prefix})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		f.gate.SetSharedLimiter(client)
		f.store.agent.Policy = []byte(`{"max_tool_calls_per_minute":2}`)
	}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := range 20 {
		f := []*fixture{a, b}[i%2]
		wg.Go(func() {
			dec, err := f.gate.Check(context.Background(), f.p, f.call(ToolListCatalogs, nil))
			if err != nil {
				t.Error(err)
				return
			}
			if dec.Allowed {
				allowed.Add(1)
			} else if dec.Reason != ReasonRate || dec.RetryAfter <= 0 || dec.RetryAfter > time.Minute {
				t.Errorf("unexpected refusal: %+v", dec)
			}
		})
	}
	wg.Wait()
	if got := allowed.Load(); got != 2 {
		t.Fatalf("allowed = %d, want 2 across both gates", got)
	}
	for _, limit := range []string{`{"max_tool_calls_per_minute":60}`, `{"max_tool_calls_per_minute":1}`} {
		a.store.agent.Policy, b.store.agent.Policy = []byte(limit), []byte(limit)
		if dec := a.check(t, a.call(ToolListCatalogs, nil)); !dec.Allowed {
			t.Fatalf("changed limit: %+v", dec)
		}
	}
	if dec := b.check(t, b.call(ToolListCatalogs, nil)); dec.Allowed {
		t.Fatal("lowered limit was not shared")
	}
}

// TestDisabledSharedClientFallsBackToLocal covers the deployment with no
// Redis: the shared limiter is disabled, so the gate enforces the per-agent
// burst with its in-process bucket instead of failing or allowing everything.
func TestDisabledSharedClientFallsBackToLocal(t *testing.T) {
	t.Parallel()
	client, err := realtime.Open(t.Context(), realtime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if client.Enabled() {
		t.Fatal("a client without a URL must be disabled")
	}
	f := newFixture()
	f.store.agent.Policy = []byte(`{"max_tool_calls_per_minute":2}`)
	f.gate.SetSharedLimiter(client)
	for i := 0; i < 2; i++ {
		if dec := f.check(t, f.call(ToolListCatalogs, nil)); !dec.Allowed {
			t.Fatalf("call %d = %+v, want allowed", i+1, dec)
		}
	}
	dec := f.check(t, f.call(ToolListCatalogs, nil))
	if dec.Allowed || dec.Reason != ReasonRate {
		t.Fatalf("third call = %+v, want local rate refusal", dec)
	}
	if dec.RetryAfter <= 0 || dec.RetryAfter > time.Minute {
		t.Fatalf("retry after = %v, want positive and bounded", dec.RetryAfter)
	}
}
