package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring/events"
	"github.com/google/uuid"
)

// StreamMonitoringEvents streams a small "changed" signal when an engine's
// monitoring data changes (#733). The payload is not the data: the client
// re-fetches its filtered list on each event. Changes are written by the worker
// (Lens pollers) and the API (Trino event ingester); the hub already carries
// the cross-process hop (see internal/monitoring/events).
func (h *OpsHandlers) StreamMonitoringEvents(ctx context.Context, request StreamMonitoringEventsRequestObject) (StreamMonitoringEventsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return StreamMonitoringEvents401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	engineID := db.UUID{Bytes: request.Id, Valid: true}
	if _, ok := h.engineForMonitoring(ctx, p, engineID, authz.ActRead); !ok {
		return StreamMonitoringEvents404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "engine not found"}}, nil
	}
	stream := &monitoringSSE{ctx: ctx, ping: time.NewTicker(15 * time.Second)}
	if h.monitoringHub != nil {
		stream.sub, stream.unsubscribe = h.monitoringHub.Subscribe(uuid.UUID(engineID.Bytes).String())
	}
	// Revalidate access on the long-lived stream: a revoked grant closes it
	// instead of keeping the signal flowing (#733).
	stream.valid = func() bool {
		_, ok := h.engineForMonitoring(ctx, p, engineID, authz.ActRead)
		return ok
	}
	return StreamMonitoringEvents200TexteventStreamResponse{Body: stream}, nil
}

// monitoringSSE is the SSE body. The generated Visit uses io.Copy, so WriteTo
// flushes each frame through http.Flusher instead of buffering it.
type monitoringSSE struct {
	ctx         context.Context
	sub         *events.Subscription
	unsubscribe func()
	valid       func() bool
	ping        *time.Ticker
}

func (s *monitoringSSE) Read([]byte) (int, error) { return 0, io.EOF }

func (s *monitoringSSE) Close() error {
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
	if s.ping != nil {
		s.ping.Stop()
	}
	return nil
}

func (s *monitoringSSE) allowed() bool {
	return s.valid == nil || s.valid()
}

func (s *monitoringSSE) WriteTo(w io.Writer) (int64, error) {
	var written int64
	flusher, _ := w.(http.Flusher)
	write := func(frame string) error {
		n, err := io.WriteString(w, frame)
		written += int64(n)
		if flusher != nil {
			flusher.Flush()
		}
		return err
	}
	// Tell the client the stream is open before the first change.
	if err := write(": connected\n\n"); err != nil {
		return written, err
	}
	var wake <-chan struct{}
	if s.sub != nil {
		wake = s.sub.Wake()
	}
	for {
		// Flush pending kinds first so a wake is never lost.
		if s.sub != nil {
			for _, kind := range s.sub.Take() {
				if !s.allowed() {
					return written, nil
				}
				payload, _ := json.Marshal(map[string]string{"engine_id": s.sub.EngineID(), "kind": string(kind)})
				if err := write("event: changed\ndata: " + string(payload) + "\n\n"); err != nil {
					return written, err
				}
			}
		}
		select {
		case <-s.ctx.Done():
			return written, nil
		case <-s.ping.C:
			// Revalidate even when no change arrives, so a revoked grant does
			// not keep an idle stream open.
			if !s.allowed() {
				return written, nil
			}
			if err := write(": ping\n\n"); err != nil {
				return written, err
			}
		case <-wake:
		}
	}
}
