// Package wsticket issues short-lived, single-use tickets for WebSocket
// authentication so the JWT never appears in a URL (#26).
package wsticket

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/realtime"
)

// TTL is how long an issued ticket stays valid.
const TTL = 30 * time.Second

// Ticket is what a consumed token authorizes.
type Ticket struct {
	UserID      string    `json:"user_id"`
	ExecutionID string    `json:"execution_id"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// Store keeps tickets in memory, or in the shared layer when one is wired, so
// a ticket issued by one replica is consumed by another (ADR 0022).
type Store struct {
	mu      sync.Mutex
	now     func() time.Time
	tickets map[string]Ticket
	bus     *realtime.Client
}

// New returns a store using the wall clock.
func New() *Store {
	return NewWithClock(time.Now)
}

// NewWithClock returns a store with an injectable clock (tests).
func NewWithClock(now func() time.Time) *Store {
	return &Store{now: now, tickets: make(map[string]Ticket)}
}

// SetBus backs the tickets with the shared layer. Call it once, at startup.
func (s *Store) SetBus(bus *realtime.Client) {
	s.mu.Lock()
	s.bus = bus
	s.mu.Unlock()
}

// Issue mints a single-use ticket. An empty executionID makes it valid on any
// execution the user may access.
func (s *Store) Issue(userID, executionID string) (string, time.Time) {
	token := rand.Text()
	expiresAt := s.now().Add(TTL)
	ticket := Ticket{UserID: userID, ExecutionID: executionID, ExpiresAt: expiresAt}

	s.mu.Lock()
	bus := s.bus
	s.mu.Unlock()
	if bus.Enabled() {
		if raw, err := json.Marshal(ticket); err == nil {
			if err := bus.CacheSet(context.Background(), "wsticket:"+token, raw, TTL); err == nil {
				return token, expiresAt
			}
		}
	}

	s.mu.Lock()
	s.cleanupLocked()
	s.tickets[token] = ticket
	s.mu.Unlock()
	return token, expiresAt
}

// Consume returns the ticket and removes it, whether or not it was expired: a
// ticket is never valid twice.
func (s *Store) Consume(token string) (Ticket, bool) {
	s.mu.Lock()
	bus := s.bus
	s.mu.Unlock()
	if bus.Enabled() {
		raw, ok, err := bus.CacheGetDel(context.Background(), "wsticket:"+token)
		if err != nil || !ok {
			return Ticket{}, false
		}
		var ticket Ticket
		if json.Unmarshal(raw, &ticket) != nil || s.now().After(ticket.ExpiresAt) {
			return Ticket{}, false
		}
		return ticket, true
	}

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
