package api

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/openlineage"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/google/uuid"
)

var errLineageUnauthorized = errors.New("unauthorized")
var errLineageForbidden = errors.New("forbidden")
var errLineageNotFound = errors.New("not found")

func (h *CatalogHandlers) lineageManager(ctx context.Context, ws db.UUID) (authz.Principal, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return authz.Principal{}, errLineageUnauthorized
	}
	if _, err = h.access.Workspace(ctx, ws); errors.Is(err, wsaccess.ErrNotFound) || errors.Is(err, db.ErrNoRows) {
		return p, errLineageNotFound
	} else if err != nil {
		return p, err
	}
	if p.IsAdmin() {
		return p, nil
	}
	if err = h.access.Check(ctx, ws, wsaccess.Actor{User: p.UserID}); errors.Is(err, wsaccess.ErrNotFound) {
		return p, errLineageNotFound
	} else if err != nil {
		return p, err
	}
	owner, err := h.queries.IsWorkspaceStructuralOwner(ctx, db.IsWorkspaceStructuralOwnerParams{ID: ws, UserID: p.UserID})
	if err != nil {
		return p, err
	}
	if !owner {
		return p, errLineageForbidden
	}
	return p, nil
}

func (h *CatalogHandlers) GetOpenLineageDestination(ctx context.Context, req GetOpenLineageDestinationRequestObject) (GetOpenLineageDestinationResponseObject, error) {
	ws := db.UUID{Bytes: req.Id, Valid: true}
	if _, err := h.lineageManager(ctx, ws); err != nil {
		switch {
		case errors.Is(err, errLineageUnauthorized):
			return GetOpenLineageDestination401JSONResponse{UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		case errors.Is(err, errLineageForbidden):
			return GetOpenLineageDestination403JSONResponse{ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "forbidden"}}, nil
		case errors.Is(err, errLineageNotFound):
			return GetOpenLineageDestination404JSONResponse{NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
		}
		return nil, err
	}
	row, err := repo.NewOpenLineageDestinations(h.store).Get(ctx, repo.UUIDString(ws))
	if errors.Is(err, db.ErrNoRows) {
		return GetOpenLineageDestination200JSONResponse{}, nil
	}
	if err != nil {
		return nil, err
	}
	return GetOpenLineageDestination200JSONResponse{Configured: true, Endpoint: row.Endpoint, Namespace: row.Namespace}, nil
}

func (h *CatalogHandlers) PutOpenLineageDestination(ctx context.Context, req PutOpenLineageDestinationRequestObject) (PutOpenLineageDestinationResponseObject, error) {
	ws := db.UUID{Bytes: req.Id, Valid: true}
	p, err := h.lineageManager(ctx, ws)
	if err != nil {
		switch {
		case errors.Is(err, errLineageUnauthorized):
			return PutOpenLineageDestination401JSONResponse{UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		case errors.Is(err, errLineageForbidden):
			return PutOpenLineageDestination403JSONResponse{ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "forbidden"}}, nil
		case errors.Is(err, errLineageNotFound):
			return PutOpenLineageDestination404JSONResponse{NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
		}
		return nil, err
	}
	invalid := func() (PutOpenLineageDestinationResponseObject, error) {
		return PutOpenLineageDestination422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "invalid OpenLineage destination")}, nil
	}
	if req.Body == nil {
		return invalid()
	}
	endpoint := strings.TrimSpace(req.Body.Endpoint)
	namespace := strings.TrimSpace(req.Body.Namespace)
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.RawQuery != "" || namespace == "" || len(namespace) > 256 {
		return invalid()
	}
	store := repo.NewOpenLineageDestinations(h.store)
	key := repo.UUIDString(ws)
	row, err := store.Get(ctx, key)
	if err != nil && !errors.Is(err, db.ErrNoRows) {
		return nil, err
	}
	if req.Body.Token != nil {
		if *req.Body.Token == "" {
			row.TokenEnc = ""
		} else {
			if h.keyring == nil {
				return invalid()
			}
			row.TokenEnc, err = h.keyring.Seal([]byte(*req.Body.Token), "openlineage:"+key)
			if err != nil {
				return nil, err
			}
		}
	}
	user := model.UUID(repo.UUIDString(p.UserID))
	row.WorkspaceID = model.UUID(key)
	row.Endpoint = endpoint
	row.Namespace = namespace
	row.UpdatedBy = &user
	row, err = store.Upsert(ctx, row, req.Body.Token != nil)
	if errors.Is(err, openlineage.ErrInsecureBearer) {
		return invalid()
	}
	if err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{Action: audit.WorkspaceUpdate, ResourceType: "openlineage_destination", ResourceID: key, WorkspaceID: key, Changes: map[string]audit.Change{"configured": {To: true}}}.Allowed())
	return PutOpenLineageDestination200JSONResponse{Configured: true, Endpoint: row.Endpoint, Namespace: row.Namespace}, nil
}

func (h *CatalogHandlers) DeleteOpenLineageDestination(ctx context.Context, req DeleteOpenLineageDestinationRequestObject) (DeleteOpenLineageDestinationResponseObject, error) {
	ws := db.UUID{Bytes: req.Id, Valid: true}
	if _, err := h.lineageManager(ctx, ws); err != nil {
		switch {
		case errors.Is(err, errLineageUnauthorized):
			return DeleteOpenLineageDestination401JSONResponse{UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		case errors.Is(err, errLineageForbidden):
			return DeleteOpenLineageDestination403JSONResponse{ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "forbidden"}}, nil
		case errors.Is(err, errLineageNotFound):
			return DeleteOpenLineageDestination404JSONResponse{NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
		}
		return nil, err
	}
	if err := repo.NewOpenLineageDestinations(h.store).Delete(ctx, repo.UUIDString(ws)); err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{Action: audit.WorkspaceUpdate, ResourceType: "openlineage_destination", ResourceID: repo.UUIDString(ws), WorkspaceID: repo.UUIDString(ws), Changes: map[string]audit.Change{"configured": {To: false}}}.Allowed())
	return DeleteOpenLineageDestination204Response{}, nil
}

func (h *CatalogHandlers) ExportOpenLineage(ctx context.Context, req ExportOpenLineageRequestObject) (ExportOpenLineageResponseObject, error) {
	ws := db.UUID{Bytes: req.Id, Valid: true}
	p, err := h.lineageManager(ctx, ws)
	if err != nil {
		switch {
		case errors.Is(err, errLineageUnauthorized):
			return ExportOpenLineage401JSONResponse{UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		case errors.Is(err, errLineageForbidden):
			return ExportOpenLineage403JSONResponse{ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "forbidden"}}, nil
		case errors.Is(err, errLineageNotFound):
			return ExportOpenLineage404JSONResponse{NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
		}
		return nil, err
	}
	row, err := repo.NewOpenLineageDestinations(h.store).Get(ctx, repo.UUIDString(ws))
	if errors.Is(err, db.ErrNoRows) {
		return ExportOpenLineage422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "OpenLineage destination is not configured")}, nil
	}
	if err != nil {
		return nil, err
	}
	token := ""
	if row.TokenEnc != "" {
		if h.keyring == nil {
			return nil, errors.New("credential encryption unavailable")
		}
		plain, err := h.keyring.Open(row.TokenEnc, "openlineage:"+repo.UUIDString(ws))
		if err != nil {
			return nil, err
		}
		token = string(plain)
	}
	if h.dialer == nil {
		return nil, errors.New("outbound dialer unavailable")
	}
	publisher := openlineage.HTTPPublisher{Client: h.dialer.HTTPClient(10 * time.Second), Endpoint: row.Endpoint, BearerToken: token}
	items, _, err := h.lineageContent(ctx, ws, p.UserID)
	if err != nil {
		return nil, err
	}
	if len(items) > 5000 {
		return ExportOpenLineage422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeResourceExhausted, "lineage snapshot exceeds export limit")}, nil
	}
	grants, err := h.catalogGrants().ListWorkspaceCatalogGrants(ctx, ws)
	if err != nil {
		return nil, err
	}
	catalogs := make(map[string]string, len(grants))
	for _, g := range grants {
		catalogs[idString(g.CatalogID)] = g.CatalogName
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		if req.Params.Kind != nil && string(items[key].node.Kind) != string(*req.Params.Kind) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	run := uuid.New()
	result := OpenLineageExportResult{RunId: run}
	events := make([]openlineage.RunEvent, 0, len(keys))
	for _, key := range keys {
		event, ok, err := h.lineageRunEvent(ctx, ws, row.Namespace, run.String(), items[key], catalogs)
		if err != nil {
			return nil, err
		}
		if !ok {
			result.Skipped++
			continue
		}
		events = append(events, event)
	}
	for _, event := range events {
		if err = publisher.Publish(ctx, event); err != nil {
			result.Failed++
		} else {
			result.Sent++
		}
	}
	h.logger.InfoContext(ctx, "OpenLineage export completed", "workspace_id", repo.UUIDString(ws), "sent", result.Sent, "skipped", result.Skipped, "failed", result.Failed)
	h.audited(ctx, audit.Event{Action: audit.LineageExport, ResourceType: "openlineage_destination", ResourceID: repo.UUIDString(ws), WorkspaceID: repo.UUIDString(ws), Changes: map[string]audit.Change{"sent": {To: result.Sent}, "skipped": {To: result.Skipped}, "failed": {To: result.Failed}}}.Allowed())
	return ExportOpenLineage200JSONResponse(result), nil
}
