package store

import (
	"fmt"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// renameTrinoAccessMode turns the access mode "trino" into "federated" (ADR
// 0029): Trino is an engine kind, not an execution path. GORM owns the
// constraint, so the old check goes, the rows are rewritten while nothing
// constrains the column, and the check is recreated from the model tag. The
// steps share a transaction where the backend has transactional DDL
// (PostgreSQL); MySQL and MariaDB commit DDL per statement. Rows already
// federated, direct or NULL are untouched, so a rerun changes nothing.
func renameTrinoAccessMode(db *gorm.DB) error {
	const name = "catalog_connections_access_mode_check"
	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Migrator().HasConstraint(&model.CatalogConnection{}, name) {
			if err := tx.Migrator().DropConstraint(&model.CatalogConnection{}, name); err != nil {
				return fmt.Errorf("drop access mode check: %w", err)
			}
		}
		if err := tx.Model(&model.CatalogConnection{}).
			Where("access_mode = ?", "trino").
			UpdateColumn("access_mode", "federated").Error; err != nil {
			return fmt.Errorf("rename trino access mode: %w", err)
		}
		if err := tx.Migrator().CreateConstraint(&model.CatalogConnection{}, name); err != nil {
			return fmt.Errorf("create access mode check: %w", err)
		}
		return nil
	})
}
