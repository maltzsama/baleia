// Package admin edits and retires catalogs and reports what depends on them
// (#360): renaming, retiring a managed catalog, its namespace and table
// counts, and the impact of removing a catalog or a connection. The caller
// checks access and deprovisions the bindings Retire returns.
package admin

import (
	"context"
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reader counts what depends on catalogs and connections; *db.Queries
// implements it.
type Reader interface {
	CountNamespacesByCatalog(ctx context.Context, catalogID pgtype.UUID) (int64, error)
	CountTablesByCatalog(ctx context.Context, catalogID pgtype.UUID) (int64, error)
	CountCatalogImpact(ctx context.Context, catalogID pgtype.UUID) (db.CountCatalogImpactRow, error)
	CountConnectionImpact(ctx context.Context, connectionID pgtype.UUID) (db.CountConnectionImpactRow, error)
	ListBindingsByCatalog(ctx context.Context, catalogID pgtype.UUID) ([]db.CatalogBinding, error)
	ListEngines(ctx context.Context) ([]db.Engine, error)
}

// Writer renames a catalog; *db.Queries implements it.
type Writer interface {
	UpdateCatalogMeta(ctx context.Context, arg db.UpdateCatalogMetaParams) (db.Catalog, error)
}

// Store is Reader and Writer.
type Store interface {
	Reader
	Writer
}

// TxStore disables a catalog with its managed bindings; db.Querier
// implements it.
type TxStore interface {
	SetCatalogEnabled(ctx context.Context, arg db.SetCatalogEnabledParams) error
	DisableManagedBindingsOfCatalog(ctx context.Context, catalogID pgtype.UUID) error
}

// Tx runs fn in one transaction.
type Tx func(ctx context.Context, fn func(TxStore) error) error

var (
	// ErrEmptyName: a catalog needs a name.
	ErrEmptyName = errors.New("name cannot be empty")
	// ErrNotManaged: the catalog belongs to its engine, not to Baleia.
	ErrNotManaged = errors.New("admin: catalog not managed by Baleia")
	// ErrBindingBusy: a binding of the catalog is provisioning or updating.
	ErrBindingBusy = errors.New("a binding of this catalog is provisioning or updating")
)

// Service administers catalogs.
type Service struct {
	store Store
	tx    Tx
}

// New builds the service.
func New(store Store, tx Tx) *Service { return &Service{store: store, tx: tx} }

// Edit changes a catalog's name or description; nil keeps the current one.
type Edit struct {
	Name        *string
	Description *string
}

// Update applies e to cat.
func (s *Service) Update(ctx context.Context, cat db.Catalog, e Edit) (db.Catalog, error) {
	name := cat.Name
	if e.Name != nil {
		name = strings.TrimSpace(*e.Name)
	}
	if name == "" {
		return db.Catalog{}, ErrEmptyName
	}
	description := cat.Description
	if e.Description != nil {
		description = e.Description
	}
	return s.store.UpdateCatalogMeta(ctx, db.UpdateCatalogMetaParams{ID: cat.ID, Name: name, Description: description})
}

// Retire disables a catalog Baleia manages, with its managed bindings, and
// returns those bindings for the caller to deprovision.
func (s *Service) Retire(ctx context.Context, cat db.Catalog) ([]db.CatalogBinding, error) {
	if cat.Origin != "source" {
		return nil, ErrNotManaged
	}
	all, err := s.store.ListBindingsByCatalog(ctx, cat.ID)
	if err != nil {
		return nil, err
	}
	var managed []db.CatalogBinding
	for _, b := range all {
		if b.LifecycleState == "provisioning" || b.LifecycleState == "updating" {
			return nil, ErrBindingBusy
		}
		if b.Mode == "managed" {
			managed = append(managed, b)
		}
	}
	err = s.tx(ctx, func(q TxStore) error {
		if err := q.SetCatalogEnabled(ctx, db.SetCatalogEnabledParams{ID: cat.ID, Enabled: false}); err != nil {
			return err
		}
		return q.DisableManagedBindingsOfCatalog(ctx, cat.ID)
	})
	if err != nil {
		return nil, err
	}
	return managed, nil
}

// Size is how much metadata a catalog holds; a count that could not be read
// is nil.
type Size struct {
	Namespaces *int
	Tables     *int
}

// Size counts a catalog's namespaces and tables. A failed count is left out,
// never an error: the size only decorates the catalog.
func (s *Service) Size(ctx context.Context, catalogID pgtype.UUID) Size {
	var out Size
	if n, err := s.store.CountNamespacesByCatalog(ctx, catalogID); err == nil {
		v := int(n)
		out.Namespaces = &v
	}
	if n, err := s.store.CountTablesByCatalog(ctx, catalogID); err == nil {
		v := int(n)
		out.Tables = &v
	}
	return out
}

// Impact counts what depends on a catalog.
func (s *Service) Impact(ctx context.Context, catalogID pgtype.UUID) (db.CountCatalogImpactRow, error) {
	return s.store.CountCatalogImpact(ctx, catalogID)
}

// ConnectionImpact is what depends on a connection.
type ConnectionImpact struct {
	db.CountConnectionImpactRow
	// Engine is the engine the connection backs, if any.
	Engine *db.Engine
}

// ConnectionImpact counts what depends on a connection. The engine is best
// effort: a failure to list engines leaves it out.
func (s *Service) ConnectionImpact(ctx context.Context, connectionID pgtype.UUID) (ConnectionImpact, error) {
	row, err := s.store.CountConnectionImpact(ctx, connectionID)
	if err != nil {
		return ConnectionImpact{}, err
	}
	out := ConnectionImpact{CountConnectionImpactRow: row}
	if engines, err := s.store.ListEngines(ctx); err == nil {
		for _, e := range engines {
			if e.ConnectionID.Bytes == connectionID.Bytes {
				out.Engine = &e
				break
			}
		}
	}
	return out, nil
}
