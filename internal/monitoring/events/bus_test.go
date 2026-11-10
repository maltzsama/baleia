package events

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/realtime"
)

func openBusClient(t *testing.T, prefix string) *realtime.Client {
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

func TestBusTransportAcrossReplicas(t *testing.T) {
	t.Parallel()
	prefix := "events-test-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + fmt.Sprint(time.Now().UnixNano())
	writer := NewBus(openBusClient(t, prefix))
	reader := NewBus(openBusClient(t, prefix))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan string, 4)
	connected := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- reader.Listen(ctx, Channel, func() {
			select {
			case connected <- struct{}{}:
			default:
			}
		}, func(payload string) {
			select {
			case received <- payload:
			default:
			}
		})
	}()
	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("listener never reported connected")
	}

	// The subscription is acknowledged asynchronously, so retry until the
	// message crosses.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := writer.Notify(context.Background(), Channel, "engine-1:queries"); err != nil {
			t.Fatalf("notify: %v", err)
		}
		select {
		case got := <-received:
			if got != "engine-1:queries" {
				t.Fatalf("received %q", got)
			}
			cancel()
			if err := <-done; err != nil && ctx.Err() == nil {
				t.Fatalf("listen returned %v", err)
			}
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("no payload received")
}
