// Package events fans monitoring changes out to connected SSE clients (#733).
//
// Snapshots are written by two processes: the worker (the Lens pollers) and the
// API (the Trino event ingester). The API serves the SSE endpoint, so a change
// written by the worker must reach it somehow. PostgreSQL LISTEN/NOTIFY is the
// cross-process hop: every writer NOTIFYs, every API replica LISTENs, and each
// replica relays the event to its own subscribers. No extra infrastructure.
package events

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Channel is the PostgreSQL notification channel carrying monitoring changes.
const Channel = "baleia_monitoring"

// Kind is the monitoring dataset that changed.
type Kind string

const (
	KindQueries Kind = "queries"
	KindCluster Kind = "cluster"
)

// Subscription is one SSE client's pending kinds. Publishing marks a kind and
// wakes the reader; kinds coalesce, so a burst of query events cannot drop the
// signal for a cluster change, and a slow reader never blocks the publisher.
type Subscription struct {
	engineID string
	mu       sync.Mutex
	pending  map[Kind]struct{}
	wake     chan struct{}
}

func newSubscription(engineID string) *Subscription {
	return &Subscription{engineID: engineID, pending: make(map[Kind]struct{}), wake: make(chan struct{}, 1)}
}

// EngineID is the engine this subscription watches.
func (s *Subscription) EngineID() string { return s.engineID }

// Wake fires when at least one kind is pending. Drain with Take.
func (s *Subscription) Wake() <-chan struct{} { return s.wake }

// Take returns the pending kinds and clears them.
func (s *Subscription) Take() []Kind {
	s.mu.Lock()
	kinds := make([]Kind, 0, len(s.pending))
	for k := range s.pending {
		kinds = append(kinds, k)
	}
	s.pending = make(map[Kind]struct{})
	s.mu.Unlock()
	return kinds
}

func (s *Subscription) mark(kind Kind) {
	s.mu.Lock()
	s.pending[kind] = struct{}{}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Hub fans events out to this process's SSE handlers.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[*Subscription]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[*Subscription]struct{})}
}

// Subscribe returns a subscription for the engine and an unsubscribe function.
func (h *Hub) Subscribe(engineID string) (*Subscription, func()) {
	sub := newSubscription(engineID)
	h.mu.Lock()
	if h.subs[engineID] == nil {
		h.subs[engineID] = make(map[*Subscription]struct{})
	}
	h.subs[engineID][sub] = struct{}{}
	h.mu.Unlock()
	return sub, func() {
		h.mu.Lock()
		if set := h.subs[engineID]; set != nil {
			delete(set, sub)
			if len(set) == 0 {
				delete(h.subs, engineID)
			}
		}
		h.mu.Unlock()
	}
}

// Publish marks one engine's change for every subscriber in this process.
func (h *Hub) Publish(engineID string, kind Kind) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs[engineID] {
		sub.mark(kind)
	}
}

// Resync marks both kinds for every subscribed engine. The listener calls it
// after a reconnection so clients re-fetch what they missed while it was down.
func (h *Hub) Resync() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, set := range h.subs {
		for sub := range set {
			sub.mark(KindQueries)
			sub.mark(KindCluster)
		}
	}
}

// Notify tells every API replica that an engine's monitoring data changed.
// Callers that write snapshots use it; a nil pool or engine is a no-op (tests).
func Notify(ctx context.Context, pool *pgxpool.Pool, engineID string, kind Kind) error {
	if pool == nil || engineID == "" {
		return nil
	}
	_, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", Channel, engineID+":"+string(kind))
	return err
}

// Listen relays PostgreSQL notifications into the hub until ctx ends. It
// reconnects with a short backoff when the listening connection drops, and
// resyncs subscribers after a reconnection.
func Listen(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, hub *Hub) {
	connectedOnce := false
	for ctx.Err() == nil {
		err := listenOnce(ctx, pool, hub, func() {
			if connectedOnce {
				hub.Resync()
			}
			connectedOnce = true
		})
		if ctx.Err() != nil {
			return
		}
		if logger != nil {
			logger.WarnContext(ctx, "monitoring events: LISTEN dropped, reconnecting", "err", err)
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, hub *Hub, onConnected func()) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	onConnected()
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		engineID, kind, ok := ParsePayload(n.Payload)
		if ok {
			hub.Publish(engineID, kind)
		}
	}
}

// ParsePayload splits an "<engine_id>:<kind>" notification payload.
func ParsePayload(payload string) (string, Kind, bool) {
	engineID, kind, ok := strings.Cut(payload, ":")
	if !ok || engineID == "" {
		return "", "", false
	}
	switch Kind(kind) {
	case KindQueries, KindCluster:
		return engineID, Kind(kind), true
	default:
		return "", "", false
	}
}
