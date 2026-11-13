package store

import (
	"fmt"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

const accessModeCheck = "catalog_connections_access_mode_check"

// dropAccessModeCheck removes the access-mode check when it exists. GORM owns
// the constraint, so it is changed by dropping it and creating it again from the
// model tag.
func dropAccessModeCheck(tx *gorm.DB) error {
	if !tx.Migrator().HasConstraint(&model.CatalogConnection{}, accessModeCheck) {
		return nil
	}
	if err := tx.Migrator().DropConstraint(&model.CatalogConnection{}, accessModeCheck); err != nil {
		return fmt.Errorf("drop access mode check: %w", err)
	}
	return nil
}

func createAccessModeCheck(tx *gorm.DB) error {
	if err := tx.Migrator().CreateConstraint(&model.CatalogConnection{}, accessModeCheck); err != nil {
		return fmt.Errorf("create access mode check: %w", err)
	}
	return nil
}

// recreateAccessModeCheck replaces the installed check with the one the model
// declares. Migration 8 calls it before writing "federated": a database that
// reaches it from an older version still has the check that allows only
// "direct" and "trino".
func recreateAccessModeCheck(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := dropAccessModeCheck(tx); err != nil {
			return err
		}
		return createAccessModeCheck(tx)
	})
}

// renameTrinoAccessMode turns the access mode "trino" into "federated" (ADR
// 0029): Trino is an engine kind, not an execution path. The old check goes,
// the rows are rewritten while nothing constrains the column, and the check is
// recreated from the model tag. The steps share a transaction where the backend
// has transactional DDL (PostgreSQL); MySQL and MariaDB commit DDL per
// statement. Rows already federated, direct or NULL are untouched, so a rerun
// changes nothing.
func renameTrinoAccessMode(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := dropAccessModeCheck(tx); err != nil {
			return err
		}
		if err := tx.Model(&model.CatalogConnection{}).
			Where("access_mode = ?", "trino").
			UpdateColumn("access_mode", "federated").Error; err != nil {
			return fmt.Errorf("rename trino access mode: %w", err)
		}
		return createAccessModeCheck(tx)
	})
}
