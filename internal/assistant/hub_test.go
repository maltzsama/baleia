package assistant_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/assistant"
	"github.com/galileostudio/baleia/internal/realtime"
)

func TestHubLocal(t *testing.T) {
	t.Parallel()
	h := assistant.NewHub()
	id := [16]byte{1}
	ch, unsub := h.Subscribe(id)
	defer unsub()

	h.Publish(id, assistant.Event{Type: "delta", Delta: "hi"})
	select {
	case ev := <-ch:
		if ev.Type != "delta" || ev.Delta != "hi" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestHubAcrossReplicas(t *testing.T) {
	t.Parallel()
	url := os.Getenv("BALEIA_TEST_REDIS_URL")
	if url == "" {
		t.Skip("BALEIA_TEST_REDIS_URL not set")
	}
	prefix := "assistant-test-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + fmt.Sprint(time.Now().UnixNano())
	open := func() *realtime.Client {
		c, err := realtime.Open(context.Background(), realtime.Options{URL: url, Prefix: prefix})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	a := assistant.NewHub()
	b := assistant.NewHub()
	a.SetBus(open())
	b.SetBus(open())

	id := [16]byte{0xaa}
	ch, unsub := b.Subscribe(id)
	defer unsub()

	// The bus subscription is acknowledged asynchronously, so retry until the
	// delta crosses; a delta carries no sequence to dedupe it.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a.Publish(id, assistant.Event{Type: "delta", Delta: "from-a"})
		select {
		case ev := <-ch:
			if ev.Type != "delta" || ev.Delta != "from-a" {
				t.Fatalf("event = %+v", ev)
			}
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("no event received across replicas")
}
