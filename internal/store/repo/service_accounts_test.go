package repo

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"gorm.io/gorm"
)

// TestServiceAccountsRoundTrip (#653, #769) exercises the GORM replacement for
// service_accounts.sql: creation with its workspace membership in one
// transaction, rollback, the listings, the admin-workspace lookup, the
// identity-provider link and deletion.
func TestServiceAccountsRoundTrip(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)

	ctx := context.Background()
	accounts := NewServiceAccounts(gdb)
	admin := createAPIKeyUser(t, gdb, "sa-admin@example.test")
	outsider := createAPIKeyUser(t, gdb, "sa-outsider@example.test")
	adminID := PGUUID(string(admin.ID))
	outsiderID := PGUUID(string(outsider.ID))

	teams := NewTeams(gdb)
	team, err := teams.CreateTeam(ctx, db.CreateTeamParams{Name: "ops", CreatedBy: adminID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := teams.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: adminID, Role: "owner"}); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}

	ws, err := New(gdb).CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Ops", Type: "shared", PolarisCatalogName: "ws_sa_test", OwnerTeamID: team.ID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	// Created with its membership in one transaction.
	var sa db.User
	err = accounts.Tx(ctx, func(w ServiceAccountWrites) error {
		var err error
		sa, err = w.CreateServiceAccount(ctx, db.CreateServiceAccountParams{
			Name: "Reporting bot", Email: "reporting-bot@service-accounts.baleia.invalid",
			AuthProvider: "service", HomeWorkspaceID: ws.ID,
		})
		if err != nil {
			return err
		}
		n, err := w.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{
			WorkspaceID: ws.ID, UserID: sa.ID, Role: "editor", GrantedBy: adminID,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("membership rows = %d", n)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if sa.Kind != "service_account" || sa.Role != "member" {
		t.Errorf("created = kind %q role %q", sa.Kind, sa.Role)
	}

	// A failure rolls the account and its membership back together.
	sentinel := errors.New("deliberate rollback")
	var rolled db.User
	err = accounts.Tx(ctx, func(w ServiceAccountWrites) error {
		var err error
		rolled, err = w.CreateServiceAccount(ctx, db.CreateServiceAccountParams{
			Name: "Rollback", Email: "rollback@service-accounts.baleia.invalid",
			AuthProvider: "service", HomeWorkspaceID: ws.ID,
		})
		if err != nil {
			return err
		}
		if _, err := w.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{
			WorkspaceID: ws.ID, UserID: rolled.ID, Role: "viewer", GrantedBy: adminID,
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("rollback tx = %v", err)
	}
	if _, err := accounts.GetServiceAccount(ctx, rolled.ID); !errors.Is(err, db.ErrNoRows) {
		t.Errorf("rolled-back account = %v", err)
	}
	if count := membershipCount(t, gdb, ws.ID, rolled.ID); count != 0 {
		t.Errorf("rolled-back membership rows = %d", count)
	}

	if got, err := accounts.GetServiceAccount(ctx, sa.ID); err != nil || got.ID != sa.ID {
		t.Errorf("GetServiceAccount = %v, %v", got.ID, err)
	}

	// The workspace listing covers an account created there (home), one merely
	// added to it (membership), and excludes one created elsewhere.
	homeOnly, err := accounts.CreateServiceAccount(ctx, db.CreateServiceAccountParams{
		Name: "Home bot", Email: "home@service-accounts.baleia.invalid",
		AuthProvider: "service", HomeWorkspaceID: ws.ID,
	})
	if err != nil {
		t.Fatalf("CreateServiceAccount(home): %v", err)
	}
	memberOnly, err := accounts.CreateServiceAccount(ctx, db.CreateServiceAccountParams{
		Name: "Member bot", Email: "member@service-accounts.baleia.invalid",
		AuthProvider: "service",
	})
	if err != nil {
		t.Fatalf("CreateServiceAccount(member): %v", err)
	}
	if n, err := accounts.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{
		WorkspaceID: ws.ID, UserID: memberOnly.ID, Role: "viewer", GrantedBy: adminID,
	}); err != nil || n != 1 {
		t.Fatalf("member-only membership = %d, %v", n, err)
	}
	otherWS, err := New(gdb).CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Other", Type: "shared", PolarisCatalogName: "ws_sa_other", OwnerTeamID: team.ID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace(other): %v", err)
	}
	unrelated, err := accounts.CreateServiceAccount(ctx, db.CreateServiceAccountParams{
		Name: "Unrelated bot", Email: "unrelated@service-accounts.baleia.invalid",
		AuthProvider: "service", HomeWorkspaceID: otherWS.ID,
	})
	if err != nil {
		t.Fatalf("CreateServiceAccount(unrelated): %v", err)
	}

	if list, err := accounts.ListServiceAccounts(ctx); err != nil || len(list) != 4 {
		t.Errorf("ListServiceAccounts = %d, %v", len(list), err)
	}
	list, err := accounts.ListServiceAccountsOfWorkspaces(ctx, []db.UUID{ws.ID})
	if err != nil {
		t.Fatalf("ListServiceAccountsOfWorkspaces: %v", err)
	}
	got := make(map[string]bool, len(list))
	for _, row := range list {
		got[UUIDString(row.ID)] = true
	}
	want := map[string]bool{UUIDString(sa.ID): true, UUIDString(homeOnly.ID): true, UUIDString(memberOnly.ID): true}
	if len(got) != len(want) {
		t.Errorf("ListServiceAccountsOfWorkspaces = %v, want %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("missing account %s", id)
		}
	}
	if got[UUIDString(unrelated.ID)] {
		t.Errorf("unrelated account listed")
	}

	// The team owner administers the workspace; the outsider does not.
	if ids, err := accounts.ListAdminWorkspaceIDs(ctx, adminID); err != nil || len(ids) != 2 {
		t.Errorf("ListAdminWorkspaceIDs(admin) = %v, %v", ids, err)
	}
	if ids, err := accounts.ListAdminWorkspaceIDs(ctx, outsiderID); err != nil || len(ids) != 0 {
		t.Errorf("ListAdminWorkspaceIDs(outsider) = %v, %v", ids, err)
	}

	// A person outside the team cannot be written to a shared workspace.
	if n, err := accounts.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{
		WorkspaceID: ws.ID, UserID: outsiderID, Role: "viewer", GrantedBy: adminID,
	}); err != nil || n != 0 {
		t.Errorf("outsider membership = %d, %v", n, err)
	}

	// An identity-provider client links one service account; a second is a
	// conflict.
	provider, err := NewProviders(gdb).CreateAuthProvider(ctx, db.CreateAuthProviderParams{
		Name: "corp", Type: "oidc", ConfigEnc: "x", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAuthProvider: %v", err)
	}
	external := "client:reporting-bot"
	updated, err := accounts.UpdateServiceAccount(ctx, db.UpdateServiceAccountParams{
		ID: sa.ID, Name: "Reporting bot", AuthProvider: "oidc",
		AuthProviderID: provider.ID, ExternalID: &external,
	})
	if err != nil {
		t.Fatalf("UpdateServiceAccount: %v", err)
	}
	if updated.AuthProviderID != provider.ID || updated.ExternalID == nil || *updated.ExternalID != external {
		t.Errorf("link = %v / %v", updated.AuthProviderID, updated.ExternalID)
	}
	_, err = accounts.CreateServiceAccount(ctx, db.CreateServiceAccountParams{
		Name: "Duplicate", Email: "dup@service-accounts.baleia.invalid",
		AuthProvider: "oidc", AuthProviderID: provider.ID, ExternalID: &external,
	})
	pgErr, ok := db.AsPGError(err)
	if !ok || pgErr.Code != uniqueViolation {
		t.Errorf("duplicate client = %v", err)
	}

	// Deleting it takes the membership with it.
	if n, err := accounts.DeleteServiceAccount(ctx, sa.ID); err != nil || n != 1 {
		t.Fatalf("DeleteServiceAccount = %d, %v", n, err)
	}
	if _, err := accounts.GetServiceAccount(ctx, sa.ID); !errors.Is(err, db.ErrNoRows) {
		t.Errorf("deleted get = %v", err)
	}
	if role, err := New(gdb).WorkspaceRole(ctx, UUIDString(ws.ID), UUIDString(sa.ID)); err != nil || role != "" {
		t.Errorf("deleted role = %q, %v", role, err)
	}
	if count := membershipCount(t, gdb, ws.ID, sa.ID); count != 0 {
		t.Errorf("deleted membership rows = %d", count)
	}
}

// membershipCount counts the workspace rows of a user directly, so a deletion
// that leaves the junction row behind is caught even when the role reads back
// empty for other reasons.
func membershipCount(t *testing.T, gdb *gorm.DB, workspaceID, userID db.UUID) int64 {
	t.Helper()
	var count int64
	if err := gdb.Model(&model.WorkspaceMember{}).
		Where("workspace_id = ? AND user_id = ?", UUIDString(workspaceID), UUIDString(userID)).
		Count(&count).Error; err != nil {
		t.Fatalf("count membership: %v", err)
	}
	return count
}
