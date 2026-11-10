package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// allowedWebhookEvents is the closed set the API accepts.
var allowedWebhookEvents = map[string]bool{
	"execution.finished": true,
	"catalog.synced":     true,
	"audit":              true,
	"*":                  true,
}

var (
	errWebhookUnauthorized = errors.New("unauthorized")
	errWebhookForbidden    = errors.New("forbidden")
	errWebhookNotFound     = errors.New("not found")
)

// webhookManager authorizes a webhook operation: an admin, or a structural
// owner of the workspace.
func (h *WorkspaceHandlers) webhookManager(ctx context.Context, wsID db.UUID) (authz.Principal, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return authz.Principal{}, errWebhookUnauthorized
	}
	if p.IsAdmin() {
		return p, nil
	}
	if err := h.access.Check(ctx, wsID, wsaccess.Actor{User: p.UserID}); err != nil {
		return authz.Principal{}, errWebhookNotFound
	}
	owner, err := h.queries.IsWorkspaceStructuralOwner(ctx, db.IsWorkspaceStructuralOwnerParams{ID: wsID, UserID: p.UserID})
	if err != nil {
		return authz.Principal{}, err
	}
	if !owner {
		return authz.Principal{}, errWebhookForbidden
	}
	return p, nil
}

func (h *WorkspaceHandlers) ListWebhooks(ctx context.Context, request ListWebhooksRequestObject) (ListWebhooksResponseObject, error) {
	wsID := db.UUID{Bytes: [16]byte(request.Id), Valid: true}
	if _, err := h.webhookManager(ctx, wsID); err != nil {
		return webhookError[ListWebhooksResponseObject](err,
			ListWebhooks401JSONResponse{UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "unauthorized"}},
			ListWebhooks403JSONResponse{ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "forbidden"}},
			ListWebhooks404JSONResponse{NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}},
		)
	}
	if h.webhooks == nil {
		return nil, errors.New("webhooks store is not configured")
	}
	rows, err := h.webhooks.List(ctx, repo.UUIDString(wsID))
	if err != nil {
		return nil, err
	}
	out := make([]Webhook, 0, len(rows))
	for _, w := range rows {
		out = append(out, webhookToPublic(w))
	}
	return ListWebhooks200JSONResponse(out), nil
}

func (h *WorkspaceHandlers) CreateWebhook(ctx context.Context, request CreateWebhookRequestObject) (CreateWebhookResponseObject, error) {
	wsID := db.UUID{Bytes: [16]byte(request.Id), Valid: true}
	p, err := h.webhookManager(ctx, wsID)
	if err != nil {
		return webhookError[CreateWebhookResponseObject](err,
			CreateWebhook401JSONResponse{UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "unauthorized"}},
			CreateWebhook403JSONResponse{ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "forbidden"}},
			CreateWebhook404JSONResponse{NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}},
		)
	}
	if request.Body == nil || validateWebhookRequest(*request.Body) != nil {
		return CreateWebhook422JSONResponse{}, nil
	}
	if h.keyring == nil || h.webhooks == nil {
		return nil, errors.New("webhooks are not configured")
	}

	events := make([]string, 0, len(request.Body.Events))
	for _, e := range request.Body.Events {
		events = append(events, string(e))
	}
	eventsJSON, err := json.Marshal(events)
	if err != nil {
		return nil, err
	}
	secret, err := randomSecret()
	if err != nil {
		return nil, err
	}
	id := repo.NewUUID()
	sealed, err := h.keyring.Seal([]byte(secret), "webhook:"+id)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	row, err := h.webhooks.Create(ctx, model.Webhook{
		ID:          model.UUID(id),
		WorkspaceID: model.UUID(repo.UUIDString(wsID)),
		URL:         request.Body.Url,
		SecretEnc:   sealed,
		Events:      model.JSON(eventsJSON),
		Enabled:     true,
		CreatedBy:   model.UUID(repo.UUIDString(p.UserID)),
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		return nil, err
	}
	public := webhookToPublic(row)
	return CreateWebhook201JSONResponse(CreatedWebhook{
		Id:        public.Id,
		Url:       public.Url,
		Events:    public.Events,
		Enabled:   public.Enabled,
		CreatedAt: public.CreatedAt,
		UpdatedAt: public.UpdatedAt,
		Secret:    secret,
	}), nil
}

func (h *WorkspaceHandlers) DeleteWebhook(ctx context.Context, request DeleteWebhookRequestObject) (DeleteWebhookResponseObject, error) {
	wsID := db.UUID{Bytes: [16]byte(request.Id), Valid: true}
	if _, err := h.webhookManager(ctx, wsID); err != nil {
		return webhookError[DeleteWebhookResponseObject](err,
			DeleteWebhook401JSONResponse{UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "unauthorized"}},
			DeleteWebhook403JSONResponse{ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "forbidden"}},
			DeleteWebhook404JSONResponse{NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}},
		)
	}
	if h.webhooks == nil {
		return nil, errors.New("webhooks store is not configured")
	}
	ok, err := h.webhooks.Delete(ctx, repo.UUIDString(wsID), uuid.UUID(request.WebhookId).String())
	if err != nil {
		return nil, err
	}
	if !ok {
		return DeleteWebhook404JSONResponse{NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "webhook not found"}}, nil
	}
	return DeleteWebhook204Response{}, nil
}

func webhookToPublic(w model.Webhook) Webhook {
	var events []string
	_ = json.Unmarshal([]byte(w.Events), &events)
	id, _ := uuid.Parse(string(w.ID))
	updated := w.UpdatedAt
	return Webhook{
		Id:        openapi_types.UUID(id),
		Url:       w.URL,
		Events:    events,
		Enabled:   w.Enabled,
		CreatedAt: w.CreatedAt,
		UpdatedAt: &updated,
	}
}

func validateWebhookRequest(req CreateWebhookJSONRequestBody) error {
	u, err := url.Parse(req.Url)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("webhook url must be https")
	}
	if len(req.Events) == 0 {
		return errors.New("at least one event is required")
	}
	for _, e := range req.Events {
		if !allowedWebhookEvents[string(e)] {
			return errors.New("unknown webhook event")
		}
	}
	return nil
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// webhookError maps the sentinel errors to the generated responses.
func webhookError[T any](err error, unauthorized, forbidden, notFound T) (T, error) {
	switch {
	case errors.Is(err, errWebhookUnauthorized):
		return unauthorized, nil
	case errors.Is(err, errWebhookForbidden):
		return forbidden, nil
	case errors.Is(err, errWebhookNotFound):
		return notFound, nil
	default:
		var zero T
		return zero, err
	}
}
