package queryengine

import (
	"context"
	"sync"
	"time"
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
func (m *managed) OpenSession(ctx context.Context, spec SessionSpec) (Session, error) {
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

func (s *managedSession) Close() error {
	err := s.Session.Close()
	s.once.Do(s.owner.sessionDone)
	return err
}

// Unwrap returns the adapter behind the wrapper.
func (m *managed) Unwrap() Engine { return m.Engine }
