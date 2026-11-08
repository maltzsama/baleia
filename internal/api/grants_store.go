package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/jackc/pgx/v5/pgtype"
)

// catalogGrantStore is the grant flow's persistence. *db.Queries and
// *repo.Repo both implement it, so the handlers keep the sqlc path in tests
// and run GORM at runtime (#771).
type catalogGrantStore interface {
	GetCatalogByID(ctx context.Context, id pgtype.UUID) (db.Catalog, error)
	GetWorkspaceByID(ctx context.Context, id pgtype.UUID) (db.Workspace, error)
	ListWorkspaceCatalogGrants(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListWorkspaceCatalogGrantsRow, error)
	ListCatalogGrantRequestsForWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListCatalogGrantRequestsForWorkspaceRow, error)
	ListCatalogGrants(ctx context.Context, catalogID pgtype.UUID) ([]db.ListCatalogGrantsRow, error)
	ListCatalogGrantRequestsForCatalog(ctx context.Context, catalogID pgtype.UUID) ([]db.ListCatalogGrantRequestsForCatalogRow, error)
	UpsertWorkspaceCatalogGrant(ctx context.Context, arg db.UpsertWorkspaceCatalogGrantParams) (db.WorkspaceCatalogGrant, error)
	CreateCatalogGrantRequest(ctx context.Context, arg db.CreateCatalogGrantRequestParams) (db.CatalogGrantRequest, error)
	GetCatalogGrantRequest(ctx context.Context, arg db.GetCatalogGrantRequestParams) (db.CatalogGrantRequest, error)
	DeleteCatalogGrantRequest(ctx context.Context, arg db.DeleteCatalogGrantRequestParams) (int64, error)
	DeleteWorkspaceCatalogGrant(ctx context.Context, arg db.DeleteWorkspaceCatalogGrantParams) (int64, error)
	ListGrantableWorkspaces(ctx context.Context) ([]db.ListGrantableWorkspacesRow, error)
	SetCatalogDiscoverable(ctx context.Context, arg db.SetCatalogDiscoverableParams) error
	ListDiscoverableCatalogs(ctx context.Context) ([]db.ListDiscoverableCatalogsRow, error)
}

// catalogGrants returns the grant store: GORM when configured, sqlc otherwise.
func (h *core) catalogGrants() catalogGrantStore {
	if h.store != nil {
		return repo.New(h.store)
	}
	return h.queries
}
