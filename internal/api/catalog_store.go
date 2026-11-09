package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
)

// catalogWriteStore is the persistence the catalog and grant write flows need
// in one transaction. *repo.Repo implements it over the GORM store (#771).
type catalogWriteStore interface {
	CreateCatalog(ctx context.Context, arg db.CreateCatalogParams) (db.Catalog, error)
	UpdateCatalog(ctx context.Context, arg db.UpdateCatalogParams) (db.Catalog, error)
	SetCatalogEnabled(ctx context.Context, arg db.SetCatalogEnabledParams) error
	UpsertManagedBinding(ctx context.Context, arg db.UpsertManagedBindingParams) (db.CatalogBinding, error)
	UpdateBinding(ctx context.Context, arg db.UpdateBindingParams) (db.CatalogBinding, error)
	DisableManagedBindingsOfCatalog(ctx context.Context, catalogID db.UUID) error
	UpsertWorkspaceCatalogGrant(ctx context.Context, arg db.UpsertWorkspaceCatalogGrantParams) (db.WorkspaceCatalogGrant, error)
	DeleteCatalogGrantRequest(ctx context.Context, arg db.DeleteCatalogGrantRequestParams) (int64, error)
}

// inCatalogTx runs fn in one transaction, on the GORM store when it is
// configured and on the pool otherwise.
func (h *core) inCatalogTx(ctx context.Context, fn func(catalogWriteStore) error) error {
	if h.store != nil {
		return repo.New(h.store).TxCatalogWrite(ctx, func(r *repo.Repo) error { return fn(r) })
	}
	return h.inTx(ctx, func(q db.Querier) error { return fn(q) })
}
