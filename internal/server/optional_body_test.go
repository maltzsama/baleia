package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestOptionalJSONBody: an empty POST to an optional-body operation reaches
// the handler as {}; a body that was sent, and other routes, are untouched.
func TestOptionalJSONBody(t *testing.T) {
	t.Parallel()
	var got string
	h := OptionalJSONBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	}))
	cases := []struct{ path, body, want string }{
		{"/api/v1/auth/logout", "", "{}"},
		{"/api/v1/auth/refresh", "", "{}"},
		{"/api/v1/workspaces/w1/retrievers/r1/refresh", "", "{}"},
		{"/api/v1/workspaces/w1/retrievers/r1/refresh", `{"refresh_type":"incremental"}`, `{"refresh_type":"incremental"}`},
		{"/api/v1/auth/login", "", ""},
	}
	for _, tc := range cases {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
		if got != tc.want {
			t.Errorf("POST %s with %q: handler read %q, want %q", tc.path, tc.body, got, tc.want)
		}
	}
	// A chunked body (unknown length) is passed through whole.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", io.MultiReader(strings.NewReader(`{"refresh_token":"x"}`)))
	req.ContentLength = -1
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != `{"refresh_token":"x"}` {
		t.Errorf("chunked body read as %q", got)
	}
}

// TestOptionalBodyRoutesMatchSpec keeps optionalBodyRoutes equal to the
// operations whose requestBody api/swagger.yaml does not require.
func TestOptionalBodyRoutesMatchSpec(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../api/swagger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	param := regexp.MustCompile(`\{[^}]+\}`)
	var want []string
	for path, ops := range spec.Paths {
		for method, v := range ops {
			op, ok := v.(map[string]any)
			if !ok {
				continue // parameters
			}
			body, ok := op["requestBody"].(map[string]any)
			if !ok || body["required"] == true {
				continue
			}
			if method != "post" {
				t.Errorf("%s %s: optional bodies are handled for POST only", method, path)
			}
			want = append(want, "/api/v1"+param.ReplaceAllString(path, "x"))
		}
	}
	sort.Strings(want)
	for _, p := range want {
		if !matchesOptionalBody(p) {
			t.Errorf("%s has an optional body in the spec but is not in optionalBodyRoutes", p)
		}
	}
	if len(want) != len(optionalBodyRoutes) {
		t.Errorf("spec has %d optional-body operations, optionalBodyRoutes %d", len(want), len(optionalBodyRoutes))
	}
}
