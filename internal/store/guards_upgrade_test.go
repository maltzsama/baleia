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
	for _, c := range []struct {
		model any
		old   any
		index string
	}{
		{&model.Space{}, &oldSpaceKey{}, "spaces_one_default"},
		{&model.MaintenanceRun{}, &oldRunKey{}, "maintenance_runs_one_running"},
	} {
		if err := db.Migrator().DropIndex(c.model, c.index); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrator().CreateIndex(c.old, c.index); err != nil {
			t.Fatal(err)
		}
		if exists, spans, err := store.IndexSpansGuard(db, c.index); err != nil || !exists || spans {
			t.Fatalf("%s should be in its old shape: exists = %v, spans the guard = %v, %v", c.index, exists, spans, err)
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
	// Rows as the earlier migrations left them: no guard yet.
	if err := db.Model(&model.Space{}).Where("workspace_id = ?", string(ws)).Update("default_guard", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.MaintenanceRun{}).Where("catalog_id = ?", string(catalog)).Update("running_guard", nil).Error; err != nil {
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
	for _, index := range []string{"spaces_one_default", "maintenance_runs_one_running"} {
		if exists, spans, err := store.IndexSpansGuard(db, index); err != nil || !exists || !spans {
			t.Errorf("%s: exists = %v, spans the guard = %v, %v", index, exists, spans, err)
		}
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
