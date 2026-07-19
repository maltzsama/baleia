package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/storage"
	"github.com/galileostudio/baleia/internal/trino"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/client"
)

var ErrWorkspaceNotFound = errors.New("workspace not found")

type Handlers struct {
	queries        *db.Queries
	cfg            *config.Config
	temporalClient client.Client
	s3             *storage.S3Client
	trino          *trino.Client
	catalogCache   *TTLStore[[]workspaceCatalogEntry]
}

func NewHandlers(queries *db.Queries, cfg *config.Config, temporalClient client.Client, s3 *storage.S3Client) *Handlers {
	return &Handlers{
		queries:        queries,
		cfg:            cfg,
		temporalClient: temporalClient,
		s3:             s3,
		trino:          trino.New(cfg.TrinoURL, "baleia"),
		catalogCache:   NewTTLStore[[]workspaceCatalogEntry](60 * time.Second),
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func dbUserToPublic(u db.User) UserPublic {
	id := openapi_types.UUID(u.ID.Bytes)
	email := openapi_types.Email(u.Email)
	return UserPublic{
		Id:           id,
		Name:         u.Name,
		Email:        email,
		Role:         UserPublicRole(u.Role),
		AuthProvider: UserPublicAuthProvider(u.AuthProvider),
		CreatedAt:    pgTimestampToTime(u.CreatedAt),
	}
}

func pgTimestampToTime(ts pgtype.Timestamptz) time.Time {
	if ts.Valid {
		return ts.Time
	}
	return time.Time{}
}

func uuidFromString(s string) (pgtype.UUID, error) {
	var uid pgtype.UUID
	err := uid.Scan(s)
	return uid, err
}

func workspaceIDFromRequest(id openapi_types.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func requireWorkspaceAccess(ctx context.Context, queries *db.Queries, workspaceID openapi_types.UUID) error {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return err
	}

	hasAccess, err := queries.CheckWorkspaceAccess(ctx, db.CheckWorkspaceAccessParams{
		ID:     workspaceIDFromRequest(workspaceID),
		UserID: uid,
	})
	if err != nil {
		return err
	}
	if !hasAccess {
		return ErrWorkspaceNotFound
	}
	return nil
}

func errUnauthorized(msg string) Error {
	return Error{Code: "unauthorized", Message: msg}
}

func errForbidden(msg string) Error {
	return Error{Code: "forbidden", Message: msg}
}

func errNotFound(msg string) Error {
	return Error{Code: "not_found", Message: msg}
}

func errConflict(msg string) Error {
	return Error{Code: "conflict", Message: msg}
}

// respondError é usado apenas pelas rotas manuais (OIDC).
func respondError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"code":"` + code + `","message":"` + message + `"}`))
}