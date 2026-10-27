package queryengine

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// tracer names the engines' spans (#380). It is resolved at each span, not
// once: a tracer taken from the global provider stays bound to the first
// provider installed. With tracing off it is the no-op one.
func tracer() trace.Tracer {
	return otel.Tracer("github.com/galileostudio/baleia/internal/queryengine")
}

// Span attributes. They are bounded: never the SQL text, which can carry
// literals and user data.
const (
	attrKind       = attribute.Key("baleia.engine.kind")
	attrReadOnly   = attribute.Key("baleia.session.read_only")
	attrRows       = attribute.Key("baleia.rows")
	attrResultSets = attribute.Key("baleia.result_sets")
	attrErrorCode  = attribute.Key("baleia.error.code")
)

// failSpan marks span failed with the error's code. The message is not
// recorded: an engine's error text can quote the statement.
func failSpan(span trace.Span, err error) {
	if err == nil {
		return
	}
	code := string(CodeOf(err))
	span.SetAttributes(attrErrorCode.String(code))
	span.SetStatus(codes.Error, code)
}

// countingSink passes results through and counts them for the span.
type countingSink struct {
	RecordSink
	rows int64
	sets int
}

func (s *countingSink) End(stats Stats) error {
	s.rows += stats.RowCount
	s.sets++
	return s.RecordSink.End(stats)
}
