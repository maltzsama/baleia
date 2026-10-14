package db

// The Trino columns of catalogs are set for source catalogs and NULL for
// native ones (migration 00025). These accessors read them as values, "" when
// absent, for the Trino code paths that only ever see source catalogs.

// TrinoName is the catalog's name inside the default Trino cluster.
func (c Catalog) TrinoName() string { return deref(c.TrinoCatalogName) }

// WarehouseName is the Iceberg warehouse or database the catalog projects.
func (c Catalog) WarehouseName() string { return deref(c.Warehouse) }

// ConnectorName is the Trino connector of the catalog.
func (c Catalog) ConnectorName() string { return deref(c.Connector) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
