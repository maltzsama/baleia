package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// TestSpaceDefaultGuard: a workspace holds one default space and any number of
// others. MySQL and MariaDB have no partial indexes, so the rule is a unique key
// over the default_guard column, and it has to hold the same on every engine.
func TestSpaceDefaultGuard(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t) // a workspace with its default space and a "Private" one
	now := time.Now().UTC()
	space := func(name, slug string, isDefault bool) model.Space {
		return model.Space{
			ID: model.UUID(NewUUID()), WorkspaceID: model.UUID(string(f.ws)), Name: model.Text(name),
			Visibility: "workspace", DefaultRole: "viewer", IsDefault: isDefault,
			CreatedAt: now, UpdatedAt: now, Slug: model.Text(slug),
		}
	}

	// Many non-default spaces coexist.
	for _, s := range []model.Space{space("Second", "second", false), space("Third", "third", false)} {
		if err := f.gdb.Create(&s).Error; err != nil {
			t.Fatalf("create non-default space %s: %v", s.Slug, err)
		}
	}
	// A second default is refused by the database.
	second := space("Another default", "another-default", true)
	if err := f.gdb.Create(&second).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("a second default space: error = %v, want a duplicate key", err)
	}
	// Another workspace has its own default space.
	ctx := context.Background()
	elsewhere, err := f.r.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Elsewhere", Type: "shared", PolarisCatalogName: "ws_guard_elsewhere", OwnerTeamID: PGUUID(string(f.teamID(t))),
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if _, err := f.r.GetDefaultSpace(ctx, elsewhere.ID); err != nil {
		t.Fatalf("the other workspace has no default space: %v", err)
	}
	// The guard follows the flag, and the existing default keeps it.
	var rows []model.Space
	if err := f.gdb.Where("workspace_id = ?", string(f.ws)).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	defaults := 0
	for _, s := range rows {
		if (s.DefaultGuard != nil) != s.IsDefault {
			t.Errorf("space %s: is_default = %v but guard = %v", s.Slug, s.IsDefault, s.DefaultGuard)
		}
		if s.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("workspace has %d default spaces, want 1", defaults)
	}
	// Editing a space through a map update leaves the guard alone.
	if err := f.gdb.Model(&model.Space{}).Where("workspace_id = ? AND is_default = ?", string(f.ws), true).
		Updates(map[string]any{"description": "edited"}).Error; err != nil {
		t.Fatal(err)
	}
	var kept model.Space
	if err := f.gdb.Where("workspace_id = ? AND is_default = ?", string(f.ws), true).First(&kept).Error; err != nil || kept.DefaultGuard == nil {
		t.Fatalf("the default space lost its guard on update: %+v, %v", kept.DefaultGuard, err)
	}
}
