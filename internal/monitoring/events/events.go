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

// Event says that one engine's monitoring data changed.
type Event struct {
	EngineID string
	Kind     Kind
}

// Hub fans events out to this process's SSE handlers. A slow subscriber drops
// events instead of blocking the publisher; the client re-fetches on the next
// event and keeps a slow fallback poll.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan Event]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan Event]struct{})}
}

// Subscribe returns the engine's event stream and an unsubscribe function.
func (h *Hub) Subscribe(engineID string) (<-chan Event, func()) {
	ch := make(chan Event, 8)
	h.mu.Lock()
	if h.subs[engineID] == nil {
		h.subs[engineID] = make(map[chan Event]struct{})
	}
	h.subs[engineID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if set := h.subs[engineID]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(h.subs, engineID)
			}
		}
		h.mu.Unlock()
	}
}

// Publish notifies this process's subscribers of one engine's change.
func (h *Hub) Publish(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[ev.EngineID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Notify tells every API replica that an engine's monitoring data changed.
// Callers that write snapshots use it; a nil pool is a no-op (tests).
func Notify(ctx context.Context, pool *pgxpool.Pool, engineID string, kind Kind) error {
	if pool == nil || engineID == "" {
		return nil
	}
	_, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", Channel, engineID+":"+string(kind))
	return err
}

// Listen relays PostgreSQL notifications into the hub until ctx ends. It
// reconnects with a short backoff when the listening connection drops.
func Listen(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, hub *Hub) {
	for ctx.Err() == nil {
		err := listenOnce(ctx, pool, hub)
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

func listenOnce(ctx context.Context, pool *pgxpool.Pool, hub *Hub) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		engineID, kind, ok := ParsePayload(n.Payload)
		if ok {
			hub.Publish(Event{EngineID: engineID, Kind: kind})
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
