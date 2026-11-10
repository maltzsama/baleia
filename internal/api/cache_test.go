package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/realtime"
)

// openSharedCacheClient returns a client for the cross-replica cache tests, or
// skips when BALEIA_TEST_REDIS_URL is unset.
func openSharedCacheClient(t *testing.T, prefix string) *realtime.Client {
	t.Helper()
	url := os.Getenv("BALEIA_TEST_REDIS_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_REDIS_URL not set")
	}
	c, err := realtime.Open(context.Background(), realtime.Options{URL: url, Prefix: prefix})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestCacheLocalFallback(t *testing.T) {
	t.Parallel()
	c := newCache[[]workspaceCatalogEntry](nil, "catalog", time.Minute)
	entries := []workspaceCatalogEntry{{ConnectionID: "c1", CatalogName: "cat"}}
	c.Set("k", entries)
	got, ok := c.Get("k")
	if !ok || len(got) != 1 || got[0].CatalogName != "cat" {
		t.Fatalf("get = %#v, %v", got, ok)
	}
	c.Clear()
	if _, ok := c.Get("k"); ok {
		t.Fatal("cleared key is still present")
	}
}

func TestCacheAcrossReplicas(t *testing.T) {
	t.Parallel()
	prefix := "api-cache-test-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + fmt.Sprint(time.Now().UnixNano())
	a := openSharedCacheClient(t, prefix)
	b := openSharedCacheClient(t, prefix)
	ca := newCache[[]workspaceCatalogEntry](a, "catalog", time.Minute)
	cb := newCache[[]workspaceCatalogEntry](b, "catalog", time.Minute)

	entries := []workspaceCatalogEntry{{ConnectionID: "c1", CatalogName: "cat"}}
	ca.Set("ws-1", entries)
	got, ok := cb.Get("ws-1")
	if !ok || len(got) != 1 || got[0].CatalogName != "cat" {
		t.Fatalf("replica B get = %#v, %v", got, ok)
	}

	// Replica B cached the generation, so a Clear on replica A is seen after
	// cacheGenerationTTL, not instantly.
	ca.Clear()
	time.Sleep(cacheGenerationTTL + 100*time.Millisecond)
	if _, ok := cb.Get("ws-1"); ok {
		t.Fatal("replica B still sees a cleared value")
	}
}
