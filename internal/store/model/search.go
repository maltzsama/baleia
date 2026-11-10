package model

import (
	"database/sql/driver"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Term is a folded word of the search index, at most 64 runes. PostgreSQL
// compares it by bytes (collation C), which lets a btree index serve prefix
// lookups on a database collated en_US; MySQL and MariaDB serve them as is.
type Term string

// GormDataType is the name GORM uses when no dialect overrides it.
func (Term) GormDataType() string { return "string" }

// GormDBDataType resolves the column type per dialect.
func (Term) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Name() == "postgres" {
		return `varchar(64) COLLATE "C"`
	}
	return "varchar(64)"
}

// Scan implements sql.Scanner.
func (t *Term) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*t = ""
	case string:
		*t = Term(v)
	case []byte:
		*t = Term(v)
	default:
		return fmt.Errorf("model.Term: cannot scan %T", value)
	}
	return nil
}

// Value implements driver.Valuer.
func (t Term) Value() (driver.Value, error) { return string(t), nil }

// SearchScope is one scope of the search index (ADR 0023): a catalog's
// metadata, or a workspace's docs or content. Fingerprint is the state of the
// scope's source rows its terms were built from; a different fingerprint
// means the terms are rebuilt. Deleting the catalog or workspace removes it.
type SearchScope struct {
	ScopeKey    string     `gorm:"column:scope_key;primaryKey;size:80;not null"`
	CatalogID   *UUID      `gorm:"column:catalog_id;index:idx_search_scopes_catalog,priority:1"`
	WorkspaceID *UUID      `gorm:"column:workspace_id;index:idx_search_scopes_workspace,priority:1"`
	Fingerprint string     `gorm:"column:fingerprint;not null;size:255;default:''"`
	IndexedAt   *time.Time `gorm:"column:indexed_at"`

	Catalog   *Catalog   `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
	Workspace *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName is the table of SearchScope.
func (SearchScope) TableName() string { return "search_scopes" }

// SearchTerm is one folded word of a row's searchable texts in a scope of the
// search index (ADR 0023). Kind and RefID name the row; the primary key
// serves exact and prefix lookups of a term within a scope.
type SearchTerm struct {
	ScopeKey string `gorm:"column:scope_key;primaryKey;size:80;not null"`
	Term     Term   `gorm:"column:term;primaryKey;not null"`
	Kind     string `gorm:"column:kind;primaryKey;size:24;not null"`
	RefID    string `gorm:"column:ref_id;primaryKey;size:80;not null"`

	Scope *SearchScope `gorm:"foreignKey:ScopeKey;references:ScopeKey;constraint:OnDelete:CASCADE"`
}

// TableName is the table of SearchTerm.
func (SearchTerm) TableName() string { return "search_terms" }
