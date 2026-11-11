package store_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"gorm.io/gorm"
)

// oldSpaceKey and oldRunKey declare the two keys as they were: partial indexes.
// PostgreSQL creates the real partial indexes from them. MySQL and MariaDB have
// no partial indexes, so there the WHERE is dropped and they become the keys
// those engines actually had: unique over workspace_id, and over the table.
type oldSpaceKey struct {
	WorkspaceID model.UUID `gorm:"column:workspace_id;uniqueIndex:spaces_one_default,priority:1,where:is_default"`
	IsDefault   bool       `gorm:"column:is_default"`
}

func (oldSpaceKey) TableName() string { return "spaces" }

type oldRunKey struct {
	CatalogID      model.UUID `gorm:"column:catalog_id;uniqueIndex:maintenance_runs_one_running,priority:1,where:status = 'running'"`
	Namespace      model.Text `gorm:"column:namespace;uniqueIndex:maintenance_runs_one_running,priority:2"`
	TableNameValue model.Text `gorm:"column:table_name;uniqueIndex:maintenance_runs_one_running,priority:3"`
	Status         string     `gorm:"column:status"`
}

func (oldRunKey) TableName() string { return "maintenance_runs" }

func fixtureUUID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

func ptr[T any](v T) *T { return &v }

// TestGuardPartialKeysUpgrades takes a database back to the old keys, with the
// rows such a database can hold, applies the migration, and checks the guards,
// the new keys, that what the old MySQL key forbade now works, and that a rerun
// changes nothing. It runs on every engine the test database can be.
func TestGuardPartialKeysUpgrades(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)

	// The fixtures name a workspace and a catalog that do not exist, so the
	// foreign keys that would refuse them go.
	for _, c := range []struct {
		model     any
		relations []string
	}{
		{&model.Space{}, []string{"Workspace", "CreatedByRef"}},
		{&model.MaintenanceRun{}, []string{"Catalog", "Binding", "StartedByRef"}},
	} {
		for _, rel := range c.relations {
			if db.Migrator().HasConstraint(c.model, rel) {
				if err := db.Migrator().DropConstraint(c.model, rel); err != nil {
					t.Fatalf("drop foreign key %s: %v", rel, err)
				}
			}
		}
	}
	// Back to the old keys: the guarded key goes, the old one is rebuilt from
	// its old declaration.
	oldShapes := map[string]any{"spaces": &oldSpaceKey{}, "maintenance_runs": &oldRunKey{}}
	guarded := store.GuardedIndexes()
	for _, g := range guarded {
		if err := db.Migrator().DropIndex(g.Model, g.Index); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrator().CreateIndex(oldShapes[g.Table], g.OldIndex); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC()
	ws, catalog := model.UUID(fixtureUUID(1)), model.UUID(fixtureUUID(2))
	space := func(n int, slug string, isDefault bool) model.Space {
		return model.Space{ID: model.UUID(fixtureUUID(10 + n)), WorkspaceID: ws, Name: model.Text(slug), Visibility: "workspace",
			DefaultRole: "viewer", IsDefault: isDefault, CreatedAt: now, UpdatedAt: now, Slug: model.Text(slug)}
	}
	run := func(n int, status string) model.MaintenanceRun {
		return model.MaintenanceRun{ID: model.UUID(fixtureUUID(30 + n)), CatalogID: catalog, Namespace: "sales", TableNameValue: "orders",
			Operations: model.StringArray{}, Status: status, WorkflowID: "w", Trigger: "manual",
			Reason: model.JSON("{}"), Parameters: model.JSON("{}"), StartedAt: now}
	}
	// One default space and one running run: all the old MySQL keys allowed.
	if err := db.Create(ptr(space(1, "general", true))).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(ptr(run(3, "running"))).Error; err != nil {
		t.Fatal(err)
	}
	// The database as the earlier migrations left it: rows in place and no guard
	// column at all, so the migration has to add both.
	for _, g := range guarded {
		if err := db.Migrator().DropColumn(g.Model, g.GuardField); err != nil {
			t.Fatalf("drop %s: %v", g.GuardField, err)
		}
	}
	var before model.Space
	if err := db.Select("id", "updated_at").Where("workspace_id = ?", string(ws)).First(&before).Error; err != nil {
		t.Fatal(err)
	}

	for range 2 { // the second run proves it is idempotent
		if err := store.GuardPartialKeys(db); err != nil {
			t.Fatalf("guardPartialKeys: %v", err)
		}
	}

	var def model.Space
	if err := db.Where("workspace_id = ?", string(ws)).First(&def).Error; err != nil || def.DefaultGuard == nil || !*def.DefaultGuard {
		t.Fatalf("the default space was not backfilled: %+v, %v", def.DefaultGuard, err)
	}
	var running model.MaintenanceRun
	if err := db.Where("catalog_id = ?", string(catalog)).First(&running).Error; err != nil || running.RunningGuard == nil || !*running.RunningGuard {
		t.Fatalf("the running run was not backfilled: %+v, %v", running.RunningGuard, err)
	}
	for _, g := range guarded {
		if !db.Migrator().HasColumn(g.Model, g.GuardField) {
			t.Errorf("%s.%s was not added", g.Table, g.GuardField)
		}
		if exists, spans, err := store.IndexSpansGuard(db, g.Table, g.Index); err != nil || !exists || !spans {
			t.Errorf("%s: exists = %v, spans the guard = %v, %v", g.Index, exists, spans, err)
		}
		if db.Migrator().HasIndex(g.Model, g.OldIndex) {
			t.Errorf("the old key %s is still there", g.OldIndex)
		}
	}
	// Populating the guard is not an edit of the space.
	var after model.Space
	if err := db.Select("id", "updated_at").Where("id = ?", string(before.ID)).First(&after).Error; err != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updated_at changed from %v to %v (%v)", before.UpdatedAt, after.UpdatedAt, err)
	}

	// What the old MySQL keys forbade now works, and the rules still hold.
	for _, s := range []model.Space{space(2, "private", false), space(3, "third", false)} {
		if err := db.Create(&s).Error; err != nil {
			t.Errorf("a non-default space %s: %v", s.Slug, err)
		}
	}
	if err := db.Create(ptr(space(4, "second-default", true))).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Errorf("a second default space: %v, want a duplicate key", err)
	}
	for _, r := range []model.MaintenanceRun{run(1, "finished"), run(2, "failed")} {
		if err := db.Create(&r).Error; err != nil {
			t.Errorf("a %s run beside the running one: %v", r.Status, err)
		}
	}
	if err := db.Create(ptr(run(4, "running"))).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Errorf("a second running run on the table: %v, want a duplicate key", err)
	}
}
