package partitioning

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Partitioning a table by a column requires that column in every unique key
// (ADR 0024). The model is the schema, so the key the table must end up with is
// the one the model declares: the adapters read it from the model's GORM schema
// instead of taking a second description of it.

// modelKey returns the primary key columns the model declares, in order.
func modelKey(db *gorm.DB, model any) ([]string, error) {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(model); err != nil {
		return nil, fmt.Errorf("partitioning: parse model: %w", err)
	}
	cols := make([]string, 0, len(stmt.Schema.PrimaryFields))
	for _, f := range stmt.Schema.PrimaryFields {
		// Some dialects (Oracle) name columns in upper case.
		name := strings.ToLower(f.DBName)
		if !identifier.MatchString(name) {
			return nil, fmt.Errorf("partitioning: invalid key column %q", f.DBName)
		}
		cols = append(cols, name)
	}
	return cols, nil
}

// currentKey returns the name and columns of the table's primary key as the
// database has it, or no columns when the table has none.
func currentKey(db *gorm.DB, table string) (name string, cols []string, err error) {
	indexes, err := db.Migrator().GetIndexes(table)
	if err != nil {
		return "", nil, fmt.Errorf("partitioning: list indexes of %s: %w", table, err)
	}
	for _, idx := range indexes {
		if primary, ok := idx.PrimaryKey(); ok && primary {
			return idx.Name(), idx.Columns(), nil
		}
	}
	return "", nil, nil
}

// keyChange reports the key the model wants and whether the table's current
// key differs from it (same columns, same order, compared case-insensitively
// because Oracle upper-cases unquoted names).
func keyChange(db *gorm.DB, model any, table string) (want []string, current string, change bool, err error) {
	want, err = modelKey(db, model)
	if err != nil {
		return nil, "", false, err
	}
	if len(want) == 0 {
		return nil, "", false, fmt.Errorf("partitioning: model of %s declares no primary key", table)
	}
	name, have, err := currentKey(db, table)
	if err != nil {
		return nil, "", false, err
	}
	return want, name, !sameKey(want, have), nil
}

// sameKey compares two keys column by column, ignoring case.
func sameKey(want, have []string) bool {
	if len(have) != len(want) {
		return false
	}
	for i := range want {
		if !strings.EqualFold(have[i], want[i]) {
			return false
		}
	}
	return true
}
