package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/galileostudio/baleia/internal/workspaces/spaces"
)

// TestSpacesRoundTrip (#40, #770) exercises the GORM space store: the default
// space, the effective-role listing, creation with its first manager, member
// grants and the content counts.
func TestSpacesRoundTrip(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)

	ctx := context.Background()
	r := New(gdb)
	owner := createAPIKeyUser(t, gdb, "space-owner@example.test")
	member := createAPIKeyUser(t, gdb, "space-member@example.test")
	admin := createAPIKeyUser(t, gdb, "space-admin@example.test")
	if err := gdb.Model(&model.User{}).Where("id = ?", string(admin.ID)).Update("role", "admin").Error; err != nil {
		t.Fatal(err)
	}
	ownerID := PGUUID(string(owner.ID))
	memberID := PGUUID(string(member.ID))
	adminID := PGUUID(string(admin.ID))

	team, err := NewTeams(gdb).CreateTeam(ctx, db.CreateTeamParams{Name: "data", CreatedBy: ownerID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	for u, role := range map[string]string{string(owner.ID): "owner", string(member.ID): "member"} {
		if _, err := NewTeams(gdb).AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: PGUUID(u), Role: role}); err != nil {
			t.Fatalf("AddTeamMember: %v", err)
		}
	}
	ws, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Data", Type: "shared", PolarisCatalogName: "ws_spaces_test", OwnerTeamID: team.ID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	// The default space exists (created by the schema trigger or by Go).
	def, err := r.GetDefaultSpace(ctx, ws.ID)
	if err != nil || !def.IsDefault {
		t.Fatalf("GetDefaultSpace = %+v, %v", def, err)
	}

	// A restricted space, created with its first manager in one transaction.
	var secret db.Space
	err = r.TxSpaces(ctx, func(tx spaces.TxStore) error {
		var err error
		secret, err = tx.CreateSpace(ctx, db.CreateSpaceParams{
			WorkspaceID: ws.ID, Name: "Secret", Slug: "secret", Visibility: "restricted",
			DefaultRole: "viewer", CreatedBy: ownerID,
		})
		if err != nil {
			return err
		}
		_, err = tx.SetSpaceMember(ctx, db.SetSpaceMemberParams{
			SpaceID: secret.ID, PrincipalType: "user", PrincipalID: ownerID, Role: "manager", GrantedBy: ownerID,
		})
		return err
	})
	if err != nil {
		t.Fatalf("TxSpaces: %v", err)
	}

	// The owner sees both (default through workspace visibility, secret as
	// manager); the member sees the default only; the platform admin sees both.
	if rows, err := r.ListSpacesForPrincipal(ctx, db.ListSpacesForPrincipalParams{WorkspaceID: ws.ID, UserID: ownerID}); err != nil || len(rows) != 2 {
		t.Errorf("owner list = %d, %v", len(rows), err)
	} else {
		for _, row := range rows {
			if row.MyRole != "manager" {
				t.Errorf("owner role in %s = %q, want manager", row.Name, row.MyRole)
			}
		}
	}
	if rows, err := r.ListSpacesForPrincipal(ctx, db.ListSpacesForPrincipalParams{WorkspaceID: ws.ID, UserID: memberID}); err != nil || len(rows) != 1 || rows[0].ID != def.ID {
		t.Errorf("member list = %+v, %v", rows, err)
	}
	if rows, err := r.ListSpacesForPrincipal(ctx, db.ListSpacesForPrincipalParams{WorkspaceID: ws.ID, UserID: adminID}); err != nil || len(rows) != 2 {
		t.Errorf("admin list = %d, %v", len(rows), err)
	}
	if role, err := r.GetEffectiveSpaceRole(ctx, db.GetEffectiveSpaceRoleParams{WorkspaceID: ws.ID, SpaceID: secret.ID, UserID: memberID}); err != nil || role != "" {
		t.Errorf("member secret role = %q, %v", role, err)
	}

	// A team grant reaches its members.
	if _, err := r.SetSpaceMember(ctx, db.SetSpaceMemberParams{
		SpaceID: secret.ID, PrincipalType: "team", PrincipalID: team.ID, Role: "editor", GrantedBy: ownerID,
	}); err != nil {
		t.Fatalf("SetSpaceMember(team): %v", err)
	}
	if role, err := r.GetEffectiveSpaceRole(ctx, db.GetEffectiveSpaceRoleParams{WorkspaceID: ws.ID, SpaceID: secret.ID, UserID: memberID}); err != nil || role != "editor" {
		t.Errorf("member secret role after team grant = %q, %v", role, err)
	}
	if members, err := r.ListSpaceMembers(ctx, secret.ID); err != nil || len(members) != 2 {
		t.Errorf("ListSpaceMembers = %d, %v", len(members), err)
	}
	if n, err := r.DeleteSpaceMember(ctx, db.DeleteSpaceMemberParams{SpaceID: secret.ID, PrincipalType: "team", PrincipalID: team.ID}); err != nil || n != 1 {
		t.Errorf("DeleteSpaceMember = %d, %v", n, err)
	}

	// Slug, content counts and update.
	if exists, err := r.SpaceSlugExists(ctx, db.SpaceSlugExistsParams{WorkspaceID: ws.ID, Slug: "secret"}); err != nil || !exists {
		t.Errorf("SpaceSlugExists = %v, %v", exists, err)
	}
	if n, err := r.CountSpaceContent(ctx, secret.ID); err != nil || n != 0 {
		t.Errorf("CountSpaceContent = %d, %v", n, err)
	}
	if kinds, err := r.CountSpaceContentKinds(ctx, secret.ID); err != nil || kinds.SavedQueries != 0 || kinds.SemanticMetrics != 0 || kinds.SemanticModels != 0 || kinds.Folders != 0 {
		t.Errorf("CountSpaceContentKinds = %+v, %v", kinds, err)
	}
	updated, err := r.UpdateSpace(ctx, db.UpdateSpaceParams{
		WorkspaceID: ws.ID, ID: secret.ID, Name: "Secret v2", Visibility: "restricted", DefaultRole: "viewer",
	})
	if err != nil || updated.Name != "Secret v2" {
		t.Errorf("UpdateSpace = %+v, %v", updated, err)
	}

	// The default space is never deleted; a normal one is.
	if n, err := r.DeleteSpace(ctx, db.DeleteSpaceParams{WorkspaceID: ws.ID, ID: def.ID}); err != nil || n != 0 {
		t.Errorf("delete default = %d, %v", n, err)
	}
	if n, err := r.DeleteSpace(ctx, db.DeleteSpaceParams{WorkspaceID: ws.ID, ID: secret.ID}); err != nil || n != 1 {
		t.Errorf("delete secret = %d, %v", n, err)
	}
	if _, err := r.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: ws.ID, ID: secret.ID}); !errors.Is(err, db.ErrNoRows) {
		t.Errorf("deleted GetSpace = %v", err)
	}
}
