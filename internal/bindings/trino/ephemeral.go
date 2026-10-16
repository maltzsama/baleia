package trino

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// EphemeralPrefix names the short-lived catalogs connection tests bind
// (#12); observation and reconciliation never adopt them.
const EphemeralPrefix = "baleia_test_"

// EphemeralStore writes the registry rows of short-lived catalogs.
type EphemeralStore interface {
	InsertEphemeralRegistryRow(ctx context.Context, arg db.InsertEphemeralRegistryRowParams) error
	DeleteEphemeralRegistryRow(ctx context.Context, arg db.DeleteEphemeralRegistryRowParams) error
	ListExpiredEphemeralRegistryRows(ctx context.Context) ([]db.ListExpiredEphemeralRegistryRowsRow, error)
}

// CreateEphemeral binds a connection test's catalog: an enabled registry
// row that expires after ttl, then CREATE CATALOG. The caller drops it with
// DropEphemeral; the sweeper drops what a crash left behind.
func (b *Binder) CreateEphemeral(ctx context.Context, store EphemeralStore, eng bindings.EngineRef, p bindings.Projection, ttl time.Duration) error {
	props, err := json.Marshal(p.Properties)
	if err != nil {
		return fmt.Errorf("marshal properties: %w", err)
	}
	version := p.Version
	if err := store.InsertEphemeralRegistryRow(ctx, db.InsertEphemeralRegistryRowParams{
		ClusterID: eng.ClusterID, CatalogName: p.EngineCatalog, ConnectorName: p.Connector,
		Column4: props, CatalogVersion: &version,
		Ttl: pgtype.Interval{Microseconds: ttl.Microseconds(), Valid: true},
	}); err != nil {
		return fmt.Errorf("insert ephemeral registry row: %w", err)
	}
	return b.Apply(ctx, p)
}

// DropEphemeral drops a test catalog and hard-deletes its registry row
// (it holds real credentials). A catalog already gone is not an error.
func (b *Binder) DropEphemeral(ctx context.Context, store EphemeralStore, eng bindings.EngineRef, name string) error {
	if err := b.admin.DropCatalog(ctx, name); err != nil {
		return fmt.Errorf("drop catalog: %w", err)
	}
	return store.DeleteEphemeralRegistryRow(ctx, db.DeleteEphemeralRegistryRowParams{ClusterID: eng.ClusterID, CatalogName: name})
}

// SweepEphemeral drops every expired test catalog; it returns how many.
func (b *Binder) SweepEphemeral(ctx context.Context, store EphemeralStore) (int, error) {
	rows, err := store.ListExpiredEphemeralRegistryRows(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		if err := b.DropEphemeral(ctx, store, bindings.EngineRef{ClusterID: r.ClusterID}, r.CatalogName); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
