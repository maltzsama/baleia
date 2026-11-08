package store

import "gorm.io/gorm"

// Models is the authoritative store schema (ADR 0020): one GORM model per
// table, with tags and associations. Schema transitions are expressed against
// these models. The domains are populated by #769–#774.
func Models() []any { return nil }

// AllMigrations is the versioned schema registry. Additive transitions call
// AutoMigrate over Models; destructive or renaming transitions use GORM's
// Migrator inside their own versioned entry.
func AllMigrations() (*Registry, error) {
	return NewRegistry(
		Migration{
			Version: 1,
			Name:    "create store schema",
			Up: func(db *gorm.DB) error {
				if len(Models()) == 0 {
					return nil
				}
				return db.AutoMigrate(Models()...)
			},
		},
	)
}
