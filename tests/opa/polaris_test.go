//go:build opa

package opa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// polarisAPI calls Polaris through a port-forward.
type polarisAPI struct {
	base string
}

func (p polarisAPI) do(t testing.TB, method, path, token string, body any, header map[string]string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, p.base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Polaris-Realm", "POLARIS")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(raw)
}

// tokenFrom posts a token request and returns the access token.
func tokenFrom(t testing.TB, endpoint string, form url.Values) string {
	t.Helper()
	resp, err := http.PostForm(endpoint, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken == "" {
		t.Fatalf("token from %s: %s %s", endpoint, body.Error, body.Description)
	}
	return body.AccessToken
}

// Q8. Polaris-OPA mode: Polaris 1.9 with OIDC users (credential-mode
// external) and its OPA authorizer. Keycloak adds the baleia_session role only
// to tokens issued to Baleia's client; the Polaris policy requires it to load
// a policied table. The same user's token from another client (Spark,
// PyIceberg) is refused that table and keeps every other one.
func TestPolaris_PolicedTableNeedsABaleiaSession(t *testing.T) {
	mustKubectl(t, "apply", "-k", filepath.Join(repoRoot(), "deploy", "dev", "opa-spike-polaris"))
	if err := waitRollout(t.Context(), "keycloak", "polaris"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	p := polarisAPI{base: "http://" + forward(t, "svc/polaris", 8181)}
	keycloak := "http://" + forward(t, "svc/keycloak", 8080) + "/realms/baleia/protocol/openid-connect/token"

	root := tokenFrom(t, p.base+"/api/catalog/v1/oauth/tokens", url.Values{
		"grant_type": {"client_credentials"}, "client_id": {"root"}, "client_secret": {"s3cr3t"}, "scope": {"PRINCIPAL_ROLE:ALL"},
	})
	setup := []struct {
		path string
		body any
	}{
		{"/api/management/v1/catalogs", map[string]any{"catalog": map[string]any{
			"name": "lake", "type": "INTERNAL",
			"properties":        map[string]string{"default-base-location": "file:///tmp/lake"},
			"storageConfigInfo": map[string]any{"storageType": "FILE", "allowedLocations": []string{"file:///tmp/lake"}},
		}}},
		{"/api/catalog/v1/lake/namespaces", map[string]any{"namespace": []string{"sales"}}},
	}
	for _, table := range []string{"orders", "items"} {
		setup = append(setup, struct {
			path string
			body any
		}{"/api/catalog/v1/lake/namespaces/sales/tables", map[string]any{
			"name":   table,
			"schema": map[string]any{"type": "struct", "fields": []any{map[string]any{"id": 1, "name": "id", "type": "int", "required": false}}},
		}})
	}
	for _, s := range setup {
		if code, body := p.do(t, http.MethodPost, s.path, root, s.body, nil); code >= 300 && code != http.StatusConflict {
			t.Fatalf("POST %s: %d %s", s.path, code, body)
		}
	}

	tokens := map[string]string{}
	for client, secret := range map[string]string{"baleia": "baleia-secret", "spark": "spark-secret"} {
		tokens[client] = tokenFrom(t, keycloak, url.Values{
			"grant_type": {"password"}, "client_id": {client}, "client_secret": {secret},
			"username": {"ana"}, "password": {"ana-password"}, "scope": {"openid"},
		})
	}
	cases := []struct {
		client, table string
		expected      int
	}{
		{"baleia", "orders", http.StatusOK},
		{"baleia", "items", http.StatusOK},
		{"spark", "orders", http.StatusForbidden},
		{"spark", "items", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s token loads %s", tc.client, tc.table), func(t *testing.T) {
			code, body := p.do(t, http.MethodGet, "/api/catalog/v1/lake/namespaces/sales/tables/"+tc.table, tokens[tc.client], nil,
				map[string]string{"X-Iceberg-Access-Delegation": "vended-credentials"})
			if code != tc.expected {
				t.Errorf("HTTP %d, want %d: %.300s", code, tc.expected, body)
			}
		})
	}

	// What Polaris's OPA authorizer sent for ana, one decision per client.
	out := mustKubectl(t, "logs", "deploy/polaris", "-c", "opa", "--since-time="+start.Add(-time.Second).UTC().Format(time.RFC3339))
	seen := map[string]bool{}
	for line := range strings.Lines(out) {
		var d struct {
			Input struct {
				Actor struct {
					Principal  string            `json:"principal"`
					Roles      []string          `json:"roles"`
					Attributes map[string]string `json:"attributes"`
				} `json:"actor"`
				Action string `json:"action"`
			} `json:"input"`
			Result any `json:"result"`
		}
		if !strings.Contains(line, `"Decision Log"`) || json.Unmarshal([]byte(line), &d) != nil || d.Input.Actor.Principal != "ana" {
			continue
		}
		key := fmt.Sprint(d.Input.Actor.Roles) + d.Input.Action
		if !seen[key] {
			seen[key] = true
			t.Logf("actor=%+v action=%s result=%v", d.Input.Actor, d.Input.Action, d.Result)
		}
	}
	if len(seen) == 0 {
		t.Error("no OPA decision for ana")
	}
}
