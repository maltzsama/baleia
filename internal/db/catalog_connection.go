package db

// IsSystemOwned reports whether the platform owns this connection: the default
// Trino cluster the bootstrap seeds, or a connection an operator provides.
//
// Only these may reach a private address through the managed dialer (#609).
// The marker is deliberately not derived from CreatedBy: a deleted user nulls
// that column (ON DELETE SET NULL), and an orphaned user connection must stay
// on the strict dialer (#694).
func (c CatalogConnection) IsSystemOwned() bool {
	return c.SystemOwned
}
