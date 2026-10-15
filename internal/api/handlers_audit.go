package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	defaultAuditPage = 50
	maxAuditPage     = 200
)

// ListAuditEvents — GET /audit/events (#45). A global admin sees every
// event; anyone else only the events they performed.
func (h *Handlers) ListAuditEvents(ctx context.Context, request ListAuditEventsRequestObject) (ListAuditEventsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListAuditEvents401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	q := request.Params
	limit := defaultAuditPage
	if q.Limit != nil {
		if *q.Limit < 1 || *q.Limit > maxAuditPage {
			return ListAuditEvents422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "limit must be between 1 and 200")}, nil
		}
		limit = *q.Limit
	}
	params := db.ListAuditEventsParams{
		ResourceType: q.ResourceType, ResourceID: q.ResourceId, ActorID: q.ActorId, Action: q.Action,
		Lim: int32(limit + 1),
	}
	if q.Outcome != nil {
		o := string(*q.Outcome)
		params.Outcome = &o
	}
	if q.From != nil {
		params.FromTime = pgtype.Timestamptz{Time: *q.From, Valid: true}
	}
	if q.To != nil {
		params.ToTime = pgtype.Timestamptz{Time: *q.To, Valid: true}
	}
	if !p.IsAdmin() {
		self := p.UserID.String()
		params.ActorID = &self
	}
	if q.Cursor != nil && *q.Cursor != "" {
		at, id, ok := decodeAuditCursor(*q.Cursor)
		if !ok {
			return ListAuditEvents422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "invalid cursor")}, nil
		}
		params.CursorAt, params.CursorID = pgtype.Timestamptz{Time: at, Valid: true}, &id
	}
	rows, err := h.queries.ListAuditEvents(ctx, params)
	if err != nil {
		return nil, err
	}
	out := ListAuditEvents200JSONResponse{Items: make([]AuditEvent, 0, len(rows))}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		c := encodeAuditCursor(last.OccurredAt.Time, last.ID)
		out.NextCursor = &c
	}
	for _, r := range rows {
		e := AuditEvent{
			Id: r.ID, OccurredAt: r.OccurredAt.Time, ActorKind: AuditEventActorKind(r.ActorKind), ActorId: r.ActorID,
			AuthMethod: r.AuthMethod, Action: r.Action, ResourceType: r.ResourceType, ResourceId: r.ResourceID,
			Outcome: AuditEventOutcome(r.Outcome), Reason: r.Reason, RequestId: r.RequestID, TraceId: r.TraceID,
			UserAgent: r.UserAgent,
		}
		if r.WorkspaceID.Valid {
			ws := openapi_types.UUID(r.WorkspaceID.Bytes)
			e.WorkspaceId = &ws
		}
		if r.ClientIp != nil {
			ip := r.ClientIp.String()
			e.ClientIp = &ip
		}
		if len(r.Changes) > 0 {
			if err := json.Unmarshal(r.Changes, &e.Changes); err != nil {
				return nil, err
			}
		}
		out.Items = append(out.Items, e)
	}
	return out, nil
}

// The cursor is the (occurred_at, id) of the last event of a page.
func encodeAuditCursor(at time.Time, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(id, 10)))
}

func decodeAuditCursor(c string) (time.Time, int64, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, 0, false
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, 0, false
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, 0, false
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return time.Time{}, 0, false
	}
	return at, id, true
}
