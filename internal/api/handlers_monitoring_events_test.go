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
	ch := make(chan events.Event, 1)
	ch <- events.Event{EngineID: "e1", Kind: events.KindQueries}
	close(ch)

	stream := &monitoringSSE{ctx: context.Background(), events: ch, ping: time.NewTicker(time.Hour)}
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
