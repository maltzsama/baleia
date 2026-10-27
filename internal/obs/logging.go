package obs

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// NewLogger is the logger of the api and worker processes (#379): JSON lines
// on w from level up. A record logged with a context that carries a span gets
// the span's trace_id and span_id, so every line can be joined to its trace.
func NewLogger(w io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(traceHandler{slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})})
}

// traceHandler adds the context's trace and span to each record.
type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}
