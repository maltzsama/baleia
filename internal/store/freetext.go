package store

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// Two free-text columns were declared as model.Text with a size, which is
// varchar(size) on MySQL and MariaDB (those engines cannot index or default a
// TEXT column) and text on PostgreSQL. A size of 128 made sense for the "trgm"
// index they carried, which ADR 0020 removed along with pg_trgm, but it left the
// columns capped at 128 characters on MySQL and MariaDB: the history could not
// record a query longer than that, and a semantic description, allowed up to
// 20000 characters, failed the same way ("Data too long"). They are plain text
// now, without the leftover index.

// freeText names one such column.
type freeText struct {
	model any
	table string
	// field is the model's field, column its database name; index is the
	// leftover index that went with the cap.
	field, column, index string
}

func freeTexts() []freeText {
	return []freeText{
		{model: &model.QueryExecution{}, table: "query_executions", field: "SQL", column: "sql", index: "query_executions_sql_trgm"},
		{model: &model.SemanticDoc{}, table: "semantic_docs", field: "Description", column: "description", index: "semantic_docs_description_trgm"},
	}
}

// unboundFreeText drops the leftover indexes and widens the capped columns. It
// is idempotent, and a no-op on a database created from the current models and
// on PostgreSQL, where the columns were already text.
func unboundFreeText(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, c := range freeTexts() {
			// The index first: MySQL cannot widen a column to TEXT while a key
			// without a prefix length covers it.
			if tx.Migrator().HasIndex(c.model, c.index) {
				if err := tx.Migrator().DropIndex(c.model, c.index); err != nil {
					return fmt.Errorf("drop %s: %w", c.index, err)
				}
			}
			capped, err := isCapped(tx, c)
			if err != nil {
				return err
			}
			if capped {
				if err := tx.Migrator().AlterColumn(c.model, c.field); err != nil {
					return fmt.Errorf("widen %s.%s: %w", c.table, c.column, err)
				}
			}
		}
		return nil
	})
}

// dropCapIndexes drops the leftover index of a free-text column from a database
// that still has it, before AutoMigrate reconciles models that include that
// column. Without it an earlier migration, which reads the current model, would
// widen the column under its index and MySQL would refuse ("BLOB/TEXT column used
// in key specification without a key length"), leaving that migration dirty.
func dropCapIndexes(db *gorm.DB, models []any) error {
	for _, c := range freeTexts() {
		for _, m := range models {
			if reflect.TypeOf(m) != reflect.TypeOf(c.model) {
				continue
			}
			if !db.Migrator().HasTable(c.model) || !db.Migrator().HasIndex(c.model, c.index) {
				continue
			}
			if err := db.Migrator().DropIndex(c.model, c.index); err != nil {
				return fmt.Errorf("drop %s: %w", c.index, err)
			}
		}
	}
	return nil
}

// isCapped reports whether the column is a bounded character type.
func isCapped(db *gorm.DB, c freeText) (bool, error) {
	types, err := db.Migrator().ColumnTypes(c.model)
	if err != nil {
		return false, fmt.Errorf("read the columns of %s: %w", c.table, err)
	}
	for _, ct := range types {
		if ct.Name() == c.column {
			return strings.Contains(strings.ToLower(ct.DatabaseTypeName()), "char"), nil
		}
	}
	return false, nil
}
