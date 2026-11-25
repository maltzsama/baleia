//go:build e2e

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/bikeys"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/google/uuid"
)

// The data policy e2e (ADR 0028, #1145): an Iceberg REST catalog in the dev
// Polaris, a table with rows from two regions, and policies that give the
// member the EU rows and the viewer the US rows with e-mails masked.
const (
	governedCatalog   = "e2e_governed"
	governedPolaris   = "e2e_governed"
	governedTable     = governedCatalog + ".sales.customers"
	governedLocation  = "s3://baleia/e2e-governed"
	governedMaskValue = "***"
)

// seedDataPolicies wires everything the data policy e2e reads. It runs
// through Baleia's API, as an admin would, except for the Polaris catalog
// (the catalog manager's own API) and the rows (written through Trino as
// Baleia's admin identity, which data policies never filter).
type seedDataPolicies struct {
	q        *repo.Repo
	apiURL   string
	polaris  string
	password string
	trino    *trino.Client
	token    string
}

func (s *seedDataPolicies) run(ctx context.Context, admin, member, viewer db.User) error {
	if err := s.polarisCatalog(ctx); err != nil {
		return fmt.Errorf("polaris catalog: %w", err)
	}
	if err := s.login(ctx, admin.Email); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	engine, err := s.q.GetDefaultTrinoEngine(ctx)
	if err != nil {
		return err
	}
	engineID := uuid.UUID(engine.Engine.ID.Bytes).String()
	if err := s.api(ctx, http.MethodPatch, "/engines/"+engineID, map[string]any{"data_policies": true}, nil); err != nil {
		return fmt.Errorf("flag the default engine: %w", err)
	}
	catalog, err := s.catalog(ctx, engineID)
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	// The personal workspaces (the BI keys') and the shared one, where the
	// editor opens.
	shared, err := s.q.GetWorkspaceByPolarisName(ctx, "e2e_shared")
	if err != nil {
		return fmt.Errorf("shared workspace: %w", err)
	}
	workspaces := []db.UUID{shared.ID}
	for _, user := range []db.User{admin, member, viewer} {
		workspaceID, err := s.q.PersonalWorkspaceID(ctx, user.ID)
		if err != nil {
			return err
		}
		workspaces = append(workspaces, workspaceID)
	}
	for _, workspaceID := range workspaces {
		if _, err := s.q.UpsertWorkspaceCatalogGrant(ctx, db.UpsertWorkspaceCatalogGrantParams{
			WorkspaceID: workspaceID, CatalogID: catalog.ID, GrantedBy: admin.ID,
		}); err != nil {
			return fmt.Errorf("grant: %w", err)
		}
	}
	if err := s.rows(ctx); err != nil {
		return fmt.Errorf("rows: %w", err)
	}
	// The editor's table allowlist (#614) reads the stored metadata; seed the
	// table instead of waiting for a sync.
	if _, err := s.q.UpsertMetadataTable(ctx, db.UpsertMetadataTableParams{
		CatalogID: catalog.ID, Namespace: []string{"sales"}, Name: "customers",
		Kind: "table", Fingerprint: "e2e", Properties: []byte(`{}`),
	}); err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	catalogID := uuid.UUID(catalog.ID.Bytes).String()
	if err := s.policies(ctx, catalogID, member, viewer); err != nil {
		return fmt.Errorf("policies: %w", err)
	}
	if err := s.publish(ctx, catalogID, engineID); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	for _, u := range []struct {
		user db.User
		env  string
	}{{member, "E2E_BI_MEMBER_KEY_FILE"}, {viewer, "E2E_BI_VIEWER_KEY_FILE"}} {
		if err := s.biKey(ctx, u.user, engine.Engine.ID, os.Getenv(u.env)); err != nil {
			return fmt.Errorf("BI key %s: %w", u.user.Email, err)
		}
	}
	log.Printf("e2e-seed: data policies on %s (member: EU, viewer: US with e-mails masked)", governedTable)
	return nil
}

// polarisCatalog creates the Polaris catalog, on the dev object store with
// static credentials (no STS on RustFS), and lets its admin role write.
func (s *seedDataPolicies) polarisCatalog(ctx context.Context) error {
	form := url.Values{
		"grant_type": {"client_credentials"}, "client_id": {"admin"},
		"client_secret": {"admin"}, "scope": {"PRINCIPAL_ROLE:ALL"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.polaris+"/api/catalog/v1/oauth/tokens", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Polaris-Realm", "POLARIS")
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := doJSON(req, &tok, http.StatusOK); err != nil {
		return err
	}
	polaris := func(method, path string, body any, ok ...int) error {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, s.polaris+"/api/management/v1/catalogs"+path, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		req.Header.Set("Polaris-Realm", "POLARIS")
		req.Header.Set("Content-Type", "application/json")
		return doJSON(req, nil, ok...)
	}
	err = polaris(http.MethodPost, "", map[string]any{"catalog": map[string]any{
		"name": governedPolaris, "type": "INTERNAL",
		"properties": map[string]string{"default-base-location": governedLocation},
		"storageConfigInfo": map[string]any{
			"storageType": "S3", "allowedLocations": []string{governedLocation},
			"endpoint": "http://rustfs:9000", "endpointInternal": "http://rustfs:9000",
			"pathStyleAccess": true, "stsUnavailable": true, "region": "us-east-1",
		},
	}}, http.StatusCreated, http.StatusConflict)
	if err != nil {
		return err
	}
	for _, privilege := range []string{"CATALOG_MANAGE_CONTENT", "TABLE_WRITE_DATA"} {
		err := polaris(http.MethodPut, "/"+governedPolaris+"/catalog-roles/catalog_admin/grants",
			map[string]any{"grant": map[string]string{"privilege": privilege, "type": "catalog"}},
			http.StatusCreated, http.StatusOK)
		if err != nil {
			return fmt.Errorf("grant %s: %w", privilege, err)
		}
	}
	return nil
}

func (s *seedDataPolicies) login(ctx context.Context, email string) error {
	var out struct {
		Token string `json:"token"`
	}
	if err := s.api(ctx, http.MethodPost, "/auth/login", map[string]string{"email": email, "password": s.password}, &out); err != nil {
		return err
	}
	s.token = out.Token
	return nil
}

// catalog registers the Polaris catalog through a connection bound to the
// default engine, once.
func (s *seedDataPolicies) catalog(ctx context.Context, engineID string) (db.Catalog, error) {
	cluster, err := s.q.GetDefaultTrinoCluster(ctx)
	if err != nil {
		return db.Catalog{}, err
	}
	lookup := db.GetCatalogByTrinoNameParams{ClusterID: cluster.ID, TrinoCatalogName: governedCatalog}
	if cat, err := s.q.GetCatalogByTrinoName(ctx, lookup); err == nil {
		return cat, nil
	}
	err = s.api(ctx, http.MethodPost, "/catalog-connections", map[string]any{
		"name": "e2e-polaris",
		"type": "polaris",
		"config": map[string]string{
			"url": "http://polaris:8181", "client_id": "admin",
			"storage.endpoint": "http://rustfs:9000", "storage.region": "us-east-1",
			"storage.path_style": "true", "storage.access_key": "rustfsadmin",
		},
		"credentials": map[string]string{"client_secret": "admin", "storage.secret_key": "rustfsadmin"},
		"engine_ids":  []string{engineID},
		"catalog_registration": map[string]any{
			"catalogs":  []map[string]string{{"name": "E2E Governed", "trino_catalog_name": governedCatalog}},
			"warehouse": governedPolaris,
		},
	}, nil)
	if err != nil {
		return db.Catalog{}, err
	}
	return s.q.GetCatalogByTrinoName(ctx, lookup)
}

// rows writes four customers, two per region, once.
func (s *seedDataPolicies) rows(ctx context.Context) error {
	for _, sql := range []string{
		"CREATE SCHEMA IF NOT EXISTS " + governedCatalog + ".sales",
		"CREATE TABLE IF NOT EXISTS " + governedTable + " (id integer, region varchar, email varchar)",
	} {
		if _, err := s.trino.Execute(ctx, sql, "", ""); err != nil {
			return err
		}
	}
	res, err := s.trino.Execute(ctx, "SELECT count(*) FROM "+governedTable, "", "")
	if err != nil {
		return err
	}
	if len(res.Data) == 1 && fmt.Sprint(res.Data[0][0]) != "0" {
		return nil
	}
	_, err = s.trino.Execute(ctx, "INSERT INTO "+governedTable+` VALUES
		(1, 'EU', 'ana@example.eu'), (2, 'EU', 'bo@example.eu'),
		(3, 'US', 'cy@example.us'), (4, 'US', 'di@example.us')`, "", "")
	return err
}

// policies creates the member's and the viewer's policies, by name, once.
func (s *seedDataPolicies) policies(ctx context.Context, catalogID string, member, viewer db.User) error {
	type existingPolicy struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Validation string `json:"validation"`
		Version    int64  `json:"version"`
	}
	var existing struct {
		Policies []existingPolicy `json:"policies"`
	}
	if err := s.api(ctx, http.MethodGet, "/catalogs/"+catalogID+"/data-policies", nil, &existing); err != nil {
		return err
	}
	have := map[string]existingPolicy{}
	for _, p := range existing.Policies {
		have[p.Name] = p
	}
	subject := func(u db.User) []map[string]string {
		return []map[string]string{{"user": uuid.UUID(u.ID.Bytes).String()}}
	}
	for _, p := range []map[string]any{
		{"name": "member-eu", "kind": "row_filter", "mode": "permissive", "subjects": subject(member), "expression": "region = 'EU'"},
		{"name": "viewer-us", "kind": "row_filter", "mode": "permissive", "subjects": subject(viewer), "expression": "region = 'US'"},
		{"name": "viewer-email", "kind": "column_mask", "column": "email", "subjects": subject(viewer), "expression": "'" + governedMaskValue + "'", "template": "constant"},
	} {
		p["schema"], p["table"] = "sales", "customers"
		prior, ok := have[p["name"].(string)]
		switch {
		case !ok:
			if err := s.api(ctx, http.MethodPost, "/catalogs/"+catalogID+"/data-policies", p, nil); err != nil {
				return fmt.Errorf("%s: %w", p["name"], err)
			}
		case prior.Validation == "pending":
			// Saved while the engine could not type-check it: an update
			// checks it again.
			p["version"] = prior.Version
			if err := s.api(ctx, http.MethodPut, "/data-policies/"+prior.ID, p, nil); err != nil {
				return fmt.Errorf("%s: %w", p["name"], err)
			}
		}
	}
	return nil
}

// publish publishes the catalog's policies and waits until the engine's OPA
// reports the published revision as active.
func (s *seedDataPolicies) publish(ctx context.Context, catalogID, engineID string) error {
	var rev struct {
		ID string `json:"id"`
	}
	if err := s.api(ctx, http.MethodPost, "/catalogs/"+catalogID+"/data-policies/publish", map[string]any{}, &rev); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var status struct {
			Engines []struct {
				EngineID       string  `json:"engine_id"`
				ActiveRevision *string `json:"active_revision"`
			} `json:"engines"`
		}
		if err := s.api(ctx, http.MethodGet, "/data-policies/status", nil, &status); err != nil {
			return err
		}
		for _, e := range status.Engines {
			if e.EngineID == engineID && e.ActiveRevision != nil && *e.ActiveRevision == rev.ID {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the engine's OPA did not activate revision %s in time", rev.ID)
		}
		time.Sleep(2 * time.Second)
	}
}

// biKey issues a BI key on the user's personal workspace and writes its
// secret to path, for the gateway e2e. An empty path skips it.
func (s *seedDataPolicies) biKey(ctx context.Context, user db.User, engineID db.UUID, path string) error {
	if path == "" {
		return nil
	}
	workspaceID, err := s.q.PersonalWorkspaceID(ctx, user.ID)
	if err != nil {
		return err
	}
	_, secret, err := bikeys.New(s.q).Create(ctx, bikeys.CreateParams{
		WorkspaceID: workspaceID, EngineID: engineID, ActorID: user.ID, Name: "e2e data policies",
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(secret+"\n"), 0o600)
}

// api calls Baleia's API with the admin's token, decoding a 2xx answer into
// out when it is not nil.
func (s *seedDataPolicies) api(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.apiURL+"/api/v1"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	return doJSON(req, out)
}

// doJSON sends req and decodes the answer into out. ok lists the accepted
// statuses; empty accepts any 2xx.
func doJSON(req *http.Request, out any, ok ...int) error {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	accepted := resp.StatusCode >= 200 && resp.StatusCode < 300
	if len(ok) > 0 {
		accepted = false
		for _, code := range ok {
			accepted = accepted || resp.StatusCode == code
		}
	}
	if !accepted {
		return fmt.Errorf("%s %s = %d: %s", req.Method, req.URL.Path, resp.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 || resp.StatusCode == http.StatusConflict {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.Join(fmt.Errorf("decode %s %s", req.Method, req.URL.Path), err)
	}
	return nil
}
