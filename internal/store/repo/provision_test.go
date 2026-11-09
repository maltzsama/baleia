package repo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"gorm.io/gorm"
)

// TestApplyGroupWorkspaceRoles (#653, #769) checks the Go replacement for
// baleia_apply_group_workspace_roles: only shared workspaces of the user's
// teams, idp_group rows written/updated/dropped, a manual role never touched.
func TestApplyGroupWorkspaceRoles(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)

	ctx := context.Background()
	r := New(gdb)
	user := createAPIKeyUser(t, gdb, "provision-user@example.test")
	other := createAPIKeyUser(t, gdb, "provision-other@example.test")
	userID := PGUUID(string(user.ID))

	teams := NewTeams(gdb)
	team, err := teams.CreateTeam(ctx, db.CreateTeamParams{Name: "data", CreatedBy: userID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := teams.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: userID, Role: "member"}); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}
	otherTeam, err := teams.CreateTeam(ctx, db.CreateTeamParams{Name: "other", CreatedBy: PGUUID(string(other.ID))})
	if err != nil {
		t.Fatalf("CreateTeam(other): %v", err)
	}

	ws, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Data", Type: "shared", PolarisCatalogName: "ws_provision", OwnerTeamID: team.ID})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	foreign, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Foreign", Type: "shared", PolarisCatalogName: "ws_foreign", OwnerTeamID: otherTeam.ID})
	if err != nil {
		t.Fatalf("CreateWorkspace(foreign): %v", err)
	}
	personal, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Mine", Type: "personal", PolarisCatalogName: "ws_personal", OwnerUserID: userID})
	if err != nil {
		t.Fatalf("CreateWorkspace(personal): %v", err)
	}

	apply := func(assignments ...struct {
		WorkspaceID string `json:"workspace_id"`
		Role        string `json:"role"`
	}) error {
		raw, err := json.Marshal(assignments)
		if err != nil {
			return err
		}
		return r.ApplyGroupWorkspaceRoles(ctx, db.ApplyGroupWorkspaceRolesParams{UserID: userID, Assignments: raw})
	}
	type assignment = struct {
		WorkspaceID string `json:"workspace_id"`
		Role        string `json:"role"`
	}

	// Grants the team's shared workspace, ignores a foreign team's and a
	// personal one.
	if err := apply(
		assignment{WorkspaceID: UUIDString(ws.ID), Role: "editor"},
		assignment{WorkspaceID: UUIDString(foreign.ID), Role: "admin"},
		assignment{WorkspaceID: UUIDString(personal.ID), Role: "admin"},
	); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if role, err := r.WorkspaceRole(ctx, UUIDString(ws.ID), string(user.ID)); err != nil || role != "editor" {
		t.Errorf("granted role = %q, %v", role, err)
	}
	if member, err := workspaceMember(ctx, gdb, UUIDString(ws.ID), string(user.ID)); err != nil || member.Source != "idp_group" {
		t.Errorf("granted source = %q, %v", member.Source, err)
	}
	if role, _ := r.WorkspaceRole(ctx, UUIDString(foreign.ID), string(user.ID)); role != "" {
		t.Errorf("foreign role = %q", role)
	}
	if _, err := workspaceMember(ctx, gdb, UUIDString(personal.ID), string(user.ID)); !errors.Is(err, db.ErrNoRows) {
		t.Errorf("personal membership written: %v", err)
	}

	// A manual role is never overwritten, and a group role is updated.
	if err := gdb.Model(&model.WorkspaceMember{}).
		Where("workspace_id = ? AND user_id = ?", UUIDString(ws.ID), string(user.ID)).
		Updates(map[string]any{"source": "manual", "role": "admin"}).Error; err != nil {
		t.Fatalf("manual membership: %v", err)
	}
	if err := apply(assignment{WorkspaceID: UUIDString(ws.ID), Role: "viewer"}); err != nil {
		t.Fatalf("apply manual: %v", err)
	}
	if member, err := workspaceMember(ctx, gdb, UUIDString(ws.ID), string(user.ID)); err != nil || member.Role != "admin" || member.Source != "manual" {
		t.Errorf("manual role = %q/%q, %v", member.Role, member.Source, err)
	}

	// Without the assignment the idp_group row goes; the manual one stays.
	extra := createAPIKeyUser(t, gdb, "provision-extra@example.test")
	extraID := PGUUID(string(extra.ID))
	if err := gdb.Create(&model.WorkspaceMember{
		WorkspaceID: model.UUID(UUIDString(ws.ID)), UserID: model.UUID(UUIDString(extraID)),
		Role: "editor", Source: "idp_group", GrantedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed idp_group: %v", err)
	}
	if err := r.ApplyGroupWorkspaceRoles(ctx, db.ApplyGroupWorkspaceRolesParams{UserID: extraID, Assignments: []byte("[]")}); err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	if _, err := workspaceMember(ctx, gdb, UUIDString(ws.ID), UUIDString(extraID)); !errors.Is(err, db.ErrNoRows) {
		t.Errorf("stale idp_group row = %v", err)
	}
	if _, err := workspaceMember(ctx, gdb, UUIDString(ws.ID), UUIDString(userID)); err != nil {
		t.Errorf("manual row dropped: %v", err)
	}
}

// TestDirectoryRoundTrip covers the directory bookkeeping and team upsert the
// provisioning path uses.
func TestDirectoryRoundTrip(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)

	ctx := context.Background()
	users := NewUsers(gdb)
	teams := NewTeams(gdb)
	admin := createAPIKeyUser(t, gdb, "directory-admin@example.test")
	adminID := PGUUID(string(admin.ID))

	provider, err := NewProviders(gdb).CreateAuthProvider(ctx, db.CreateAuthProviderParams{
		Name: "corp", Type: "ldap", ConfigEnc: "x", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAuthProvider: %v", err)
	}
	if list, err := NewProviders(gdb).ListEnabledLDAPProviders(ctx); err != nil || len(list) != 1 {
		t.Errorf("ListEnabledLDAPProviders = %v, %v", list, err)
	}

	run, err := users.CreateDirectoryReconcileRun(ctx, provider.ID)
	if err != nil {
		t.Fatalf("CreateDirectoryReconcileRun: %v", err)
	}
	outcome := "success"
	if err := users.FinishDirectoryReconcileRun(ctx, db.FinishDirectoryReconcileRunParams{
		ID: run.ID, Outcome: &outcome, Seen: 3, Actions: []byte("[]"),
	}); err != nil {
		t.Fatalf("FinishDirectoryReconcileRun: %v", err)
	}
	runs, err := users.ListDirectoryReconcileRuns(ctx, db.ListDirectoryReconcileRunsParams{Limit: 10})
	if err != nil || len(runs) != 1 || runs[0].Seen != 3 || runs[0].Outcome == nil || *runs[0].Outcome != "success" {
		t.Errorf("ListDirectoryReconcileRuns = %+v, %v", runs, err)
	}

	// The team upsert adds once and changes the role after that.
	team, err := teams.CreateTeam(ctx, db.CreateTeamParams{Name: "ops", CreatedBy: adminID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	for _, role := range []string{"member", "owner"} {
		if err := teams.UpsertTeamMember(ctx, db.UpsertTeamMemberParams{TeamID: team.ID, UserID: adminID, Role: role}); err != nil {
			t.Fatalf("UpsertTeamMember(%s): %v", role, err)
		}
	}
	if got, err := New(gdb).GetTeamMemberRole(ctx, db.GetTeamMemberRoleParams{TeamID: team.ID, UserID: adminID}); err != nil || got != "owner" {
		t.Errorf("GetTeamMemberRole = %q, %v", got, err)
	}
}

func workspaceMember(ctx context.Context, gdb *gorm.DB, workspaceID, userID string) (model.WorkspaceMember, error) {
	var row model.WorkspaceMember
	err := gdb.WithContext(ctx).
		Where("workspace_id = ? AND user_id = ?", workspaceID, userID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.WorkspaceMember{}, db.ErrNoRows
	}
	return row, err
}
