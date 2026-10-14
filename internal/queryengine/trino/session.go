package trino

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/arrowipc"
	"github.com/galileostudio/baleia/internal/queryengine"
	trinoclient "github.com/galileostudio/baleia/internal/trino"
)

// session is client-side state: what the next request must send.
type session struct {
	engine     *Engine
	spec       queryengine.SessionSpec
	mu         sync.Mutex
	catalog    string
	schema     string
	properties map[string]string
	prepared   map[string]string
}

func newSession(e *Engine, spec queryengine.SessionSpec) *session {
	s := &session{
		engine:     e,
		spec:       spec,
		catalog:    spec.Catalog,
		schema:     strings.Join(spec.Schema, "."),
		properties: map[string]string{},
		prepared:   map[string]string{},
	}
	if spec.Timeout > 0 {
		// Trino enforces it too (#84).
		s.properties["query_max_execution_time"] = fmt.Sprintf("%ds", int64(spec.Timeout/time.Second))
	}
	return s
}

func (s *session) Close() error { return nil }

// options renders the session for one request.
func (s *session) options(cb queryengine.Callbacks, onHeaders func(http.Header)) trinoclient.ExecuteOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := trinoclient.ExecuteOptions{
		Catalog:            s.catalog,
		Schema:             s.schema,
		User:               s.spec.Identity.UserID, // empty: the service identity
		SessionProperties:  copyMap(s.properties),
		PreparedStatements: copyMap(s.prepared),
		Source:             "baleia",
		ClientTags:         []string{"baleia"},
		TraceToken:         s.spec.Tags.RequestID,
		OnHeaders:          onHeaders,
	}
	if s.spec.Tags.Source != "" {
		o.ClientTags = append(o.ClientTags, s.spec.Tags.Source)
	}
	if s.spec.Tags.ExecutionID != "" {
		info, _ := json.Marshal(map[string]string{"execution_id": s.spec.Tags.ExecutionID})
		o.ClientInfo = string(info)
	}
	if s.spec.ReadOnly {
		o.ExtraCredentials = map[string]string{"baleia.mode": "readonly"}
	}
	if cb.OnProgress != nil {
		o.OnProgress = func() { cb.OnProgress(queryengine.Progress{State: "RUNNING"}) }
	}
	return o
}

func copyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// applyHeaders updates the session from a response. Identity is never taken
// from the engine (#92): X-Trino-Set-Authorization-User is ignored.
func (s *session) applyHeaders(h http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := h.Get("X-Trino-Set-Catalog"); v != "" {
		s.catalog = v
	}
	if v := h.Get("X-Trino-Set-Schema"); v != "" {
		s.schema = v
	}
	for _, kv := range splitHeader(h.Values("X-Trino-Set-Session")) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			s.properties[strings.TrimSpace(k)] = unescape(v)
		}
	}
	for _, k := range splitHeader(h.Values("X-Trino-Clear-Session")) {
		delete(s.properties, strings.TrimSpace(k))
	}
	for _, kv := range splitHeader(h.Values("X-Trino-Added-Prepare")) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			s.prepared[strings.TrimSpace(k)] = unescape(v)
		}
	}
	for _, k := range splitHeader(h.Values("X-Trino-Deallocated-Prepare")) {
		delete(s.prepared, strings.TrimSpace(k))
	}
	if h.Get("X-Trino-Set-Authorization-User") != "" {
		slog.Warn("trino asked to change the session user; ignored", "engine", s.engine.id)
	}
}

func splitHeader(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func unescape(v string) string {
	if u, err := url.QueryUnescape(strings.TrimSpace(v)); err == nil {
		return u
	}
	return strings.TrimSpace(v)
}

// Execute runs one statement and streams it to sink as Arrow batches.
func (s *session) Execute(ctx context.Context, sql string, cb queryengine.Callbacks, sink queryengine.RecordSink) error {
	start := time.Now()
	var (
		batcher *arrowipc.Batcher
		began   bool
		sinkErr error
	)
	opts := s.options(cb, s.applyHeaders)
	opts.OnQueryID = func(id string) {
		if cb.OnStart != nil {
			cb.OnStart(queryengine.QueryHandle{EngineID: s.engine.id, QueryID: id})
		}
	}
	defer func() {
		if batcher != nil {
			batcher.Release()
		}
	}()
	emit := func(rec arrow.RecordBatch) error {
		if err := sink.Write(rec); err != nil {
			sinkErr = err
			return err
		}
		return nil
	}

	_, err := s.engine.client.ExecuteStream(ctx, sql, opts, func(cols []trinoclient.Column, rows [][]interface{}) error {
		if batcher == nil && len(cols) > 0 {
			infos := make([]arrowipc.ColumnInfo, len(cols))
			for i, c := range cols {
				infos[i] = arrowipc.ColumnInfo{Name: c.Name, Type: c.Type}
			}
			b, err := arrowipc.NewBatcher(infos)
			if err != nil {
				return err
			}
			batcher = b
			if err := sink.Begin(b.Schema()); err != nil {
				sinkErr = err
				return err
			}
			began = true
		}
		if len(rows) == 0 {
			return nil
		}
		if batcher == nil {
			return fmt.Errorf("trino sent rows before columns")
		}
		return batcher.Append(rows, emit)
	})
	if sinkErr != nil {
		return sinkErr
	}
	if err != nil {
		return mapError(err)
	}
	if !began {
		// Statements without columns (SET, USE, DDL) still produce one result set.
		if err := sink.Begin(arrow.NewSchema(nil, nil)); err != nil {
			return err
		}
		return sink.End(queryengine.Stats{ElapsedMs: time.Since(start).Milliseconds()})
	}
	if err := batcher.Flush(emit); err != nil {
		if sinkErr != nil {
			return sinkErr
		}
		return err
	}
	return sink.End(queryengine.Stats{RowCount: batcher.RowCount(), ElapsedMs: time.Since(start).Milliseconds()})
}
