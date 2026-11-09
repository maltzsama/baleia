//go:build e2e

// e2e-seed creates the deterministic users, team and workspaces the Playwright
// smoke suite logs in with (#106). It uses the generated queries and the auth
// package — the same code paths as the API — not raw SQL. It is idempotent and
// is never built into the release image (build tag `e2e`).
package main

import (
	"context"
	"log"
	"os"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/repo"
)

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

	backend, err := store.BackendFromDSN(databaseURL)
	if err != nil {
		log.Fatalf("e2e-seed: store backend: %v", err)
	}
	gdb, err := store.Open(ctx, databaseURL, backend, store.Options{})
	if err != nil {
		log.Fatalf("e2e-seed: connect: %v", err)
	}
	defer func() { _ = store.Close(gdb) }()
	q := repo.New(gdb)

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

	catalog, binding, err := ensureTarget(ctx, q, admin, member)
	if err != nil {
		log.Fatalf("e2e-seed: target: %v", err)
	}
	if err := ensureQueueCapacity(ctx, q); err != nil {
		log.Fatalf("e2e-seed: queue capacity: %v", err)
	}
	if trinoURL := os.Getenv("E2E_TRINO_URL"); trinoURL != "" {
		if err := ensureTrinoCatalog(ctx, q, trinoURL); err != nil {
			log.Fatalf("e2e-seed: trino catalog: %v", err)
		}
	}
	contentSvc, err := newContentService(gdb)
	if err != nil {
		log.Fatalf("e2e-seed: content store: %v", err)
	}
	if err := ensureModels(ctx, q, contentSvc, member, catalog, binding); err != nil {
		log.Fatalf("e2e-seed: models: %v", err)
	}
	if err := ensureV1Metric(ctx, q, contentSvc, member, catalog, binding); err != nil {
		log.Fatalf("e2e-seed: v1 metric: %v", err)
	}
	if err := ensureMetadata(ctx, q, catalog); err != nil {
		log.Fatalf("e2e-seed: metadata: %v", err)
	}

	log.Printf("e2e-seed: ready (admin/member/viewer @e2e.test)")
}
