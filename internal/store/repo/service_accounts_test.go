package repo

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// TestServiceAccountsRoundTrip (#653, #769) exercises the GORM replacement for
// service_accounts.sql: creation with its workspace membership in one
// transaction, the listings, the admin-workspace lookup, the identity-provider
// link and deletion.
func TestServiceAccountsRoundTrip(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:         gormlogger.Discard,
		TranslateError: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

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

	if got, err := accounts.GetServiceAccount(ctx, sa.ID); err != nil || got.ID != sa.ID {
		t.Errorf("GetServiceAccount = %v, %v", got.ID, err)
	}
	if list, err := accounts.ListServiceAccounts(ctx); err != nil || len(list) != 1 {
		t.Errorf("ListServiceAccounts = %v, %v", list, err)
	}
	if list, err := accounts.ListServiceAccountsOfWorkspaces(ctx, []pgtype.UUID{ws.ID}); err != nil || len(list) != 1 {
		t.Errorf("ListServiceAccountsOfWorkspaces = %v, %v", list, err)
	}

	// The team owner administers the workspace; the outsider does not.
	if ids, err := accounts.ListAdminWorkspaceIDs(ctx, adminID); err != nil || len(ids) != 1 || UUIDString(ids[0]) != UUIDString(ws.ID) {
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
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
		t.Errorf("duplicate client = %v", err)
	}

	// Deleting it takes the membership with it.
	if n, err := accounts.DeleteServiceAccount(ctx, sa.ID); err != nil || n != 1 {
		t.Fatalf("DeleteServiceAccount = %d, %v", n, err)
	}
	if _, err := accounts.GetServiceAccount(ctx, sa.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("deleted get = %v", err)
	}
	if role, err := New(gdb).WorkspaceRole(ctx, UUIDString(ws.ID), UUIDString(sa.ID)); err != nil || role != "" {
		t.Errorf("deleted role = %q, %v", role, err)
	}
}
