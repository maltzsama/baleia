package store

import "gorm.io/gorm"

// GuardPartialKeys exposes migration 17 to the external tests.
var GuardPartialKeys = guardPartialKeys

// IndexSpansGuard reports whether a guarded key's index exists and already
// includes its guard column.
func IndexSpansGuard(db *gorm.DB, index string) (exists, spans bool, err error) {
	for _, k := range guardedKeys() {
		if k.index == index {
			return indexSpans(db, k)
		}
	}
	return false, false, nil
}
