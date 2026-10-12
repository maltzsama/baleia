package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

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
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, err
	}

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

	keyID, err := uuidFromString(request.Id.String())
	if err != nil {
		return DeleteApiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "api key not found"}}, nil
	}

	if err := h.queries.DeleteAPIKey(ctx, db.DeleteAPIKeyParams{
		ID:     keyID,
		UserID: uid,
	}); err != nil {
		return DeleteApiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "api key not found"}}, nil
	}

	return DeleteApiKey204Response{}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func dbApiKeyToPublic(k db.ApiKey) ApiKey {
	id := openapi_types.UUID(k.ID.Bytes)
	pub := ApiKey{
		Id:        id,
		Name:      k.Name,
		KeyPrefix: k.KeyHash[:8] + "...",
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
	rand.Read(b)
	return "blw_" + base64.URLEncoding.EncodeToString(b)
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", h)
}
