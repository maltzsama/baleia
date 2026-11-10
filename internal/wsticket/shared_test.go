package wsticket

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/realtime"
)

func TestStoreSharedAcrossReplicas(t *testing.T) {
	t.Parallel()
	url := os.Getenv("BALEIA_TEST_REDIS_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_REDIS_URL not set")
	}
	prefix := "wsticket-test-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + fmt.Sprint(time.Now().UnixNano())
	open := func() *realtime.Client {
		c, err := realtime.Open(context.Background(), realtime.Options{URL: url, Prefix: prefix})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	a := New()
	a.SetBus(open())
	b := New()
	b.SetBus(open())

	// The ticket is issued by replica A and consumed by replica B.
	token, _ := a.Issue("user-1", "exec-1")
	ticket, ok := b.Consume(token)
	if !ok || ticket.UserID != "user-1" || ticket.ExecutionID != "exec-1" {
		t.Fatalf("replica B consume = %+v, %v", ticket, ok)
	}
	if _, ok := b.Consume(token); ok {
		t.Fatal("a ticket is valid exactly once across replicas")
	}
}
