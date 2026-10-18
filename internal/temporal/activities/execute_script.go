package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/arrowipc"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/policy"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// Statement codes that are not engine codes (queryengine.ErrorCode).
const (
	// CodeInterrupted: the worker running the statement stopped (crash,
	// heartbeat timeout); the statement's outcome on the engine is unknown.
	CodeInterrupted = "interrupted"
	// codeEditorRejected: the editor gate refused the statement.
	codeEditorRejected = "editor_rejected"
)

// StatementRef is one statement of a script, in execution order.
type StatementRef struct {
	Position int32
	SQL      string
}

// ExecuteScriptParams runs every statement of an execution on its target.
// It carries control only: the engine's connection is loaded and decrypted
// here, never passed through workflow history.
type ExecuteScriptParams struct {
	ExecutionID string
	Target      queryengine.Target
	Identity    queryengine.Identity
	Statements  []StatementRef
	Timeout     time.Duration // per statement; 0 = engine default
	RequestID   string
}

// StatementOutcome is how one statement ended. It is control data only:
// result rows live in object storage under ResultKey.
type StatementOutcome struct {
	Position      int32
	Status        string // finished | failed | cancelled
	EngineQueryID string
	RowCount      int64
	DurationMs    int64
	ResultKey     string // "" when the statement returned no result set (DDL/DML)
	ResultBytes   int64
	Truncated     bool
	ErrorCode     string
	Error         string
}

// ExecuteScriptResult lists the statements that ran, in order. After a
// failure or a cancel on the engine the rest were skipped and are not listed.
type ExecuteScriptResult struct {
	Statements []StatementOutcome
}

// heartbeatDetails travel with each heartbeat: which statement runs and its
// engine handle. Never rows, never SQL.
type heartbeatDetails struct {
	Position int32
	Handle   queryengine.QueryHandle
}

// heartbeatInterval is how often the script heartbeats while it runs, also
// while an engine blocks without reporting progress; the workflow's
// heartbeat timeout is 3x this interval. A var so tests can shrink it.
var heartbeatInterval = 10 * time.Second

// recordHeartbeat is activity.RecordHeartbeat; tests count calls through it.
var recordHeartbeat = activity.RecordHeartbeat

// statementTransportBackoff bounds retries of transport failures that happen
// before the engine accepted a statement. It is a package var so tests can
// shrink it.
var statementTransportBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// arrowContentType is the MIME type used for stored Arrow IPC streams.
const arrowContentType = "application/vnd.apache.arrow.file"

// errResultCapReached signals that a statement hit the row/byte cap. It is an
// internal sentinel, converted to a truncated (not failed) result.
var errResultCapReached = errors.New("statement result cap reached")

// ExecuteScript runs every statement of an execution in ONE engine session
// (#27), so USE/SET, temporary tables and transactions persist from one
// statement to the next on every engine. It is never retried: a session
// cannot be resumed and statements are not idempotent. An execution is not a
// transaction: closing the session rolls back what the user left open.
func ExecuteScript(ctx context.Context, params ExecuteScriptParams) (*ExecuteScriptResult, error) {
	return defaultActivities.executeScript(ctx, params)
}

// script is the state of one ExecuteScript call.
type script struct {
	a      *Activities
	ctx    context.Context
	bk     context.Context // bookkeeping: survives the activity's cancellation
	p      ExecuteScriptParams
	execID pgtype.UUID
	eng    queryengine.Engine

	mu      sync.Mutex // guards current
	current heartbeatDetails
	res     ExecuteScriptResult
}

func (a *Activities) executeScript(ctx context.Context, p ExecuteScriptParams) (*ExecuteScriptResult, error) {
	s := &script{a: a, ctx: ctx, bk: context.WithoutCancel(ctx), p: p}
	if err := s.execID.Scan(p.ExecutionID); err != nil {
		return nil, temporal.NewNonRetryableApplicationError("invalid execution id", "invalid_input", err)
	}
	if len(p.Statements) == 0 {
		return &s.res, nil
	}
	activity.GetLogger(ctx).Info("executing script", "execution_id", p.ExecutionID, "request_id", p.RequestID, "statements", len(p.Statements))

	eng, err := s.engine()
	if err != nil {
		return s.failBeforeStart(err)
	}
	s.eng = eng
	sess, err := s.openSession()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, s.cancelled(p.Statements[0].Position, queryengine.QueryHandle{})
		}
		return s.failBeforeStart(err)
	}
	defer func() { _ = sess.Close() }()
	stopBeats := s.heartbeats()
	defer stopBeats()

	for _, st := range p.Statements {
		stop, err := s.run(sess, st)
		if err != nil || stop {
			return &s.res, err
		}
	}
	return &s.res, nil
}

// engine returns the target's engine; the execution path never falls back
// to a default engine.
func (s *script) engine() (queryengine.Engine, error) {
	if !s.p.Target.EngineID.Valid {
		return nil, queryengine.NewError(queryengine.CodeNotFound, "", "", "the execution target has no engine")
	}
	return s.a.engines.Get(s.ctx, s.p.Target.EngineID)
}

func (s *script) openSession() (queryengine.Session, error) {
	spec := queryengine.SessionSpec{
		Catalog:  s.p.Target.EngineCatalog,
		Identity: s.p.Identity,
		ReadOnly: s.p.Target.ReadOnly,
		Timeout:  s.p.Timeout,
		Tags:     queryengine.Tags{ExecutionID: s.p.ExecutionID, RequestID: s.p.RequestID, Source: "editor"},
	}
	if s.p.Target.Schema != "" {
		spec.Schema = []string{s.p.Target.Schema}
	}
	for attempt := 0; ; attempt++ {
		sess, err := s.eng.OpenSession(s.ctx, spec)
		if err == nil || !s.retryable(err, attempt) {
			return sess, err
		}
	}
}

// retryable reports whether a transport failure before the engine accepted
// anything may be retried, after waiting for its backoff.
func (s *script) retryable(err error, attempt int) bool {
	if s.ctx.Err() != nil || queryengine.CodeOf(err) != queryengine.CodeUnavailable || attempt >= len(statementTransportBackoff) {
		return false
	}
	select {
	case <-s.ctx.Done():
		return false
	case <-time.After(statementTransportBackoff[attempt]):
		return true
	}
}

// beat records the running statement and heartbeats it at once.
// Heartbeats deliver the workflow's cancellation to this activity.
func (s *script) beat(position int32, h queryengine.QueryHandle) {
	s.mu.Lock()
	s.current = heartbeatDetails{Position: position, Handle: h}
	d := s.current
	s.mu.Unlock()
	recordHeartbeat(s.ctx, d)
}

// heartbeats keeps heartbeating the running statement every
// heartbeatInterval until the returned stop is called.
func (s *script) heartbeats() (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-s.ctx.Done():
				return
			case <-t.C:
				s.mu.Lock()
				d := s.current
				s.mu.Unlock()
				recordHeartbeat(s.ctx, d)
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// failBeforeStart fails the first statement with the error that kept the
// script from starting (no engine, no session) and skips the rest.
func (s *script) failBeforeStart(err error) (*ExecuteScriptResult, error) {
	pos := s.p.Statements[0].Position
	if _, serr := s.a.queries.StartQueryStatement(s.bk, db.StartQueryStatementParams{ExecutionID: s.execID, Position: pos}); serr != nil {
		return nil, serr
	}
	return &s.res, s.finish(StatementOutcome{Position: pos, Status: "failed", ErrorCode: string(queryengine.CodeOf(err)), Error: err.Error()})
}

// finish records a statement's outcome; after a failure or a cancel it
// skips the statements that have not run.
func (s *script) finish(o StatementOutcome) error {
	s.res.Statements = append(s.res.Statements, o)
	if err := s.a.recordOutcome(s.bk, s.execID, o); err != nil {
		return err
	}
	if o.Status != "finished" {
		return s.a.queries.SkipPendingStatements(s.bk, s.execID)
	}
	return nil
}

// cancelled records the statement at position as cancelled and skips the
// rest; the returned error tells Temporal the activity honoured the cancel.
func (s *script) cancelled(position int32, h queryengine.QueryHandle) error {
	if h.QueryID != "" {
		s.cancelOnEngine(h, "cancelled by baleia")
	}
	_, _ = s.a.queries.AbandonRunningStatements(s.bk, db.AbandonRunningStatementsParams{
		ExecutionID: s.execID, Status: "cancelled", ErrorCode: optional(string(queryengine.CodeCancelled)),
	})
	_ = s.a.queries.SkipPendingStatements(s.bk, s.execID)
	activity.GetLogger(s.ctx).Info("script cancelled", "execution_id", s.p.ExecutionID, "position", position)
	return temporal.NewCanceledError()
}

// cancelOnEngine stops a statement on the engine, bounded in time.
func (s *script) cancelOnEngine(h queryengine.QueryHandle, reason string) {
	ctx, cancel := context.WithTimeout(s.bk, 5*time.Second)
	defer cancel()
	if err := s.eng.Cancel(ctx, h); err != nil {
		activity.GetLogger(s.ctx).Warn("cancel on the engine failed", "reason", reason, "query_id", h.QueryID, "err", err)
	}
}

// run executes one statement on the session. stop is true when the script
// must not continue (the statement failed).
func (s *script) run(sess queryengine.Session, st StatementRef) (stop bool, err error) {
	n, err := s.a.queries.StartQueryStatement(s.bk, db.StartQueryStatementParams{ExecutionID: s.execID, Position: st.Position})
	if err != nil {
		return true, fmt.Errorf("start statement %d: %w", st.Position, err)
	}
	if n == 0 {
		// Abandoned elsewhere (the workflow gave up on this run): stop.
		return true, nil
	}
	s.beat(st.Position, queryengine.QueryHandle{})

	// Defense in depth: SplitSQL already rejected forbidden editor
	// statements before anything ran.
	if policy.ForbiddenInEditor(s.eng.Dialect(), st.SQL) {
		return true, s.finish(StatementOutcome{Position: st.Position, Status: "failed", ErrorCode: codeEditorRejected, Error: policy.ErrForbiddenInEditor.Error()})
	}

	start := time.Now()
	var handle queryengine.QueryHandle
	cb := queryengine.Callbacks{
		OnStart: func(h queryengine.QueryHandle) {
			handle = h
			s.beat(st.Position, h)
			s.persistHandle(st.Position, h)
		},
	}
	stmtCtx, cancel := s.ctx, context.CancelFunc(func() {})
	if s.p.Timeout > 0 {
		stmtCtx, cancel = context.WithTimeout(s.ctx, s.p.Timeout)
	}
	defer cancel()

	// A statement is never retried once the engine has accepted it: retrying
	// an INSERT/CREATE could write twice. Only an unavailable engine before
	// the statement got a handle is retried, on the same session.
	sink := &resultSink{a: s.a, ctx: s.ctx, executionID: s.p.ExecutionID, position: st.Position}
	for attempt := 0; ; attempt++ {
		handle = queryengine.QueryHandle{}
		err = sess.Execute(stmtCtx, st.SQL, cb, sink)
		if err == nil || handle.QueryID != "" || sink.began || !s.retryable(err, attempt) {
			break
		}
	}

	elapsed := time.Since(start)
	observe := func(outcome string, err error, rows, bytes int64) {
		s.a.metrics.engine.Statement(s.eng.Kind(), outcome, string(queryengine.CodeOf(err)), elapsed, rows, bytes)
	}
	o := StatementOutcome{Position: st.Position, EngineQueryID: handle.QueryID, DurationMs: elapsed.Milliseconds()}

	// Stopped by the workflow or a deadline: finalize the upload first so it
	// cannot deadlock, then stop the statement on the engine. Only a real
	// cancel is a cancellation; the script's own deadline is a timeout.
	timedOut := func(msg string) (bool, error) {
		sink.abort()
		if handle.QueryID != "" {
			s.cancelOnEngine(handle, "timeout")
		}
		terr := queryengine.NewError(queryengine.CodeTimeout, s.eng.Kind(), "", msg)
		observe(metrics.StatementError, terr, 0, 0)
		o.Status, o.ErrorCode, o.Error = "failed", string(queryengine.CodeTimeout), terr.Error()
		return true, s.finish(o)
	}
	switch {
	case errors.Is(s.ctx.Err(), context.Canceled):
		sink.abort()
		observe(metrics.StatementCancelled, nil, 0, 0)
		return true, s.cancelled(st.Position, handle)
	case s.ctx.Err() != nil:
		return timedOut("the execution ran longer than its timeout")
	case stmtCtx.Err() != nil:
		return timedOut(fmt.Sprintf("the statement ran longer than %s", s.p.Timeout))
	}

	if errors.Is(err, errResultCapReached) {
		if handle.QueryID != "" {
			s.cancelOnEngine(handle, "statement result cap reached")
		}
		sink.truncated = true
		err = nil
	}
	if cerr := sink.finish(); cerr != nil && err == nil {
		err = cerr
	}

	// A failed upload also fails the writes into its pipe: report the upload.
	if sink.uploadErr != nil {
		observe(metrics.StatementError, sink.uploadErr, sink.rows, 0)
		o.Status, o.RowCount = "failed", sink.rows
		o.ErrorCode, o.Error = string(queryengine.CodeInternal), fmt.Sprintf("store results: %v", sink.uploadErr)
		return true, s.finish(o)
	}
	// Cancelled on the engine (the cancel endpoint stops the statement by
	// its handle, usually before the workflow's cancel reaches this
	// activity): a cancellation, not a failure.
	if queryengine.CodeOf(err) == queryengine.CodeCancelled {
		observe(metrics.StatementCancelled, nil, 0, 0)
		o.Status, o.ErrorCode = "cancelled", string(queryengine.CodeCancelled)
		return true, s.finish(o)
	}
	if err != nil {
		observe(metrics.StatementError, err, 0, 0)
		o.Status, o.ErrorCode, o.Error = "failed", string(queryengine.CodeOf(err)), err.Error()
		return true, s.finish(o)
	}

	o.Status, o.RowCount, o.Truncated = "finished", sink.rows, sink.truncated
	if sink.writer != nil {
		o.ResultKey, o.ResultBytes = sink.key, sink.uploaded
	}
	outcome := metrics.StatementSuccess
	if sink.truncated {
		outcome = metrics.StatementTruncated
	}
	observe(outcome, nil, sink.rows, o.ResultBytes)
	return false, s.finish(o)
}

// persistHandle stores the statement's engine handle so the API, or the
// workflow after a crash, can cancel it from another process (#20). A
// failure only loses that ability: logged.
func (s *script) persistHandle(position int32, h queryengine.QueryHandle) {
	raw, err := json.Marshal(h)
	if err != nil {
		return
	}
	if err := s.a.queries.SetStatementQueryHandle(s.bk, db.SetStatementQueryHandleParams{
		ExecutionID: s.execID, Position: position, QueryHandle: raw,
	}); err != nil {
		activity.GetLogger(s.ctx).Warn("could not persist the query handle", "execution_id", s.p.ExecutionID, "err", err)
	}
}

// AbandonStatementsParams ends what a stopped script left behind.
type AbandonStatementsParams struct {
	ExecutionID string
	EngineID    pgtype.UUID
	Status      string // "cancelled" or "failed"
	ErrorCode   string
	Error       string
}

// AbandonStatements runs after ExecuteScript stopped without finishing (worker
// crash, heartbeat timeout, cancel): it asks the engine to stop the statement
// still recorded as running, best effort, then ends it with params.Status and
// skips the statements that never ran. It is idempotent.
func AbandonStatements(ctx context.Context, params AbandonStatementsParams) error {
	return defaultActivities.abandonStatements(ctx, params)
}

func (a *Activities) abandonStatements(ctx context.Context, p AbandonStatementsParams) error {
	var execID pgtype.UUID
	if err := execID.Scan(p.ExecutionID); err != nil {
		return temporal.NewNonRetryableApplicationError("invalid execution id", "invalid_input", err)
	}
	rows, err := a.queries.ListRunningStatementHandles(ctx, execID)
	if err != nil {
		return err
	}
	if len(rows) > 0 && p.EngineID.Valid {
		a.cancelHandles(ctx, p, rows)
	}
	if _, err := a.queries.AbandonRunningStatements(ctx, db.AbandonRunningStatementsParams{
		ExecutionID:  execID,
		Status:       p.Status,
		ErrorCode:    optional(p.ErrorCode),
		ErrorMessage: optional(p.Error),
	}); err != nil {
		return err
	}
	return a.queries.SkipPendingStatements(ctx, execID)
}

// cancelHandles asks the engine to stop orphaned statements, bounded in time.
func (a *Activities) cancelHandles(ctx context.Context, p AbandonStatementsParams, rows []db.ListRunningStatementHandlesRow) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	logger := activity.GetLogger(ctx)
	eng, err := a.engines.Get(ctx, p.EngineID)
	if err != nil {
		logger.Warn("abandon statements: no engine", "execution_id", p.ExecutionID, "err", err)
		return
	}
	for _, r := range rows {
		var h queryengine.QueryHandle
		if json.Unmarshal(r.QueryHandle, &h) != nil || h.QueryID == "" {
			continue
		}
		if err := eng.Cancel(ctx, h); err != nil {
			logger.Warn("abandon statements: cancel on the engine failed", "execution_id", p.ExecutionID, "position", r.Position, "err", err)
		}
	}
}

// countingWriter counts bytes while proxying to the underlying pipe.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// s3ResultKey lays out result objects per statement so future result sets
// (result_index > 0) fit the same layout.
func s3ResultKey(executionID string, position, resultIndex int) string {
	return fmt.Sprintf("query-results/%s/%d/%d.arrow", executionID, position, resultIndex)
}

// resultSink streams the first result set of a statement to object storage as
// one Arrow IPC *file*: batch -> IPC file writer -> io.Pipe -> S3 multipart
// upload. The file footer indexes every block, so the results endpoint can
// later seek to a row range instead of re-reading the whole object (#238).
// A result set without columns (DDL/DML) uploads nothing. Past the row or byte
// cap it stops the statement with errResultCapReached; the stored result is
// then truncated, not failed.
type resultSink struct {
	a           *Activities
	ctx         context.Context
	executionID string
	position    int32

	began     bool
	sets      int
	key       string
	writer    *arrowipc.FileWriter
	pw        *io.PipeWriter
	cw        *countingWriter
	done      chan struct{}
	uploaded  int64
	uploadErr error
	rows      int64
	truncated bool
}

func (s *resultSink) Begin(schema *arrow.Schema) error {
	s.began = true
	s.sets++
	if s.sets > 1 || schema.NumFields() == 0 {
		return nil // one stored result per statement; result_sets is #20 PR 3
	}
	s.key = s3ResultKey(s.executionID, int(s.position), 0)
	pr, pw := io.Pipe()
	s.pw = pw
	s.cw = &countingWriter{w: pw}
	writer, err := arrowipc.NewFileWriter(s.cw, schema)
	if err != nil {
		_ = pw.CloseWithError(err)
		return err
	}
	s.writer = writer
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		n, err := s.a.s3.PutStream(s.ctx, s.key, pr, arrowContentType)
		if err != nil {
			s.uploadErr = err
			_ = pr.CloseWithError(err)
			return
		}
		s.uploaded = n
	}()
	return nil
}

func (s *resultSink) Write(rec arrow.RecordBatch) error {
	defer rec.Release()
	if s.sets > 1 || s.writer == nil {
		return nil
	}
	maxRows, maxBytes := s.a.cfg.ResultMaxRows, s.a.cfg.ResultMaxBytes
	if s.rows >= maxRows || s.cw.n >= maxBytes {
		return errResultCapReached
	}
	batch := rec
	capped := false
	if remaining := maxRows - s.rows; rec.NumRows() > remaining {
		batch = rec.NewSlice(0, remaining)
		defer batch.Release()
		capped = true
	}
	if err := s.writer.Write(batch); err != nil {
		return err
	}
	s.rows += batch.NumRows()
	if capped || s.cw.n >= maxBytes {
		return errResultCapReached
	}
	return nil
}

func (s *resultSink) End(queryengine.Stats) error { return nil }

// finish closes the IPC stream and waits for the upload.
func (s *resultSink) finish() error {
	if s.writer == nil {
		return nil
	}
	err := s.writer.Close()
	_ = s.pw.Close()
	<-s.done
	return err
}

// errResultAborted fails an upload whose statement did not finish, so no
// partial object is stored.
var errResultAborted = errors.New("result upload aborted")

// abort fails the upload: the stream is incomplete and must not be stored.
func (s *resultSink) abort() {
	if s.pw == nil {
		return
	}
	_ = s.pw.CloseWithError(errResultAborted)
	<-s.done
}
