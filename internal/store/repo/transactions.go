package repo

import (
	"context"

	"github.com/galileostudio/baleia/internal/catalogs/admin"
	"github.com/galileostudio/baleia/internal/engines/manage"
	"github.com/galileostudio/baleia/internal/identity/setup"
	"gorm.io/gorm"
)

// TxEngines runs fn in one transaction over a Repo bound to it, for creating
// or deleting an engine with its native catalogs.
func (r *Repo) TxEngines(ctx context.Context, fn func(manage.TxStore) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// TxCatalogs runs fn in one transaction over a Repo bound to it, for
// disabling a catalog with its managed bindings.
func (r *Repo) TxCatalogs(ctx context.Context, fn func(admin.TxStore) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// TxCatalogWrite runs fn in one transaction over a Repo bound to it, for the
// catalog write-through flow: catalog, binding, grant and registry rows commit
// together.
func (r *Repo) TxCatalogWrite(ctx context.Context, fn func(*Repo) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// TxSetup runs fn in one transaction over a Repo bound to it, so the
// first-admin check and insert commit together (#375).
func (r *Repo) TxSetup(ctx context.Context, fn func(setup.Store) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// TxDirectory runs fn in one transaction over a Repo bound to it, so a
// directory reconcile pass applies all of its suspensions, reinstatements and
// mappings together (#257).
func (r *Repo) TxDirectory(ctx context.Context, fn func(*Repo) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}
