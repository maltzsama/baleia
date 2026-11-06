package api

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/monitoring/events"
)

func TestMonitoringSSEStreamsChangedEvents(t *testing.T) {
	t.Parallel()
	hub := events.NewHub()
	sub, unsub := hub.Subscribe("e1")
	defer unsub()
	hub.Publish("e1", events.KindQueries)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // flush the pending kind, then return on ctx.Done
	stream := &monitoringSSE{ctx: ctx, sub: sub, ping: time.NewTicker(time.Hour)}
	defer func() { _ = stream.Close() }()

	var buf bytes.Buffer
	if _, err := stream.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	out := buf.String()
	for _, want := range []string{": connected", "event: changed", `"engine_id":"e1"`, `"kind":"queries"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("stream %q lacks %q", out, want)
		}
	}
}
