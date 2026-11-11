package store

import "gorm.io/gorm"

// GuardPartialKeys exposes migration 17 to the external tests.
var GuardPartialKeys = guardPartialKeys

// IndexSpansGuard reports whether the named index of a guarded table exists and
// includes that table's guard column.
func IndexSpansGuard(db *gorm.DB, table, index string) (exists, spans bool, err error) {
	for _, k := range guardedKeys() {
		if k.table == table {
			return indexSpans(db, k, index)
		}
	}
	return false, false, nil
}

// GuardedIndex describes one guarded key for the external tests.
type GuardedIndex struct {
	Table, Index, OldIndex, GuardField string
	Model                              any
}

// GuardedIndexes lists the guarded keys and the indexes they replace.
func GuardedIndexes() []GuardedIndex {
	var out []GuardedIndex
	for _, k := range guardedKeys() {
		out = append(out, GuardedIndex{Table: k.table, Index: k.index, OldIndex: k.oldIndex, Model: k.model, GuardField: k.field})
	}
	return out
}

// UnboundFreeText exposes migration 18 to the external tests.
var UnboundFreeText = unboundFreeText
