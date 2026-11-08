package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestTeamsRoundTrip(t *testing.T) {
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
	store := NewTeams(gdb)
	owner := createAPIKeyUser(t, gdb, "team-owner@example.test")
	member := createAPIKeyUser(t, gdb, "team-member@example.test")
	ownerID := PGUUID(string(owner.ID))
	memberID := PGUUID(string(member.ID))

	team, err := store.CreateTeam(ctx, db.CreateTeamParams{Name: "Data", CreatedBy: ownerID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := store.GetTeamByID(ctx, team.ID); err != nil {
		t.Fatalf("GetTeamByID: %v", err)
	}
	if list, err := store.ListTeams(ctx); err != nil || len(list) != 1 {
		t.Fatalf("ListTeams = %v, %v", list, err)
	}

	if _, err := store.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: ownerID, Role: "owner"}); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}
	if _, err := store.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: memberID, Role: "member"}); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}
	members, err := store.ListTeamMembers(ctx, team.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("ListTeamMembers = %v, %v", members, err)
	}
	teams, err := store.ListTeamsByUser(ctx, memberID)
	if err != nil || len(teams) != 1 {
		t.Fatalf("ListTeamsByUser = %v, %v", teams, err)
	}

	if _, err := store.UpdateTeamMemberRole(ctx, db.UpdateTeamMemberRoleParams{TeamID: team.ID, UserID: memberID, Role: "owner"}); err != nil {
		t.Fatalf("UpdateTeamMemberRole: %v", err)
	}
	repo := New(gdb)
	role, err := repo.GetTeamMemberRole(ctx, db.GetTeamMemberRoleParams{TeamID: team.ID, UserID: memberID})
	if err != nil || role != "owner" {
		t.Fatalf("GetTeamMemberRole = %q, %v; want owner", role, err)
	}
	if _, err := repo.GetTeamMemberRole(ctx, db.GetTeamMemberRoleParams{TeamID: team.ID, UserID: PGUUID(NewUUID())}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetTeamMemberRole(non-member) = %v, want pgx.ErrNoRows", err)
	}
	if n, err := store.RemoveTeamMember(ctx, db.RemoveTeamMemberParams{TeamID: team.ID, UserID: memberID}); err != nil || n != 1 {
		t.Fatalf("RemoveTeamMember = %d, %v", n, err)
	}
	if err := store.DeleteTeam(ctx, team.ID); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
}
