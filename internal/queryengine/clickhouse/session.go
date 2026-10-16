package clickhouse

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/google/uuid"
)

// session is one ClickHouse HTTP session. USE and SET persist on it; ClickHouse
// HTTP sessions are exclusive, so requests are serialized.
type session struct {
	engine    *Engine
	spec      queryengine.SessionSpec
	sessionID string
	readOnly  bool

	mu sync.Mutex
}

func newSession(e *Engine, spec queryengine.SessionSpec) *session {
	return &session{engine: e, spec: spec, sessionID: uuid.NewString(), readOnly: spec.ReadOnly}
}

// exec runs a statement and discards its result (USE on open).
func (s *session) exec(ctx context.Context, sql string) error {
	return s.engine.c.streamArrow(ctx, s.request(sql, ""), discardSink{}, time.Now())
}

// Execute runs one statement and streams its Arrow result into sink. ClickHouse
// is stateless per request apart from the session; cancelling ctx aborts the
// request.
func (s *session) Execute(ctx context.Context, sql string, cb queryengine.Callbacks, sink queryengine.RecordSink) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := time.Now()
	queryID := uuid.NewString()
	if cb.OnStart != nil {
		cb.OnStart(queryengine.QueryHandle{EngineID: s.engine.ID(), QueryID: queryID})
	}
	runCtx := ctx
	if s.spec.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, s.spec.Timeout)
		defer cancel()
	}
	return s.engine.c.streamArrow(runCtx, s.request(sql, queryID), sink, start)
}

// Close ends the session; the server expires it, nothing to free.
func (s *session) Close() error { return nil }

func (s *session) request(sql, queryID string) request {
	return request{
		sql:        sql,
		queryID:    queryID,
		sessionID:  s.sessionID,
		readOnly:   s.readOnly,
		timeout:    s.spec.Timeout,
		logComment: logComment(s.spec.Tags),
	}
}

// logComment tags every request for correlation in system.query_log.
func logComment(t queryengine.Tags) string {
	if t.Source == "" && t.ExecutionID == "" && t.RequestID == "" {
		return ""
	}
	b, err := json.Marshal(struct {
		Source      string `json:"source,omitempty"`
		ExecutionID string `json:"execution_id,omitempty"`
		RequestID   string `json:"request_id,omitempty"`
	}{t.Source, t.ExecutionID, t.RequestID})
	if err != nil {
		return ""
	}
	return string(b)
}

// discardSink ignores a result set (statements that return nothing).
type discardSink struct{}

func (discardSink) Begin(*arrow.Schema) error         { return nil }
func (discardSink) Write(rec arrow.RecordBatch) error { rec.Release(); return nil }
func (discardSink) End(queryengine.Stats) error       { return nil }
