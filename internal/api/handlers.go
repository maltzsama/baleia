package api

import (
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
)

type Handlers struct {
	queries *db.Queries
	cfg     *config.Config
}

func NewHandlers(queries *db.Queries, cfg *config.Config) *Handlers {
	return &Handlers{queries: queries, cfg: cfg}
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