// Package export ships audit events to an OpenSearch/Elasticsearch-compatible
// `_bulk` endpoint (#50): at-least-once, ordered and resumable. It never
// modifies audit_events and never advances past a failed item, so a crash or a
// partial failure only re-sends (idempotent `_id`s make that safe).
package export

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Sink is the cursor row's name.
const Sink = "opensearch"

// DefaultSafetyLag keeps not-yet-committed rows (a concurrent transaction can
// insert an earlier occurred_at) out of the export.
const DefaultSafetyLag = 10 * time.Second

// Config is the exporter's configuration.
type Config struct {
	URL      string // base URL; empty disables the export
	Index    string // index prefix; the monthly index is <prefix>-YYYY.MM
	Auth     string // none | basic | bearer | api_key
	User     string
	Password string
	Token    string
	Batch    int
	Interval time.Duration
	CAFile   string
	Lag      time.Duration
}

// ConfigFromEnv reads the configuration; getenv is os.Getenv in production.
func ConfigFromEnv(getenv func(string) string) Config {
	c := Config{
		URL:      getenv("BALEIA_AUDIT_EXPORT_URL"),
		Index:    getenv("BALEIA_AUDIT_EXPORT_INDEX"),
		Auth:     getenv("BALEIA_AUDIT_EXPORT_AUTH"),
		User:     getenv("BALEIA_AUDIT_EXPORT_USER"),
		Password: getenv("BALEIA_AUDIT_EXPORT_PASSWORD"),
		Token:    getenv("BALEIA_AUDIT_EXPORT_TOKEN"),
		CAFile:   getenv("BALEIA_AUDIT_EXPORT_CA_FILE"),
	}
	if c.Index == "" {
		c.Index = "baleia-audit"
	}
	if c.Auth == "" {
		c.Auth = "none"
	}
	c.Batch = atoiDefault(getenv("BALEIA_AUDIT_EXPORT_BATCH"), 500)
	c.Interval = durationDefault(getenv("BALEIA_AUDIT_EXPORT_INTERVAL"), 30*time.Second)
	c.Lag = DefaultSafetyLag
	return c
}

// Enabled reports whether an export target is configured.
func (c Config) Enabled() bool { return c.URL != "" }

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

func durationDefault(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return def
}

// Event is one audit row to export.
type Event struct {
	OccurredAt   time.Time
	ID           int64
	ActorKind    string
	ActorID      string
	AuthMethod   string
	Action       string
	ResourceType string
	ResourceID   string
	WorkspaceID  string
	Outcome      string
	Reason       string
	RequestID    string
	TraceID      string
	ClientIP     string
	UserAgent    string
	Changes      json.RawMessage
}

// Position is a cursor value: the (occurred_at, id) of the last accepted event.
type Position struct {
	OccurredAt time.Time
	ID         int64
}

// After reports whether p is strictly after the zero position.
func (p Position) After() bool { return !p.OccurredAt.IsZero() || p.ID != 0 }

// DocumentID is the deterministic `_id` of an event: re-sending a batch
// overwrites the same document instead of duplicating it.
func DocumentID(e Event) string {
	return strconv.FormatInt(e.OccurredAt.UnixNano(), 10) + "-" + strconv.FormatInt(e.ID, 10)
}

// Document maps an event to its OpenSearch document. changes is copied as
// stored: secret values were already redacted at write time (#45).
func Document(e Event) map[string]any {
	doc := map[string]any{
		"@timestamp": e.OccurredAt.UTC().Format(time.RFC3339Nano),
		"event":      map[string]any{"action": e.Action, "outcome": e.Outcome},
		"actor":      map[string]any{"kind": e.ActorKind},
		"resource":   map[string]any{"type": e.ResourceType},
		"baleia":     map[string]any{"sink": Sink},
	}
	if e.Reason != "" {
		doc["event"].(map[string]any)["reason"] = e.Reason
	}
	if e.ActorID != "" {
		doc["actor"].(map[string]any)["id"] = e.ActorID
	}
	if e.AuthMethod != "" {
		doc["actor"].(map[string]any)["auth_method"] = e.AuthMethod
	}
	if e.ResourceID != "" {
		doc["resource"].(map[string]any)["id"] = e.ResourceID
	}
	if e.WorkspaceID != "" {
		doc["workspace_id"] = e.WorkspaceID
	}
	if e.RequestID != "" || e.TraceID != "" {
		tr := map[string]any{}
		if e.RequestID != "" {
			tr["request_id"] = e.RequestID
		}
		if e.TraceID != "" {
			tr["trace_id"] = e.TraceID
		}
		doc["trace"] = tr
	}
	if e.ClientIP != "" || e.UserAgent != "" {
		cl := map[string]any{}
		if e.ClientIP != "" {
			cl["ip"] = e.ClientIP
		}
		if e.UserAgent != "" {
			cl["user_agent"] = e.UserAgent
		}
		doc["client"] = cl
	}
	if len(e.Changes) > 0 {
		var changes any
		if json.Unmarshal(e.Changes, &changes) == nil {
			doc["changes"] = changes
		}
	}
	return doc
}

// Source reads a batch of events.
type Source interface {
	// Events returns up to limit events with (occurred_at, id) strictly after
	// the position and occurred_at before before, ordered by (occurred_at, id).
	Events(ctx context.Context, after Position, before time.Time, limit int) ([]Event, error)
}

// Cursor persists the last accepted position per sink.
type Cursor interface {
	Get(ctx context.Context, sink string) (Position, error)
	Set(ctx context.Context, sink string, p Position) error
}

// BulkSink writes documents. Bulk returns the number of leading documents the
// server accepted (contiguous), so the caller never advances past a failure.
type BulkSink interface {
	Bulk(ctx context.Context, docs []map[string]any, ids []string, index string) (accepted int, err error)
}

// Exporter runs one pass of the export.
type Exporter struct {
	Source Source
	Cursor Cursor
	Sink   BulkSink
	Index  string // prefix; the monthly suffix is added per event
	Batch  int
	Lag    time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// MaxPass bounds one pass; 0 means 5 minutes.
	MaxPass time.Duration
	// OnBatch observes each batch's outcome ("accepted"/"partial"/"error") and
	// the document count.
	OnBatch func(outcome string, n int)
	// OnLag reports the cursor's occurred_at after each advance, so the caller
	// can record the export lag.
	OnLag func(occurredAt time.Time)
}

// Run exports until caught up or the pass bound elapses. It returns the number
// of events accepted this pass.
func (e *Exporter) Run(ctx context.Context) (int, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	pass := e.MaxPass
	if pass <= 0 {
		pass = 5 * time.Minute
	}
	batch := e.Batch
	if batch <= 0 {
		batch = 500
	}
	lag := e.Lag
	if lag < 0 {
		lag = DefaultSafetyLag
	}
	deadline := now().Add(pass)

	pos, err := e.Cursor.Get(ctx, Sink)
	if err != nil {
		return 0, err
	}
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		if now().After(deadline) {
			return total, nil
		}
		before := now().Add(-lag)
		events, err := e.Source.Events(ctx, pos, before, batch)
		if err != nil {
			return total, err
		}
		if len(events) == 0 {
			return total, nil
		}
		docs := make([]map[string]any, len(events))
		ids := make([]string, len(events))
		index := e.Index
		for i, ev := range events {
			docs[i] = Document(ev)
			ids[i] = DocumentID(ev)
		}
		accepted, err := e.Sink.Bulk(ctx, docs, ids, monthlyIndex(index, events[0].OccurredAt))
		if accepted > 0 {
			last := events[accepted-1]
			newPos := Position{OccurredAt: last.OccurredAt, ID: last.ID}
			if err := e.Cursor.Set(ctx, Sink, newPos); err != nil {
				return total, err
			}
			pos = newPos
			total += accepted
			if e.OnLag != nil {
				e.OnLag(pos.OccurredAt)
			}
		}
		switch {
		case err != nil:
			e.observe("error", accepted)
			return total, err
		case accepted < len(events):
			e.observe("partial", accepted)
			return total, nil // a failed item keeps the cursor; the next pass retries
		}
		e.observe("accepted", accepted)
		if len(events) < batch {
			return total, nil
		}
	}
}

func (e *Exporter) observe(outcome string, n int) {
	if e.OnBatch != nil {
		e.OnBatch(outcome, n)
	}
}

// monthlyIndex appends the event's month to the prefix.
func monthlyIndex(prefix string, t time.Time) string {
	if prefix == "" {
		prefix = "baleia-audit"
	}
	return prefix + "-" + t.UTC().Format("2006.01")
}

// ─────────────────────────────────────────────────────────────────────────
// HTTP bulk sink
// ─────────────────────────────────────────────────────────────────────────

// HTTPBulk is the `_bulk`-over-HTTP sink. No SDK: a POST per batch.
type HTTPBulk struct {
	url   string
	auth  string
	user  string
	pass  string
	token string
	http  *http.Client
}

// NewHTTPBulk builds the sink. hc may be nil; a client is built from the config
// (CA file honoured).
func NewHTTPBulk(c Config, hc *http.Client) (*HTTPBulk, error) {
	if c.URL == "" {
		return nil, errors.New("audit export: url is required")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	if c.CAFile != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("audit export ca file: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("audit export ca file: no certificates")
		}
		hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	}
	return &HTTPBulk{url: c.URL, auth: c.Auth, user: c.User, pass: c.Password, token: c.Token, http: hc}, nil
}

type bulkItem struct {
	Index struct {
		ID    string `json:"_id"`
		Index string `json:"_index"`
	} `json:"index"`
}

type bulkResponse struct {
	Errors bool `json:"errors"`
	Items  []map[string]struct {
		Status int `json:"status"`
		Error  *struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"error"`
	} `json:"items"`
}

// Bulk sends the batch and reports how many leading documents were accepted.
func (s *HTTPBulk) Bulk(ctx context.Context, docs []map[string]any, ids []string, index string) (int, error) {
	if len(docs) == 0 {
		return 0, nil
	}
	var buf bytes.Buffer
	for i, d := range docs {
		meta, _ := json.Marshal(bulkItem{Index: struct {
			ID    string `json:"_id"`
			Index string `json:"_index"`
		}{ID: ids[i], Index: index}})
		buf.Write(meta)
		buf.WriteByte('\n')
		body, _ := json.Marshal(d)
		buf.Write(body)
		buf.WriteByte('\n')
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url+"/_bulk", &buf)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	s.authorize(req)
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return 0, fmt.Errorf("audit export: bulk status %d", resp.StatusCode)
	}
	var out bulkResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("audit export: decode bulk: %w", err)
	}
	if !out.Errors {
		return len(docs), nil
	}
	// Partial: the leading items with status < 300 were accepted.
	for i := 0; i < len(docs); i++ {
		if i >= len(out.Items) {
			return i, nil
		}
		item := out.Items[i]
		ok := true
		for _, v := range item {
			if v.Status >= 300 {
				ok = false
			}
		}
		if !ok {
			return i, nil
		}
	}
	return len(docs), nil
}

func (s *HTTPBulk) authorize(req *http.Request) {
	switch s.auth {
	case "basic":
		req.SetBasicAuth(s.user, s.pass)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+s.token)
	case "api_key":
		req.Header.Set("Authorization", "ApiKey "+s.token)
	}
}
