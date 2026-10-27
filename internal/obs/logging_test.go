package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("not one JSON line: %v (%q)", err, buf.String())
	}
	buf.Reset()
	return line
}

// TestNewLogger_CorrelatesWithTheTrace: a record logged with a span's context
// carries its trace_id and span_id; without one it carries neither.
func TestNewLogger_CorrelatesWithTheTrace(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, slog.LevelInfo).With("service", "api")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x0a},
		SpanID:     trace.SpanID{0x0b},
		TraceFlags: trace.FlagsSampled,
	})

	logger.InfoContext(trace.ContextWithSpanContext(context.Background(), sc), "query finished", "rows", 3)
	line := decodeLine(t, &buf)
	if line["trace_id"] != sc.TraceID().String() || line["span_id"] != sc.SpanID().String() {
		t.Errorf("line = %v, want the span's trace_id and span_id", line)
	}
	if line["service"] != "api" || line["msg"] != "query finished" {
		t.Errorf("line = %v, want the logger's attributes kept", line)
	}

	logger.InfoContext(context.Background(), "boot")
	if line := decodeLine(t, &buf); line["trace_id"] != nil {
		t.Errorf("line without a span = %v, want no trace_id", line)
	}
}

// TestNewLogger_Level: records under the level are dropped.
func TestNewLogger_Level(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, slog.LevelWarn)
	logger.InfoContext(context.Background(), "quiet")
	if buf.Len() != 0 {
		t.Errorf("an info line passed a warn level: %q", buf.String())
	}
	logger.WarnContext(context.Background(), "loud")
	if line := decodeLine(t, &buf); line["level"] != "WARN" {
		t.Errorf("line = %v, want the warning", line)
	}
}
