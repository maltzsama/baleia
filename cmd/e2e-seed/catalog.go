//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	trinobinding "github.com/galileostudio/baleia/internal/bindings/trino"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/trino"
)

// ensureTarget exposes Trino's tpch connector as the e2e_tpch catalog on the
// default Trino engine and grants it to the member's and the admin's personal
// workspaces, so the workbench has a runnable target for the `@harness run`
// smoke. The Baleia catalog has the engine catalog's name (#388): `tpch`
// itself is reserved, and a different name on each side made `tpch.*`
// queries fail with CATALOG_NOT_FOUND.
func ensureTarget(ctx context.Context, q db.Querier, admin, member db.User) (db.Catalog, db.CatalogBinding, error) {
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
func ensureTrinoCatalog(ctx context.Context, q db.Querier, trinoURL string) error {
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
	// The dev Trino requires HTTPS and a password (#647): E2E_TRINO_PASSWORD
	// and the CA bundle in E2E_TRINO_CA_FILE.
	var ca string
	if f := os.Getenv("E2E_TRINO_CA_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		ca = string(b)
	}
	hc, err := trino.Authenticated(&http.Client{Transport: &http.Transport{}, Timeout: time.Minute}, user, os.Getenv("E2E_TRINO_PASSWORD"), ca)
	if err != nil {
		return err
	}
	err = trino.NewWithHTTPClient(trinoURL, user, hc).CreateCatalog(ctx, trino.CatalogSpec{Name: e2eTrinoCatalog, Connector: "tpch", Properties: props})
	if errors.Is(err, trino.ErrCatalogExists) {
		return nil
	}
	return err
}

// ensureQueueCapacity pins the default Trino engine's interactive queue to one
// slot so the workload smoke (#545) can make a second run wait. The other
// classes keep their defaults.
func ensureQueueCapacity(ctx context.Context, q db.Querier) error {
	row, err := q.GetDefaultTrinoEngine(ctx)
	if err != nil {
		return err
	}
	return q.UpsertEngineQueueCapacity(ctx, db.UpsertEngineQueueCapacityParams{
		EngineID:       row.Engine.ID,
		Class:          "interactive",
		MaxRunning:     1,
		MaxQueued:      10,
		QueueTimeoutMs: 300000,
	})
}
