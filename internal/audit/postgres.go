package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// writeTimeout bounds one audit write: Record never blocks its caller longer.
const writeTimeout = 200 * time.Millisecond

// Inserter is the slice of the database the recorder writes through.
type Inserter interface {
	InsertAuditEvent(ctx context.Context, arg db.InsertAuditEventParams) error
}

// Postgres records events into audit_events.
type Postgres struct {
	q       Inserter
	secret  map[string]bool
	failed  func()
	timeout time.Duration
}

// NewPostgres returns a recorder. secretKeys are field names whose values are
// redacted (the connection providers' secret fields); onFailure counts failed
// writes (metrics.Audit.WriteFailed), nil for none.
func NewPostgres(q Inserter, secretKeys []string, onFailure func()) *Postgres {
	s := make(map[string]bool, len(secretKeys))
	for _, k := range secretKeys {
		s[k] = true
	}
	return &Postgres{q: q, secret: s, failed: onFailure, timeout: writeTimeout}
}

// Record writes e with the actor, auth method, request id and client found
// in ctx. It never blocks for more than writeTimeout and outlives a
// cancelled request; a failure is logged and counted.
func (p *Postgres) Record(ctx context.Context, e Event) error {
	err := p.record(ctx, e)
	if err != nil {
		slog.Error("audit write failed", "error_code", "audit_write_failed", "action", string(e.Action), "err", err)
		if p.failed != nil {
			p.failed()
		}
	}
	return err
}

func (p *Postgres) record(ctx context.Context, e Event) error {
	if !Known(e.Action) {
		return fmt.Errorf("unknown audit action %q", e.Action)
	}
	kind, actorID, method := actor(ctx)
	ri, requestID := requestOf(ctx)
	row := db.InsertAuditEventParams{
		ActorKind:    kind,
		ActorID:      optional(actorID),
		AuthMethod:   optional(method),
		Action:       string(e.Action),
		ResourceType: e.ResourceType,
		ResourceID:   optional(e.ResourceID),
		Outcome:      string(e.Outcome),
		Reason:       optional(e.Reason),
		RequestID:    optional(requestID),
		UserAgent:    optional(ri.UserAgent),
	}
	if ri.ClientIP.IsValid() {
		ip := ri.ClientIP
		row.ClientIp = &ip
	}
	if e.WorkspaceID != "" {
		id, err := uuid.Parse(e.WorkspaceID)
		if err != nil {
			return fmt.Errorf("audit workspace id: %w", err)
		}
		row.WorkspaceID = pgtype.UUID{Bytes: id, Valid: true}
	}
	if changes := redact(e.Changes, p.secret); changes != nil {
		raw, err := json.Marshal(changes)
		if err != nil {
			return err
		}
		row.Changes = raw
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.timeout)
	defer cancel()
	return p.q.InsertAuditEvent(wctx, row)
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
