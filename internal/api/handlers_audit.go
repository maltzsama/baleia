package api

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
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
func (h *OpsHandlers) ListAuditEvents(ctx context.Context, request ListAuditEventsRequestObject) (ListAuditEventsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListAuditEvents401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	q := request.Params
	limit := defaultAuditPage
	if q.Limit != nil {
		if *q.Limit < 1 || *q.Limit > maxAuditPage {
			return ListAuditEvents422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "limit must be between 1 and 200")}, nil
		}
		limit = *q.Limit
	}
	if w := auditWindow(q.From, q.To, q.ResourceId, q.ActorId); w != nil {
		return ListAuditEvents422JSONResponse{UnprocessableEntityJSONResponse: *w}, nil
	}
	params := db.ListAuditEventsParams{
		ResourceType: q.ResourceType, ResourceID: q.ResourceId, ActorID: q.ActorId, Action: q.Action,
		RequestID: q.RequestId, TraceID: q.TraceId,
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
		// Own events plus events of workspaces the caller owns (#49).
		self := p.UserID.String()
		params.ScopeActor = &self
		params.ScopeOwner = p.UserID
	}
	if q.Cursor != nil && *q.Cursor != "" {
		at, id, ok := decodeAuditCursor(*q.Cursor)
		if !ok {
			return ListAuditEvents422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "invalid cursor")}, nil
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

// auditWindow enforces the required, bounded time window (#49): a from date
// unless resource_id/actor_id narrows the search, and at most 93 days.
func auditWindow(from, to *time.Time, resourceID, actorID *string) *UnprocessableEntityJSONResponse {
	hasResource := resourceID != nil && *resourceID != ""
	hasActor := actorID != nil && *actorID != ""
	if from == nil && !hasResource && !hasActor {
		r := unprocessable(ErrorCodeInvalidTimeRange, "a time window (from) is required unless resource_id or actor_id is given")
		return &r
	}
	if from != nil && to != nil && to.Sub(*from) > 93*24*time.Hour {
		r := unprocessable(ErrorCodeInvalidTimeRange, "the window is larger than 93 days")
		return &r
	}
	return nil
}

// ListAuditActions returns the bounded vocabulary for the action filter.
func (h *OpsHandlers) ListAuditActions(ctx context.Context, _ ListAuditActionsRequestObject) (ListAuditActionsResponseObject, error) {
	if _, err := authz.PrincipalFromContext(ctx); err != nil {
		return ListAuditActions401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	actions := audit.Actions()
	out := make([]string, len(actions))
	for i, action := range actions {
		out[i] = string(action)
	}
	return ListAuditActions200JSONResponse(out), nil
}

// ResolveAudit resolves ids to names, only for entities the caller can see.
func (h *OpsHandlers) ResolveAudit(ctx context.Context, request ResolveAuditRequestObject) (ResolveAuditResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ResolveAudit401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	users := map[string]AuditResolvedUser{}
	workspaces := map[string]AuditResolvedName{}
	connections := map[string]AuditResolvedName{}
	catalogs := map[string]AuditResolvedName{}

	if request.Body != nil {
		for _, id := range *request.Body.Users {
			user, err := h.queries.GetUserByID(ctx, pgtype.UUID{Bytes: [16]byte(id), Valid: true})
			if err != nil {
				continue
			}
			email := user.Email
			users[uuid.UUID(id).String()] = AuditResolvedUser{Name: user.Name, Email: &email}
		}
		for _, id := range *request.Body.Workspaces {
			ws, err := h.authz().Workspace(ctx, p, pgtype.UUID{Bytes: [16]byte(id), Valid: true}, authz.ActRead)
			if err != nil {
				continue
			}
			workspaces[uuid.UUID(id).String()] = AuditResolvedName{Name: ws.Name}
		}
		for _, id := range *request.Body.Connections {
			conn, err := h.authz().Connection(ctx, p, pgtype.UUID{Bytes: [16]byte(id), Valid: true}, authz.ActRead)
			if err != nil {
				continue
			}
			connections[uuid.UUID(id).String()] = AuditResolvedName{Name: conn.Name}
		}
		for _, id := range *request.Body.Catalogs {
			catalog, err := h.queries.GetCatalogByID(ctx, pgtype.UUID{Bytes: [16]byte(id), Valid: true})
			if err != nil {
				continue
			}
			if _, err := h.authz().Connection(ctx, p, catalog.ConnectionID, authz.ActRead); err != nil {
				continue
			}
			catalogs[uuid.UUID(id).String()] = AuditResolvedName{Name: catalog.Name}
		}
	}
	return ResolveAudit200JSONResponse(AuditResolveResult{
		Users: &users, Workspaces: &workspaces, Connections: &connections, Catalogs: &catalogs,
	}), nil
}

// ExportAuditCSV streams the current filter as CSV, capped at one million rows.
func (h *OpsHandlers) ExportAuditCSV(ctx context.Context, request ExportAuditCSVRequestObject) (ExportAuditCSVResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ExportAuditCSV401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if !p.IsAdmin() {
		return ExportAuditCSV403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only administrators may export the audit trail"}}, nil
	}
	q := request.Params
	if w := auditWindow(q.From, q.To, q.ResourceId, q.ActorId); w != nil {
		return ExportAuditCSV422JSONResponse{UnprocessableEntityJSONResponse: *w}, nil
	}
	params := db.ListAuditEventsParams{
		ResourceType: q.ResourceType, ResourceID: q.ResourceId, ActorID: q.ActorId, Action: q.Action,
		RequestID: q.RequestId, TraceID: q.TraceId, Lim: 1000,
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

	// The request context ends when the client goes away, and a failed
	// write (the reader was closed) stops the producer too (#371).
	// CloseWithError always returns nil.
	reader, writer := io.Pipe()
	go func() {
		_ = writer.CloseWithError(h.writeAuditCSV(ctx, writer, params))
	}()
	return ExportAuditCSV200TextcsvResponse{Body: reader}, nil
}

// maxAuditExportRows bounds one CSV export.
const maxAuditExportRows = 1_000_000

// writeAuditCSV pages through the events matching params into w. It returns
// the first query, write or context error, so nothing keeps querying once the
// client stops reading.
func (h *core) writeAuditCSV(ctx context.Context, w io.Writer, params db.ListAuditEventsParams) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"occurred_at", "actor_kind", "actor_id", "auth_method", "action", "resource_type",
		"resource_id", "workspace_id", "outcome", "reason", "request_id", "trace_id",
		"client_ip", "user_agent",
	}); err != nil {
		return err
	}
	written := 0
	for written < maxAuditExportRows {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := h.queries.ListAuditEvents(ctx, params)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			if err := cw.Write([]string{
				r.OccurredAt.Time.UTC().Format(time.RFC3339),
				r.ActorKind, strOrEmpty(r.ActorID), strOrEmpty(r.AuthMethod), r.Action,
				r.ResourceType, strOrEmpty(r.ResourceID), idString(r.WorkspaceID), r.Outcome,
				strOrEmpty(r.Reason), strOrEmpty(r.RequestID), strOrEmpty(r.TraceID),
				ipOrEmpty(r.ClientIp), strOrEmpty(r.UserAgent),
			}); err != nil {
				return err
			}
			written++
			if written >= maxAuditExportRows {
				break
			}
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
		if len(rows) < int(params.Lim) {
			break
		}
		last := rows[len(rows)-1]
		params.CursorAt = last.OccurredAt
		id := last.ID
		params.CursorID = &id
	}
	cw.Flush()
	return cw.Error()
}

func strOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func ipOrEmpty(value *netip.Addr) string {
	if value == nil {
		return ""
	}
	return value.String()
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
func (h *core) audited(ctx context.Context, e audit.Event) {
	if h.audit == nil {
		return
	}
	// Audit is best effort; Record logs and counts its own failures.
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
func connectionChanges(before, after db.CatalogConnection, creds *map[string]any) map[string]audit.Change {
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
	decodeStored(before.Config, &was, "connections.config")
	decodeStored(after.Config, &is, "connections.config")
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
	decodeStored(raw, &m, "extra_props")
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
