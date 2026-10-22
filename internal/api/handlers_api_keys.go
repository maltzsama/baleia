package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (h *AuthHandlers) ListApiKeys(ctx context.Context, request ListApiKeysRequestObject) (ListApiKeysResponseObject, error) { //nolint:revive // name fixed by the generated StrictServerInterface
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return ListApiKeys401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	keys, err := h.apiKeys.List(ctx, uid)
	if err != nil {
		return nil, err
	}

	result := make([]ApiKey, 0, len(keys))
	for _, k := range keys {
		result = append(result, dbApiKeyToPublic(k))
	}
	return ListApiKeys200JSONResponse(result), nil
}

func (h *AuthHandlers) CreateApiKey(ctx context.Context, request CreateApiKeyRequestObject) (CreateApiKeyResponseObject, error) { //nolint:revive // name fixed by the generated StrictServerInterface
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return CreateApiKey401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	key, secret, err := h.apiKeys.Create(ctx, uid, request.Body.Name, request.Body.ExpiresAt)
	if err != nil {
		return nil, err
	}

	// The key's secret is never audited, only its name.
	h.audited(ctx, audit.Event{Action: audit.APIKeyCreate, ResourceType: "api_key", ResourceID: idString(key.ID), Outcome: audit.Allowed,
		Changes: map[string]audit.Change{"name": {To: key.Name}}})
	pub := dbApiKeyToPublic(key)
	return CreateApiKey201JSONResponse{
		Id:        pub.Id,
		Name:      pub.Name,
		KeyPrefix: pub.KeyPrefix,
		ExpiresAt: pub.ExpiresAt,
		CreatedAt: pub.CreatedAt,
		Secret:    secret,
	}, nil
}

func (h *AuthHandlers) DeleteApiKey(ctx context.Context, request DeleteApiKeyRequestObject) (DeleteApiKeyResponseObject, error) { //nolint:revive // name fixed by the generated StrictServerInterface
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return DeleteApiKey401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	ev := audit.Event{Action: audit.APIKeyRevoke, ResourceType: "api_key", ResourceID: request.Id.String()}
	keyID, err := uuidFromString(request.Id.String())
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return DeleteApiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "api key not found"}}, nil
	}

	if err := h.apiKeys.Delete(ctx, uid, keyID); err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonInternal))
		return nil, err
	}

	h.audited(ctx, ev.Allowed())
	return DeleteApiKey204Response{}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func dbApiKeyToPublic(k db.ApiKey) ApiKey {
	id := openapi_types.UUID(k.ID.Bytes)
	prefix := k.Prefix
	if prefix == "" {
		prefix = k.KeyHash[:8] // pre-#276 rows
	}
	pub := ApiKey{
		Id:        id,
		Name:      k.Name,
		KeyPrefix: prefix + "...",
		CreatedAt: pgTimestampToTime(k.CreatedAt),
	}
	if k.ExpiresAt.Valid {
		t := k.ExpiresAt.Time
		pub.ExpiresAt = &t
	}
	return pub
}
