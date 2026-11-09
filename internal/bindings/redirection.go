package bindings

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
)

// RedirectionStore reads what Hive table redirection needs.
type RedirectionStore interface {
	ListBindingsByEngine(ctx context.Context, engineID db.UUID) ([]db.CatalogBinding, error)
	GetCatalogByID(ctx context.Context, id db.UUID) (db.Catalog, error)
}

// HiveRedirection fills the Trino table-redirection properties of a
// hive-connector binding (#86). When a metastore connection also exposes an
// Iceberg or Delta Lake catalog on the same engine, queries on the Hive
// catalog transparently reach those tables:
// https://trino.io/docs/484/connector/hive.html#table-redirection
//
// Only a Hive Metastore or Glue source bound through the "hive" connector is
// affected. Sidings must already be active on the engine, so the property
// never names a catalog Trino cannot load. extra is mutated in place; the
// caller merges it over the projection.
func HiveRedirection(ctx context.Context, store RedirectionStore, srcKind connections.Kind, connID, engineID, catalogID db.UUID, connector string, extra map[string]string) error {
	if connector != "hive" || (srcKind != connections.KindHiveMetastore && srcKind != connections.KindGlue) {
		return nil
	}
	rows, err := store.ListBindingsByEngine(ctx, engineID)
	if err != nil {
		return err
	}
	for _, b := range rows {
		if !b.Enabled || b.LifecycleState != StateActive || b.Connector == nil || b.CatalogID == catalogID {
			continue
		}
		if *b.Connector != "iceberg" && *b.Connector != "delta_lake" {
			continue
		}
		cat, err := store.GetCatalogByID(ctx, b.CatalogID)
		if errors.Is(err, db.ErrNoRows) {
			continue // the binding outlived its catalog: not a sibling
		}
		if err != nil {
			return err
		}
		if cat.ConnectionID != connID || !cat.Enabled {
			continue
		}
		if *b.Connector == "iceberg" {
			extra["hive.iceberg-catalog-name"] = b.EngineCatalogName
		} else {
			extra["hive.delta-lake-catalog-name"] = b.EngineCatalogName
		}
	}
	return nil
}
