package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
)

func TestEffectiveSpaceRole(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)

	ctx := context.Background()
	now := time.Now().UTC()
	store := New(gdb)

	admin := createAPIKeyUser(t, gdb, "space-admin@example.test")
	admin.Role = "admin"
	if err := gdb.Model(&model.User{}).Where("id = ?", string(admin.ID)).Update("role", "admin").Error; err != nil {
		t.Fatalf("make admin: %v", err)
	}
	owner := createAPIKeyUser(t, gdb, "space-owner@example.test")
	member := createAPIKeyUser(t, gdb, "space-member@example.test")
	viewer := createAPIKeyUser(t, gdb, "space-viewer@example.test")
	outsider := createAPIKeyUser(t, gdb, "space-outsider@example.test")

	team := model.Team{ID: model.UUID(NewUUID()), Name: "Data", CreatedBy: owner.ID, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	for _, tm := range []model.TeamMember{
		{ID: model.UUID(NewUUID()), TeamID: team.ID, UserID: owner.ID, Role: "owner", JoinedAt: now},
		{ID: model.UUID(NewUUID()), TeamID: team.ID, UserID: member.ID, Role: "member", JoinedAt: now},
		{ID: model.UUID(NewUUID()), TeamID: team.ID, UserID: viewer.ID, Role: "member", JoinedAt: now},
	} {
		if err := gdb.Create(&tm).Error; err != nil {
			t.Fatalf("team member: %v", err)
		}
	}

	ws := model.Workspace{
		ID: model.UUID(NewUUID()), Name: "Shared", Type: "shared",
		PolarisCatalogName: model.Text("polaris_space"), OwnerTeamID: &team.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&ws).Error; err != nil {
		t.Fatalf("workspace: %v", err)
	}
	viewerID := viewer.ID
	if err := gdb.Create(&model.WorkspaceMember{
		WorkspaceID: ws.ID, UserID: viewerID, Role: "viewer", GrantedAt: now, Source: "manual",
	}).Error; err != nil {
		t.Fatalf("workspace member: %v", err)
	}

	space := model.Space{
		ID: model.UUID(NewUUID()), WorkspaceID: ws.ID, Name: "Scratch",
		Visibility: "workspace", DefaultRole: "viewer", IsDefault: false,
		Slug: "scratch", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&space).Error; err != nil {
		t.Fatalf("space: %v", err)
	}
	for _, sm := range []model.SpaceMember{
		{SpaceID: space.ID, PrincipalType: "user", PrincipalID: member.ID, Role: "manager", GrantedAt: now},
		{SpaceID: space.ID, PrincipalType: "team", PrincipalID: team.ID, Role: "manager", GrantedAt: now},
	} {
		if err := gdb.Create(&sm).Error; err != nil {
			t.Fatalf("space member: %v", err)
		}
	}

	if role, err := store.GetEffectiveSpaceRole(ctx, db.GetEffectiveSpaceRoleParams{}); err != nil || role != "" {
		t.Fatalf("empty identifiers = %q, %v; want none", role, err)
	}

	cases := []struct {
		name string
		user model.UUID
		want string
	}{
		{"global admin", admin.ID, "manager"}, {"team owner", owner.ID, "manager"},
		{"team member capped by workspace editor", member.ID, "editor"},
		{"workspace viewer", viewer.ID, "viewer"},
		{"outsider", outsider.ID, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := store.GetEffectiveSpaceRole(ctx, db.GetEffectiveSpaceRoleParams{
				WorkspaceID: PGUUID(string(ws.ID)), SpaceID: PGUUID(string(space.ID)), UserID: PGUUID(string(tc.user)),
			})
			if err != nil {
				t.Fatalf("GetEffectiveSpaceRole: %v", err)
			}
			if got != tc.want {
				t.Fatalf("GetEffectiveSpaceRole = %q, want %q", got, tc.want)
			}
		})
	}
}
