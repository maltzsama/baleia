// Package wsticket issues short-lived, single-use tickets for WebSocket
// authentication so the JWT never appears in a URL (#26).
package wsticket

import (
	"crypto/rand"
	"sync"
	"time"
)

// TTL is how long an issued ticket stays valid.
const TTL = 30 * time.Second

// Ticket is what a consumed token authorizes.
type Ticket struct {
	UserID      string
	ExecutionID string
	ExpiresAt   time.Time
}

// Store keeps tickets in memory. The API is single-process for the dev and
// test stacks; a multi-replica deployment would back it with a shared store.
type Store struct {
	mu      sync.Mutex
	now     func() time.Time
	tickets map[string]Ticket
}

// New returns a store using the wall clock.
func New() *Store {
	return NewWithClock(time.Now)
}

// NewWithClock returns a store with an injectable clock (tests).
func NewWithClock(now func() time.Time) *Store {
	return &Store{now: now, tickets: make(map[string]Ticket)}
}

// Issue mints a single-use ticket. An empty executionID makes it valid on any
// execution the user may access.
func (s *Store) Issue(userID, executionID string) (string, time.Time) {
	token := rand.Text()
	expiresAt := s.now().Add(TTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	s.tickets[token] = Ticket{UserID: userID, ExecutionID: executionID, ExpiresAt: expiresAt}
	return token, expiresAt
}

// Consume returns the ticket and removes it, whether or not it was expired: a
// ticket is never valid twice.
func (s *Store) Consume(token string) (Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.tickets[token]
	if !ok {
		return Ticket{}, false
	}
	delete(s.tickets, token)
	if s.now().After(ticket.ExpiresAt) {
		return Ticket{}, false
	}
	return ticket, true
}

func (s *Store) cleanupLocked() {
	now := s.now()
	for token, ticket := range s.tickets {
		if now.After(ticket.ExpiresAt) {
			delete(s.tickets, token)
		}
	}
}
