// Package topology reads and edits how catalogs sit on engines (#38, #364):
// engines, Trino clusters, bindings and their status, catalogs by connection,
// grant or name, and a catalog's sync state and stored columns. Lookups
// return the store's errors as they are (pgx.ErrNoRows for a missing row),
// so callers keep deciding what a missing engine or binding means.
package topology

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Engines reads engines and Trino clusters; *db.Queries implements it.
type Engines interface {
	GetEngineByID(ctx context.Context, id pgtype.UUID) (db.Engine, error)
	GetTrinoClusterByID(ctx context.Context, id pgtype.UUID) (db.TrinoCluster, error)
	GetTrinoClusterByEngine(ctx context.Context, engineID pgtype.UUID) (db.TrinoCluster, error)
	GetDefaultTrinoCluster(ctx context.Context) (db.TrinoCluster, error)
	GetCatalogConnectionByID(ctx context.Context, id pgtype.UUID) (db.CatalogConnection, error)
}

// Bindings reads and removes bindings; *db.Queries implements it.
type Bindings interface {
	GetBinding(ctx context.Context, id pgtype.UUID) (db.CatalogBinding, error)
	GetManagedBindingOfCatalog(ctx context.Context, arg db.GetManagedBindingOfCatalogParams) (db.CatalogBinding, error)
	GetBindingStatus(ctx context.Context, bindingID pgtype.UUID) (db.BindingStatusV, error)
	ListBindingsByEngine(ctx context.Context, engineID pgtype.UUID) ([]db.CatalogBinding, error)
	ListBindingsByCatalog(ctx context.Context, catalogID pgtype.UUID) ([]db.CatalogBinding, error)
	CountManagedBindingsByState(ctx context.Context) ([]db.CountManagedBindingsByStateRow, error)
	DeleteBinding(ctx context.Context, id pgtype.UUID) error
}

// Catalogs reads and edits catalogs; *db.Queries implements it.
type Catalogs interface {
	ListVisibleCatalogs(ctx context.Context, arg db.ListVisibleCatalogsParams) ([]db.Catalog, error)
	ListCatalogGrants(ctx context.Context, catalogID pgtype.UUID) ([]db.ListCatalogGrantsRow, error)
	ListCatalogsGrantedToUser(ctx context.Context, arg db.ListCatalogsGrantedToUserParams) ([]db.Catalog, error)
	ListCatalogsByConnection(ctx context.Context, connectionID pgtype.UUID) ([]db.Catalog, error)
	ListCatalogConnections(ctx context.Context, createdBy pgtype.UUID) ([]db.CatalogConnection, error)
	ListGrantedCatalogsOfWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListGrantedCatalogsOfWorkspaceRow, error)
	GetCatalogByTrinoName(ctx context.Context, arg db.GetCatalogByTrinoNameParams) (db.Catalog, error)
	GetCatalogSyncState(ctx context.Context, catalogID pgtype.UUID) (db.GetCatalogSyncStateRow, error)
	GetMetadataTableByName(ctx context.Context, arg db.GetMetadataTableByNameParams) (pgtype.UUID, error)
	ListMetadataColumns(ctx context.Context, tableID pgtype.UUID) ([]db.ListMetadataColumnsRow, error)
	UpdateCatalog(ctx context.Context, arg db.UpdateCatalogParams) (db.Catalog, error)
	SetCatalogEnabled(ctx context.Context, arg db.SetCatalogEnabledParams) error
}

// Store is Engines, Bindings and Catalogs.
type Store interface {
	Engines
	Bindings
	Catalogs
}

// Service reads and edits the catalog topology.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// Available reports whether there is a database to read from.
func (s *Service) Available() bool { return s != nil && s.store != nil }

// Engine returns an engine.
func (s *Service) Engine(ctx context.Context, id pgtype.UUID) (db.Engine, error) {
	return s.store.GetEngineByID(ctx, id)
}

// Cluster returns a Trino cluster.
func (s *Service) Cluster(ctx context.Context, id pgtype.UUID) (db.TrinoCluster, error) {
	return s.store.GetTrinoClusterByID(ctx, id)
}

// ClusterOfEngine returns the Trino cluster an engine runs.
func (s *Service) ClusterOfEngine(ctx context.Context, engineID pgtype.UUID) (db.TrinoCluster, error) {
	return s.store.GetTrinoClusterByEngine(ctx, engineID)
}

// DefaultCluster returns the default Trino cluster.
func (s *Service) DefaultCluster(ctx context.Context) (db.TrinoCluster, error) {
	return s.store.GetDefaultTrinoCluster(ctx)
}

// Connection returns a catalog connection, without checking access.
func (s *Service) Connection(ctx context.Context, id pgtype.UUID) (db.CatalogConnection, error) {
	return s.store.GetCatalogConnectionByID(ctx, id)
}

// Binding returns a binding.
func (s *Service) Binding(ctx context.Context, id pgtype.UUID) (db.CatalogBinding, error) {
	return s.store.GetBinding(ctx, id)
}

// ManagedBinding returns the managed binding of a catalog on an engine.
func (s *Service) ManagedBinding(ctx context.Context, catalogID, engineID pgtype.UUID) (db.CatalogBinding, error) {
	return s.store.GetManagedBindingOfCatalog(ctx, db.GetManagedBindingOfCatalogParams{CatalogID: catalogID, EngineID: engineID})
}

// BindingStatus returns what the engine last reported about a binding.
func (s *Service) BindingStatus(ctx context.Context, bindingID pgtype.UUID) (db.BindingStatusV, error) {
	return s.store.GetBindingStatus(ctx, bindingID)
}

// BindingsOfEngine lists an engine's bindings.
func (s *Service) BindingsOfEngine(ctx context.Context, engineID pgtype.UUID) ([]db.CatalogBinding, error) {
	return s.store.ListBindingsByEngine(ctx, engineID)
}

// BindingsOfCatalog lists a catalog's bindings.
func (s *Service) BindingsOfCatalog(ctx context.Context, catalogID pgtype.UUID) ([]db.CatalogBinding, error) {
	return s.store.ListBindingsByCatalog(ctx, catalogID)
}

// BindingStates counts managed bindings by lifecycle state.
func (s *Service) BindingStates(ctx context.Context) ([]db.CountManagedBindingsByStateRow, error) {
	return s.store.CountManagedBindingsByState(ctx)
}

// DeleteBinding removes a binding row.
func (s *Service) DeleteBinding(ctx context.Context, id pgtype.UUID) error {
	return s.store.DeleteBinding(ctx, id)
}

// VisibleCatalogs lists every catalog to an admin, and to anyone else the
// catalogs they may see.
func (s *Service) VisibleCatalogs(ctx context.Context, userID pgtype.UUID, admin bool) ([]db.Catalog, error) {
	return s.store.ListVisibleCatalogs(ctx, db.ListVisibleCatalogsParams{IsAdmin: admin, UserID: userID})
}

// Grants lists the workspaces a catalog is granted to.
func (s *Service) Grants(ctx context.Context, catalogID pgtype.UUID) ([]db.ListCatalogGrantsRow, error) {
	return s.store.ListCatalogGrants(ctx, catalogID)
}

// GrantedToUser lists a connection's catalogs granted to a user through a
// workspace.
func (s *Service) GrantedToUser(ctx context.Context, connID, userID pgtype.UUID) ([]db.Catalog, error) {
	return s.store.ListCatalogsGrantedToUser(ctx, db.ListCatalogsGrantedToUserParams{ConnectionID: connID, UserID: userID})
}

// CatalogsOfConnection lists a connection's catalogs.
func (s *Service) CatalogsOfConnection(ctx context.Context, connID pgtype.UUID) ([]db.Catalog, error) {
	return s.store.ListCatalogsByConnection(ctx, connID)
}

// ConnectionsOf lists the connections a user created.
func (s *Service) ConnectionsOf(ctx context.Context, userID pgtype.UUID) ([]db.CatalogConnection, error) {
	return s.store.ListCatalogConnections(ctx, userID)
}

// GrantedToWorkspace lists the catalogs granted to a workspace.
func (s *Service) GrantedToWorkspace(ctx context.Context, ws pgtype.UUID) ([]db.ListGrantedCatalogsOfWorkspaceRow, error) {
	return s.store.ListGrantedCatalogsOfWorkspace(ctx, ws)
}

// CatalogByTrinoName finds a catalog by its Trino name on a cluster.
func (s *Service) CatalogByTrinoName(ctx context.Context, arg db.GetCatalogByTrinoNameParams) (db.Catalog, error) {
	return s.store.GetCatalogByTrinoName(ctx, arg)
}

// SyncState returns a catalog's metadata sync state.
func (s *Service) SyncState(ctx context.Context, catalogID pgtype.UUID) (db.GetCatalogSyncStateRow, error) {
	return s.store.GetCatalogSyncState(ctx, catalogID)
}

// Columns returns the stored columns of a table, found by name.
func (s *Service) Columns(ctx context.Context, arg db.GetMetadataTableByNameParams) ([]db.ListMetadataColumnsRow, error) {
	id, err := s.store.GetMetadataTableByName(ctx, arg)
	if err != nil {
		return nil, err
	}
	return s.store.ListMetadataColumns(ctx, id)
}

// UpdateCatalog stores a catalog's new definition.
func (s *Service) UpdateCatalog(ctx context.Context, arg db.UpdateCatalogParams) (db.Catalog, error) {
	return s.store.UpdateCatalog(ctx, arg)
}

// Disable disables a catalog.
func (s *Service) Disable(ctx context.Context, catalogID pgtype.UUID) error {
	return s.store.SetCatalogEnabled(ctx, db.SetCatalogEnabledParams{ID: catalogID, Enabled: false})
}
