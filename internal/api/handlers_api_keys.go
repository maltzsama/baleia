package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (h *Handlers) ListApiKeys(ctx context.Context, request ListApiKeysRequestObject) (ListApiKeysResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return ListApiKeys401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	keys, err := h.queries.ListAPIKeysByUser(ctx, uid)
	if err != nil {
		return nil, err
	}

	result := make([]ApiKey, 0, len(keys))
	for _, k := range keys {
		result = append(result, dbApiKeyToPublic(k))
	}
	return ListApiKeys200JSONResponse(result), nil
}

func (h *Handlers) CreateApiKey(ctx context.Context, request CreateApiKeyRequestObject) (CreateApiKeyResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return CreateApiKey401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	secret := generateAPIKey()
	hash := hashAPIKey(secret)

	var expiresAt pgtype.Timestamptz
	if request.Body.ExpiresAt != nil {
		expiresAt = pgtype.Timestamptz{Time: *request.Body.ExpiresAt, Valid: true}
	}

	key, err := h.queries.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID:    uid,
		Name:      request.Body.Name,
		KeyHash:   hash,
		Prefix:    secret[:8],
		Scopes:    []string{"*"},
		ExpiresAt: expiresAt,
	})
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

func (h *Handlers) DeleteApiKey(ctx context.Context, request DeleteApiKeyRequestObject) (DeleteApiKeyResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return DeleteApiKey401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	ev := audit.Event{Action: audit.APIKeyRevoke, ResourceType: "api_key", ResourceID: request.Id.String()}
	keyID, err := uuidFromString(request.Id.String())
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return DeleteApiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "api key not found"}}, nil
	}

	if err := h.queries.DeleteAPIKey(ctx, db.DeleteAPIKeyParams{
		ID:     keyID,
		UserID: uid,
	}); err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return DeleteApiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "api key not found"}}, nil
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

func generateAPIKey() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "bal_" + base64.RawURLEncoding.EncodeToString(b)
}

func hashAPIKey(key string) string {
	return auth.HashAPIKey(key)
}
