package store_test

import (
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"gorm.io/gorm"
)

// oldExecSQL and oldDocDescription declare the two columns as they were: capped
// at 128 characters, with the index that went with the cap. On MySQL and MariaDB
// that is varchar(128); on PostgreSQL the column was text all along.
type oldExecSQL struct {
	SQL model.Text `gorm:"column:sql;not null;size:128;index:query_executions_sql_trgm,priority:1"`
}

func (oldExecSQL) TableName() string { return "query_executions" }

type oldDocDescription struct {
	Description model.Text `gorm:"column:description;not null;size:128;default:'';index:semantic_docs_description_trgm,priority:1"`
}

func (oldDocDescription) TableName() string { return "semantic_docs" }

// dropForeignKeys removes the foreign keys of a model, so a row can name a
// workspace and a catalog that do not exist.
func dropForeignKeys(t *testing.T, db *gorm.DB, m any) {
	t.Helper()
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(m); err != nil {
		t.Fatal(err)
	}
	for name := range stmt.Schema.Relationships.Relations {
		if db.Migrator().HasConstraint(m, name) {
			if err := db.Migrator().DropConstraint(m, name); err != nil {
				t.Fatalf("drop foreign key %s: %v", name, err)
			}
		}
	}
}

func capped(t *testing.T, db *gorm.DB, m any, column string) bool {
	t.Helper()
	types, err := db.Migrator().ColumnTypes(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, ct := range types {
		if ct.Name() == column {
			return strings.Contains(strings.ToLower(ct.DatabaseTypeName()), "char")
		}
	}
	t.Fatalf("no column %s", column)
	return false
}

// TestUnboundFreeTextUpgrades takes a database back to the capped columns, with
// a row in each, applies the migration twice, and checks that the caps and the
// leftover indexes are gone, that the rows survived, and that long text now fits.
// It runs on every engine the test database can be.
func TestUnboundFreeTextUpgrades(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	dropForeignKeys(t, db, &model.QueryExecution{})
	dropForeignKeys(t, db, &model.SemanticDoc{})

	for _, c := range []struct {
		old    any
		model  any
		field  string
		index  string
		column string
	}{
		{&oldExecSQL{}, &model.QueryExecution{}, "SQL", "query_executions_sql_trgm", "sql"},
		{&oldDocDescription{}, &model.SemanticDoc{}, "Description", "semantic_docs_description_trgm", "description"},
	} {
		if err := db.Migrator().AlterColumn(c.old, c.field); err != nil {
			t.Fatalf("cap %s: %v", c.column, err)
		}
		if err := db.Migrator().CreateIndex(c.old, c.index); err != nil {
			t.Fatalf("create %s: %v", c.index, err)
		}
		if !db.Migrator().HasIndex(c.model, c.index) {
			t.Fatalf("%s should be there before the migration", c.index)
		}
	}

	now := time.Now().UTC()
	uuid := func(n int) model.UUID { return model.UUID(fixtureUUID(n)) }
	if err := db.Create(&model.QueryExecution{
		ID: uuid(1), WorkspaceID: uuid(2), UserID: uuid(3), Status: "finished", Origin: "editor", SQL: "select 1", StartedAt: now,
	}).Error; err != nil {
		t.Fatalf("a short query in the capped column: %v", err)
	}
	if err := db.Create(&model.SemanticDoc{
		ID: uuid(4), WorkspaceID: uuid(2), CatalogID: uuid(5), Namespace: model.StringArray{"sales"}, TableNameValue: "orders",
		Description: "Orders.", Tags: model.StringArray{}, UpdatedBy: uuid(3), UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("a short description in the capped column: %v", err)
	}

	for range 2 { // the second run proves it is idempotent
		if err := store.UnboundFreeText(db); err != nil {
			t.Fatalf("UnboundFreeText: %v", err)
		}
	}

	for _, c := range []struct {
		model  any
		index  string
		column string
	}{
		{&model.QueryExecution{}, "query_executions_sql_trgm", "sql"},
		{&model.SemanticDoc{}, "semantic_docs_description_trgm", "description"},
	} {
		if db.Migrator().HasIndex(c.model, c.index) {
			t.Errorf("the leftover index %s is still there", c.index)
		}
		if capped(t, db, c.model, c.column) {
			t.Errorf("%s is still a capped column", c.column)
		}
	}

	var exec model.QueryExecution
	if err := db.First(&exec, "id = ?", string(uuid(1))).Error; err != nil || exec.SQL != "select 1" {
		t.Fatalf("the query did not survive: %q, %v", exec.SQL, err)
	}
	var doc model.SemanticDoc
	if err := db.First(&doc, "id = ?", string(uuid(4))).Error; err != nil || doc.Description != "Orders." {
		t.Fatalf("the description did not survive: %q, %v", doc.Description, err)
	}

	long := strings.Repeat("x", 20000)
	if err := db.Create(&model.QueryExecution{
		ID: uuid(6), WorkspaceID: uuid(2), UserID: uuid(3), Status: "finished", Origin: "editor", SQL: long, StartedAt: now,
	}).Error; err != nil {
		t.Errorf("a long query after the migration: %v", err)
	}
	if err := db.Create(&model.SemanticDoc{
		ID: uuid(7), WorkspaceID: uuid(2), CatalogID: uuid(5), Namespace: model.StringArray{"sales"}, TableNameValue: "customers",
		Description: long, Tags: model.StringArray{}, UpdatedBy: uuid(3), UpdatedAt: now,
	}).Error; err != nil {
		t.Errorf("a long description after the migration: %v", err)
	}
}
