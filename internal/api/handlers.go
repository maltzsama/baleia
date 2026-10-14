package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	bindtrino "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/storage"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
)

var ErrWorkspaceNotFound = errors.New("workspace not found")

type Handlers struct {
	queries        db.Querier
	pool           *pgxpool.Pool
	cfg            *config.Config
	keyring        *crypto.Keyring
	temporalClient client.Client
	s3             *storage.S3Client
	catalogCache   *TTLStore[[]workspaceCatalogEntry]
	// connections holds every connection provider (docs/adr/0002).
	connections *connections.Registry
	// engines runs SQL (docs/adr/0002); catalogDDL manages Trino catalogs as
	// the admin identity (docs/adr/0005). Both are built at the wiring site.
	engines    engineSource
	catalogDDL catalogDDL
}

// engineSource returns engines; *queryengine.Manager implements it.
type engineSource interface {
	Get(ctx context.Context, engineID pgtype.UUID) (queryengine.Engine, error)
	DefaultTrino(ctx context.Context) (queryengine.Engine, error)
}

// catalogDDL creates and drops Trino catalogs; *bindings/trino.DDL implements it.
type catalogDDL interface {
	CreateCatalog(ctx context.Context, spec bindtrino.CatalogSpec) error
	DropCatalog(ctx context.Context, name string) error
	ListCatalogs(ctx context.Context) ([]string, error)
}

func NewHandlers(queries db.Querier, pool *pgxpool.Pool, cfg *config.Config, keyring *crypto.Keyring, temporalClient client.Client, s3 *storage.S3Client, conns *connections.Registry, engines engineSource, ddl catalogDDL) *Handlers {
	return &Handlers{
		connections:    conns,
		queries:        queries,
		pool:           pool,
		cfg:            cfg,
		keyring:        keyring,
		temporalClient: temporalClient,
		s3:             s3,
		engines:        engines,
		catalogDDL:     ddl,
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

func requireWorkspaceAccess(ctx context.Context, queries db.Querier, workspaceID openapi_types.UUID) error {
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

// respondError is only used by the manual (OIDC) routes.
func respondError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"code":"` + code + `","message":"` + message + `"}`))
}