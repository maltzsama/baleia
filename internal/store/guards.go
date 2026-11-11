package store

import (
	"fmt"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// Two unique keys used to be partial indexes: one default space per workspace
// (WHERE is_default) and one running maintenance run per table (WHERE status =
// 'running'). MySQL and MariaDB have no partial indexes, and GORM drops the
// WHERE there, so on those engines the keys became a unique key over every row:
// a workspace could hold one space and a table could have one maintenance run
// in its whole history. Both are now plain unique keys over a nullable guard
// column that holds true only where the rule applies, and NULLs never collide
// (ADR 0020: redesigned, not hand-written SQL).

// guardedKey describes one such key.
type guardedKey struct {
	model any
	table string
	// field is the model's guard field, column its database name.
	field, column string
	index         string
	// backfill sets the guard on the rows the rule already applies to.
	backfill func(*gorm.DB) error
}

func guardedKeys() []guardedKey {
	return []guardedKey{
		{
			model: &model.Space{}, table: "spaces", field: "DefaultGuard", column: "default_guard",
			index: "spaces_one_default",
			backfill: func(tx *gorm.DB) error {
				return tx.Model(&model.Space{}).Where("is_default = ?", true).Update("default_guard", true).Error
			},
		},
		{
			model: &model.MaintenanceRun{}, table: "maintenance_runs", field: "RunningGuard", column: "running_guard",
			index: "maintenance_runs_one_running",
			backfill: func(tx *gorm.DB) error {
				return tx.Model(&model.MaintenanceRun{}).Where("status = ?", "running").Update("running_guard", true).Error
			},
		},
	}
}

// guardPartialKeys moves the keys above to their guard column. It is
// idempotent: a database created from the current models already has the
// column and the key, and a rerun finds both.
func guardPartialKeys(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, k := range guardedKeys() {
			if !tx.Migrator().HasColumn(k.model, k.field) {
				if err := tx.Migrator().AddColumn(k.model, k.field); err != nil {
					return fmt.Errorf("add %s.%s: %w", k.table, k.column, err)
				}
			}
			if err := k.backfill(tx); err != nil {
				return fmt.Errorf("backfill %s.%s: %w", k.table, k.column, err)
			}
			exists, spans, err := indexSpans(tx, k)
			if err != nil {
				return err
			}
			if exists && !spans {
				// The old definition: partial on PostgreSQL, over the key columns
				// alone on MySQL and MariaDB.
				if err := tx.Migrator().DropIndex(k.model, k.index); err != nil {
					return fmt.Errorf("drop %s: %w", k.index, err)
				}
			}
			if !exists || !spans {
				if err := tx.Migrator().CreateIndex(k.model, k.index); err != nil {
					return fmt.Errorf("create %s: %w", k.index, err)
				}
			}
		}
		return nil
	})
}

// indexSpans reports whether the key's index exists and whether it already
// includes the guard column.
func indexSpans(db *gorm.DB, k guardedKey) (exists, spans bool, err error) {
	indexes, err := db.Migrator().GetIndexes(k.table)
	if err != nil {
		return false, false, fmt.Errorf("list indexes of %s: %w", k.table, err)
	}
	for _, idx := range indexes {
		if idx.Name() != k.index {
			continue
		}
		for _, col := range idx.Columns() {
			if col == k.column {
				return true, true, nil
			}
		}
		return true, false, nil
	}
	return false, false, nil
}
