package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/audit/trail"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ListAuditEvents — GET /audit/events (#45). A global admin sees every
// event; anyone else only the events they performed.
func (h *OpsHandlers) ListAuditEvents(ctx context.Context, request ListAuditEventsRequestObject) (ListAuditEventsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListAuditEvents401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	q := request.Params
	filter := trail.Filter{
		ResourceType: q.ResourceType, ResourceID: q.ResourceId, ActorID: q.ActorId, Action: q.Action,
		RequestID: q.RequestId, TraceID: q.TraceId, From: q.From, To: q.To,
	}
	if q.Outcome != nil {
		o := string(*q.Outcome)
		filter.Outcome = &o
	}
	cursor := ""
	if q.Cursor != nil {
		cursor = *q.Cursor
	}
	page, err := h.trail.Page(ctx, trail.Viewer{UserID: p.UserID, Admin: p.IsAdmin()}, filter, q.Limit, cursor)
	var window *trail.WindowError
	var invalid *trail.InvalidError
	switch {
	case errors.As(err, &window):
		return ListAuditEvents422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidTimeRange, window.Msg)}, nil
	case errors.As(err, &invalid):
		return ListAuditEvents422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalid.Msg)}, nil
	case err != nil:
		return nil, err
	}
	out := ListAuditEvents200JSONResponse{Items: make([]AuditEvent, 0, len(page.Events)), NextCursor: page.NextCursor}
	for _, r := range page.Events {
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
			user, err := h.trail.User(ctx, pgtype.UUID{Bytes: [16]byte(id), Valid: true})
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
			catalog, err := h.trail.Catalog(ctx, pgtype.UUID{Bytes: [16]byte(id), Valid: true})
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
	filter := trail.Filter{
		ResourceType: q.ResourceType, ResourceID: q.ResourceId, ActorID: q.ActorId, Action: q.Action,
		RequestID: q.RequestId, TraceID: q.TraceId, From: q.From, To: q.To,
	}
	if q.Outcome != nil {
		o := string(*q.Outcome)
		filter.Outcome = &o
	}
	var window *trail.WindowError
	if err := trail.CheckWindow(filter); errors.As(err, &window) {
		return ExportAuditCSV422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidTimeRange, window.Msg)}, nil
	}

	reader, writer := io.Pipe()
	go func() {
		_ = writer.CloseWithError(h.trail.Export(ctx, writer, filter))
	}()
	return ExportAuditCSV200TextcsvResponse{Body: reader}, nil
}

func strOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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
