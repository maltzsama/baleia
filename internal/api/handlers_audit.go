package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
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

// audited records one audited operation (#43). It never fails the request:
// the recorder logs and counts a failed write.
func (h *Handlers) audited(ctx context.Context, e audit.Event) {
	if h.audit == nil {
		return
	}
	_ = h.audit.Record(ctx, e)
}

// idString renders a resource id for the audit trail.
func idString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

// testOutcome is a connection test's event: the probe ran (allowed) or the
// source could not be reached with the stored settings.
func testOutcome(ev audit.Event, ok bool) audit.Event {
	if ok {
		return ev.Allowed()
	}
	return ev.Failed(audit.ReasonEngineError)
}

// connectionChanges lists what an update changed: name, read-only default,
// URL and config values as they are; credentials only by key, as "***".
func connectionChanges(before, after db.CatalogConnection, creds *map[string]interface{}) map[string]audit.Change {
	changes := map[string]audit.Change{}
	if before.Name != after.Name {
		changes["name"] = audit.Change{From: before.Name, To: after.Name}
	}
	if before.DefaultReadonly != after.DefaultReadonly {
		changes["default_readonly"] = audit.Change{From: before.DefaultReadonly, To: after.DefaultReadonly}
	}
	if before.CredentialsReadOnly != after.CredentialsReadOnly {
		changes["credentials_read_only"] = audit.Change{From: before.CredentialsReadOnly, To: after.CredentialsReadOnly}
	}
	if before.Url != after.Url {
		changes["url"] = audit.Change{From: before.Url, To: after.Url}
	}
	var was, is map[string]any
	_ = json.Unmarshal(before.Config, &was)
	_ = json.Unmarshal(after.Config, &is)
	for k, v := range is {
		if old, ok := was[k]; !ok || fmt.Sprint(old) != fmt.Sprint(v) {
			changes["config."+k] = audit.Change{From: was[k], To: v}
		}
	}
	for k := range was {
		if _, ok := is[k]; !ok {
			changes["config."+k] = audit.Change{From: was[k]}
		}
	}
	if creds != nil {
		for k := range *creds {
			changes["credentials."+k] = audit.Change{From: "***", To: "***"}
		}
	}
	if len(changes) == 0 {
		return nil
	}
	return changes
}

// withID returns ev about the resource id.
func withID(ev audit.Event, id pgtype.UUID) audit.Event {
	ev.ResourceID = idString(id)
	return ev
}

// catalogChanges lists what a catalog update changes.
func catalogChanges(c db.Catalog, name, trinoName, warehouse, connector string, extraProps []byte) map[string]audit.Change {
	changes := map[string]audit.Change{}
	add := func(k string, from, to string) {
		if from != to {
			changes[k] = audit.Change{From: from, To: to}
		}
	}
	add("name", c.Name, name)
	add("trino_catalog_name", c.TrinoName(), trinoName)
	add("warehouse", c.WarehouseName(), warehouse)
	add("connector", c.ConnectorName(), connector)
	// Extra properties may carry engine secrets: only their keys are kept.
	if string(c.ExtraProps) != string(extraProps) {
		changes["extra_props"] = audit.Change{From: propKeys(c.ExtraProps), To: propKeys(extraProps)}
	}
	if len(changes) == 0 {
		return nil
	}
	return changes
}

// propKeys lists the keys of a JSON object, sorted.
func propKeys(raw []byte) []string {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// workspaceChanges lists what a workspace update changed.
func workspaceChanges(before, after db.Workspace, targetChanged bool) map[string]audit.Change {
	changes := map[string]audit.Change{}
	if before.Name != after.Name {
		changes["name"] = audit.Change{From: before.Name, To: after.Name}
	}
	if fmt.Sprint(deref(before.Description)) != fmt.Sprint(deref(after.Description)) {
		changes["description"] = audit.Change{From: deref(before.Description), To: deref(after.Description)}
	}
	if targetChanged {
		changes["default_binding_id"] = audit.Change{From: idString(before.DefaultBindingID), To: idString(after.DefaultBindingID)}
	}
	if len(changes) == 0 {
		return nil
	}
	return changes
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
