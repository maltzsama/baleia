//go:build e2e

// e2e-seed creates the deterministic users, team and workspaces the Playwright
// smoke suite logs in with (#106). It uses the generated queries and the auth
// package — the same code paths as the API — not raw SQL. It is idempotent and
// is never built into the release image (build tag `e2e`).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"strings"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dbpool"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type seedUser struct {
	name  string
	email string
	role  string
}

func main() {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgresql://baleia:baleia@localhost:5432/baleia?sslmode=disable"
	}
	password := os.Getenv("E2E_PASSWORD")
	if password == "" {
		password = "e2e-password"
	}

	pool, err := dbpool.Open(ctx, databaseURL, dbpool.Options{MaxConns: 2})
	if err != nil {
		log.Fatalf("e2e-seed: connect: %v", err)
	}
	defer pool.Close()

	q := db.New(pool)

	hash, err := auth.HashPassword(password)
	if err != nil {
		log.Fatalf("e2e-seed: hash password: %v", err)
	}

	users := []seedUser{
		{name: "E2E Admin", email: "admin@e2e.test", role: "admin"},
		{name: "E2E Member", email: "member@e2e.test", role: "member"},
		{name: "E2E Viewer", email: "viewer@e2e.test", role: "member"},
	}

	created := make(map[string]db.User, len(users))
	for _, u := range users {
		user, err := ensureUser(ctx, q, u, hash)
		if err != nil {
			log.Fatalf("e2e-seed: user %s: %v", u.email, err)
		}
		created[u.email] = user
		if err := ensurePersonalWorkspace(ctx, q, user); err != nil {
			log.Fatalf("e2e-seed: personal workspace %s: %v", u.email, err)
		}
	}

	admin := created["admin@e2e.test"]
	member := created["member@e2e.test"]
	viewer := created["viewer@e2e.test"]

	team, err := ensureTeam(ctx, q, "e2e", admin.ID)
	if err != nil {
		log.Fatalf("e2e-seed: team: %v", err)
	}
	if err := ensureTeamMember(ctx, q, team.ID, admin.ID, "owner"); err != nil {
		log.Fatalf("e2e-seed: team admin: %v", err)
	}
	if err := ensureTeamMember(ctx, q, team.ID, member.ID, "member"); err != nil {
		log.Fatalf("e2e-seed: team member: %v", err)
	}
	if err := ensureTeamMember(ctx, q, team.ID, viewer.ID, "member"); err != nil {
		log.Fatalf("e2e-seed: team viewer: %v", err)
	}

	if _, err := ensureSharedWorkspace(ctx, q, "E2E Shared", team.ID); err != nil {
		log.Fatalf("e2e-seed: shared workspace: %v", err)
	}

	if err := ensureTarget(ctx, q, admin, member); err != nil {
		log.Fatalf("e2e-seed: target: %v", err)
	}

	log.Printf("e2e-seed: ready (admin/member/viewer @e2e.test)")
}

func ensureUser(ctx context.Context, q *db.Queries, u seedUser, hash string) (db.User, error) {
	existing, err := q.GetUserByEmail(ctx, u.email)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.User{}, err
	}
	return q.CreateUser(ctx, db.CreateUserParams{
		Name:         u.name,
		Email:        u.email,
		PasswordHash: &hash,
		Role:         u.role,
		AuthProvider: "local",
	})
}

func ensurePersonalWorkspace(ctx context.Context, q *db.Queries, user db.User) error {
	workspaces, err := q.ListWorkspacesByUser(ctx, user.ID)
	if err == nil {
		for _, w := range workspaces {
			if w.Type == "personal" && w.OwnerUserID.Valid && w.OwnerUserID.Bytes == user.ID.Bytes {
				return nil
			}
		}
	}

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:               user.Name + " - workspace",
		Type:               "personal",
		PolarisCatalogName: "e2e_personal_" + stableSuffix(user.ID),
		OwnerUserID:        user.ID,
	})
	if err != nil {
		return err
	}
	return q.UpdateUserPersonalWorkspace(ctx, db.UpdateUserPersonalWorkspaceParams{
		ID:                  user.ID,
		PersonalWorkspaceID: ws.ID,
	})
}

func ensureTeam(ctx context.Context, q *db.Queries, name string, createdBy pgtype.UUID) (db.Team, error) {
	teams, err := q.ListTeams(ctx)
	if err == nil {
		for _, team := range teams {
			if team.Name == name {
				return team, nil
			}
		}
	}
	return q.CreateTeam(ctx, db.CreateTeamParams{Name: name, CreatedBy: createdBy})
}

func ensureTeamMember(ctx context.Context, q *db.Queries, teamID, userID pgtype.UUID, role string) error {
	_, err := q.AddTeamMember(ctx, db.AddTeamMemberParams{
		TeamID: teamID,
		UserID: userID,
		Role:   role,
	})
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return nil
	}
	return err
}

// ensureTarget exposes the built-in `tpch` catalog on the default Trino engine
// and grants it to the member's personal workspace, so the workbench has a
// runnable target for the `@harness run` smoke.
func ensureTarget(ctx context.Context, q *db.Queries, admin, member db.User) error {
	row, err := q.GetDefaultTrinoEngine(ctx)
	if err != nil {
		return err
	}

	catalog, err := q.UpsertNativeCatalog(ctx, db.UpsertNativeCatalogParams{
		ConnectionID: row.Engine.ConnectionID,
		Name:         "tpch",
	})
	if err != nil {
		return err
	}

	if _, err := q.UpsertNativeBinding(ctx, db.UpsertNativeBindingParams{
		CatalogID:         catalog.ID,
		EngineID:          row.Engine.ID,
		EngineCatalogName: "tpch",
	}); err != nil {
		return err
	}

	workspaceID, err := q.PersonalWorkspaceID(ctx, member.ID)
	if err != nil {
		return err
	}

	_, err = q.UpsertWorkspaceCatalogGrant(ctx, db.UpsertWorkspaceCatalogGrantParams{
		WorkspaceID: workspaceID,
		CatalogID:   catalog.ID,
		Permission:  "read",
		GrantedBy:   admin.ID,
	})
	return err
}

func ensureSharedWorkspace(ctx context.Context, q *db.Queries, name string, teamID pgtype.UUID) (db.Workspace, error) {
	polarisName := "e2e_shared"
	if ws, err := q.GetWorkspaceByPolarisName(ctx, polarisName); err == nil {
		return ws, nil
	}
	return q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:               name,
		Type:               "shared",
		PolarisCatalogName: polarisName,
		OwnerTeamID:        teamID,
	})
}

// stableSuffix keeps the legacy polaris_catalog_name unique and stable across
// repeated seed runs.
func stableSuffix(id pgtype.UUID) string {
	if len(id.Bytes) == 0 {
		return randomSuffix()
	}
	return hex.EncodeToString(id.Bytes[:8])
}

func randomSuffix() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "seed"
	}
	return hex.EncodeToString(buf)
}
