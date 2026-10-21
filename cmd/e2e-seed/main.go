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
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"

	"github.com/galileostudio/baleia/internal/auth"
	trinobinding "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dbpool"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
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

	catalog, binding, err := ensureTarget(ctx, q, admin, member)
	if err != nil {
		log.Fatalf("e2e-seed: target: %v", err)
	}
	if trinoURL := os.Getenv("E2E_TRINO_URL"); trinoURL != "" {
		if err := ensureTrinoCatalog(ctx, q, trinoURL); err != nil {
			log.Fatalf("e2e-seed: trino catalog: %v", err)
		}
	}
	if err := ensureModels(ctx, pool, q, member, catalog, binding); err != nil {
		log.Fatalf("e2e-seed: models: %v", err)
	}
	if err := ensureV1Metric(ctx, pool, q, member, catalog, binding); err != nil {
		log.Fatalf("e2e-seed: v1 metric: %v", err)
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

// ensureTarget exposes Trino's tpch connector as the e2e_tpch catalog on the
// default Trino engine and grants it to the member's and the admin's personal
// workspaces, so the workbench has a runnable target for the `@harness run`
// smoke. The Baleia catalog has the engine catalog's name (#388): `tpch`
// itself is reserved, and a different name on each side made `tpch.*`
// queries fail with CATALOG_NOT_FOUND.
func ensureTarget(ctx context.Context, q *db.Queries, admin, member db.User) (db.Catalog, db.CatalogBinding, error) {
	row, err := q.GetDefaultTrinoEngine(ctx)
	if err != nil {
		return db.Catalog{}, db.CatalogBinding{}, err
	}

	catalog, err := q.UpsertNativeCatalog(ctx, db.UpsertNativeCatalogParams{
		ConnectionID: row.Engine.ConnectionID,
		Name:         e2eTrinoCatalog,
	})
	if err != nil {
		return db.Catalog{}, db.CatalogBinding{}, err
	}

	binding, err := q.UpsertNativeBinding(ctx, db.UpsertNativeBindingParams{
		CatalogID:         catalog.ID,
		EngineID:          row.Engine.ID,
		EngineCatalogName: e2eTrinoCatalog,
	})
	if err != nil {
		return db.Catalog{}, db.CatalogBinding{}, err
	}

	// The member and the admin can both query it from their own workspace.
	for _, user := range []db.User{member, admin} {
		workspaceID, err := q.PersonalWorkspaceID(ctx, user.ID)
		if err != nil {
			return db.Catalog{}, db.CatalogBinding{}, err
		}
		if _, err := q.UpsertWorkspaceCatalogGrant(ctx, db.UpsertWorkspaceCatalogGrantParams{
			WorkspaceID: workspaceID,
			CatalogID:   catalog.ID,
			Permission:  "read",
			GrantedBy:   admin.ID,
		}); err != nil {
			return db.Catalog{}, db.CatalogBinding{}, err
		}
	}
	return catalog, binding, nil
}

// e2eTrinoCatalog names the seeded catalog in Baleia and in Trino alike; the
// registry reserves the connector names themselves
// (registry_catalog_name_not_reserved), so it cannot be `tpch`.
const e2eTrinoCatalog = "e2e_tpch"

// ensureTrinoCatalog registers the `tpch` connector as a Trino catalog the way a binding
// does (registry row, then CREATE CATALOG), so the target seeded above can
// actually run queries. Without it Trino answers CATALOG_NOT_FOUND and the run
// smokes (#106, #121) cannot pass. Trino's built-in tpch connector needs no
// external service.
func ensureTrinoCatalog(ctx context.Context, q *db.Queries, trinoURL string) error {
	cluster, err := q.GetDefaultTrinoCluster(ctx)
	if err != nil {
		return err
	}
	// One property is required: CREATE CATALOG ... WITH () is a syntax error.
	props := map[string]string{"tpch.splits-per-node": "4"}
	raw, err := json.Marshal(props)
	if err != nil {
		return err
	}
	version := trinobinding.Version(e2eTrinoCatalog, "tpch", props)
	if err := q.UpsertTrinoCatalogRegistry(ctx, db.UpsertTrinoCatalogRegistryParams{
		ClusterID: cluster.ID, CatalogName: e2eTrinoCatalog, ConnectorName: "tpch",
		Column4: raw, CatalogVersion: &version,
	}); err != nil {
		return err
	}
	user := os.Getenv("E2E_TRINO_ADMIN_USER")
	if user == "" {
		user = "baleia_admin"
	}
	err = trino.New(trinoURL, user).CreateCatalog(ctx, trino.CatalogSpec{Name: e2eTrinoCatalog, Connector: "tpch", Properties: props})
	if errors.Is(err, trino.ErrCatalogExists) {
		return nil
	}
	return err
}

// seedModels are the semantic models (#121) the Explore smoke explores: two
// tpch tables joined many_to_one, written through the content service so they
// get a version like any model created through the API.
var seedModels = []struct {
	slug, label, definition string
}{
	{
		slug: "e2e_customers", label: "E2E Customers",
		definition: `{"source":{"kind":"table","namespace":["tiny"],"table":"customer"},
			"primary_key":["custkey"],
			"dimensions":[{"name":"custkey","type":"int64"},{"name":"nation","sql":"nationkey","type":"int64"}],
			"metrics":[{"name":"customer_count","label":"Customers","aggregation":"count"}]}`,
	},
	{
		slug: "e2e_orders", label: "E2E Orders",
		definition: `{"source":{"kind":"table","namespace":["tiny"],"table":"orders"},
			"primary_key":["orderkey"],
			"dimensions":[{"name":"orderkey","type":"int64"},{"name":"custkey_fk","sql":"custkey","type":"int64"},
				{"name":"order_status","label":"Order status","sql":"orderstatus","type":"string"},
				{"name":"order_date","label":"Order date","sql":"orderdate","type":"date","grains":["month","year"]}],
			"metrics":[{"name":"order_count","label":"Orders","aggregation":"count"},
				{"name":"total_price","label":"Total price","aggregation":"sum","dimension":"orderkey"}],
			"joins":[{"to":"e2e_customers","relationship":"many_to_one","on":[{"from":"custkey_fk","to":"custkey"}]}]}`,
	},
}

// ensureModels creates the e2e models in the member's default space; an
// existing slug is left untouched so the seed stays idempotent.
func ensureModels(ctx context.Context, pool *pgxpool.Pool, q *db.Queries, member db.User, catalog db.Catalog, binding db.CatalogBinding) error {
	workspaceID, err := q.PersonalWorkspaceID(ctx, member.ID)
	if err != nil {
		return err
	}
	space, err := q.GetDefaultSpace(ctx, workspaceID)
	if err != nil {
		return err
	}
	svc := content.New(pool, q)
	for _, m := range seedModels {
		_, err := q.GetSemanticModelBySlug(ctx, db.GetSemanticModelBySlugParams{WorkspaceID: workspaceID, Slug: m.slug})
		if err == nil {
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := svc.CreateSemanticModel(ctx, member.ID, db.CreateSemanticModelParams{
			WorkspaceID: workspaceID, SpaceID: space.ID, Slug: m.slug, Label: m.label,
			CatalogID: catalog.ID, BindingID: binding.ID,
			Definition: []byte(m.definition), FormatVersion: 1,
			OwnerUserID: member.ID, UpdatedBy: member.ID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ensureV1Metric seeds one v1 metric on a table no model uses, so the
// migration dialog (#121) has something to migrate.
func ensureV1Metric(ctx context.Context, pool *pgxpool.Pool, q *db.Queries, member db.User, catalog db.Catalog, binding db.CatalogBinding) error {
	workspaceID, err := q.PersonalWorkspaceID(ctx, member.ID)
	if err != nil {
		return err
	}
	if _, err := q.GetSemanticMetricBySlug(ctx, db.GetSemanticMetricBySlugParams{WorkspaceID: workspaceID, Slug: "e2e_nation_count"}); err == nil {
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	space, err := q.GetDefaultSpace(ctx, workspaceID)
	if err != nil {
		return err
	}
	_, err = content.New(pool, q).CreateSemanticMetric(ctx, member.ID, db.CreateSemanticMetricParams{
		WorkspaceID: workspaceID, SpaceID: space.ID, Slug: "e2e_nation_count", Label: "E2E Nations",
		CatalogID: catalog.ID, Namespace: []string{"tiny"}, TableName: "nation", BindingID: binding.ID,
		Definition: []byte(`{"type":"simple","aggregation":"count"}`), FormatVersion: 1,
		OwnerUserID: member.ID, UpdatedBy: member.ID,
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
