package queryengine

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// managed wraps an engine built by the Manager. It enforces the read-only rule
// for every engine and keeps the instance open until its sessions close after
// it is retired.
type managed struct {
	Engine
	version             time.Time
	credentialsReadOnly bool
	// standalone instances (Guard) close their engine on Close; the
	// Manager's instances live until retired.
	standalone bool

	mu       sync.Mutex
	sessions int
	retired  bool
	closed   bool
}

// OpenSession refuses a read-only session on an engine that cannot enforce it,
// unless the connection asserts read-only credentials (docs/adr/0011). It
// never downgrades silently.
func (m *managed) OpenSession(ctx context.Context, spec SessionSpec) (_ Session, err error) {
	ctx, span := tracer().Start(ctx, "engine.open_session",
		trace.WithAttributes(attrKind.String(m.Kind()), attrReadOnly.Bool(spec.ReadOnly)))
	defer func() {
		failSpan(span, err)
		span.End()
	}()
	if spec.ReadOnly && m.Capabilities().ReadOnly != ReadOnlyEnforced && !m.credentialsReadOnly {
		return nil, NewError(CodeReadOnly, m.Kind(), "",
			"this engine cannot enforce a read-only session and its connection does not assert read-only credentials")
	}
	m.mu.Lock()
	if m.retired || m.closed {
		m.mu.Unlock()
		return nil, NewError(CodeUnavailable, m.Kind(), "", "engine instance was replaced; get it again from the manager")
	}
	m.sessions++
	m.mu.Unlock()

	s, err := m.Engine.OpenSession(ctx, spec)
	if err != nil {
		m.sessionDone()
		return nil, err
	}
	return &managedSession{Session: s, owner: m}, nil
}

func (m *managed) sessionDone() {
	m.mu.Lock()
	m.sessions--
	closeNow := m.retired && m.sessions == 0 && !m.closed
	if closeNow {
		m.closed = true
	}
	m.mu.Unlock()
	if closeNow {
		_ = m.Engine.Close()
	}
}

// retire marks the instance replaced: it closes now when idle, else when its
// last session closes.
func (m *managed) retire() {
	m.mu.Lock()
	m.retired = true
	closeNow := m.sessions == 0 && !m.closed
	if closeNow {
		m.closed = true
	}
	m.mu.Unlock()
	if closeNow {
		_ = m.Engine.Close()
	}
}

// Close is a no-op for the Manager's instances, which it retires itself.
func (m *managed) Close() error {
	if m.standalone {
		return m.Engine.Close()
	}
	return nil
}

// Guard applies the generic rules the Manager applies to every engine (today
// the read-only rule) to an engine built outside it, such as in tests and the
// conformance suite.
func Guard(e Engine, credentialsReadOnly bool) Engine {
	return &managed{Engine: e, credentialsReadOnly: credentialsReadOnly, standalone: true}
}

type managedSession struct {
	Session
	owner *managed
	once  sync.Once
}

// Execute runs the statement inside an engine.execute span that records how
// many rows and result sets it returned (#380).
func (s *managedSession) Execute(ctx context.Context, sql string, cb Callbacks, sink RecordSink) error {
	ctx, span := tracer().Start(ctx, "engine.execute", trace.WithAttributes(attrKind.String(s.owner.Kind())))
	defer span.End()
	counted := &countingSink{RecordSink: sink}
	err := s.Session.Execute(ctx, sql, cb, counted)
	span.SetAttributes(attrRows.Int64(counted.rows), attrResultSets.Int(counted.sets))
	failSpan(span, err)
	return err
}

func (s *managedSession) Close() error {
	err := s.Session.Close()
	s.once.Do(s.owner.sessionDone)
	return err
}

// Unwrap returns the adapter behind the wrapper.
func (m *managed) Unwrap() Engine { return m.Engine }
