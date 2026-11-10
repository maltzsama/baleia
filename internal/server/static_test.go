package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeStaticUsers struct {
	configured bool
	err        error
	calls      int
}

func (f *fakeStaticUsers) HasUsers(context.Context) (bool, error) {
	f.calls++
	return f.configured, f.err
}

func staticFixture(t *testing.T, configured bool) http.Handler {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"app.html":   "<app-shell>",
		"login.html": "<login-prerender>",
		"sql.html":   "<sql-prerender>",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return staticHandler(&fakeStaticUsers{configured: configured}, dir)
}

func serve(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// TestStaticRootServesTheApp guards #219: the root used to list web/build
// (login.html, sql.html, …), and those .html links landed on the SPA's 404.
func TestStaticRootServesTheApp(t *testing.T) {
	t.Parallel()
	rec := serve(staticFixture(t, true), http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "<app-shell>" {
		t.Fatalf("GET / body = %q, want the app shell", body)
	}
}

func TestStaticHTMLRedirectsToTheRoute(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"/login.html", "/sql.html"} {
		rec := serve(staticFixture(t, true), http.MethodGet, target)
		if rec.Code != http.StatusFound {
			t.Fatalf("GET %s = %d, want 302", target, rec.Code)
		}
		want := target[:len(target)-len(".html")]
		if loc := rec.Header().Get("Location"); loc != want {
			t.Fatalf("GET %s Location = %q, want %q", target, loc, want)
		}
	}
}

// TestStaticHTMLRejectsProtocolRelativeRedirect pins #620: a .html path that
// starts with // (or /\ which browsers normalize to it) must not become an
// off-origin Location.
func TestStaticHTMLRejectsProtocolRelativeRedirect(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"//evil.example/x.html", "/\\evil.example/x.html"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL.Path = target
		rec := httptest.NewRecorder()
		staticFixture(t, true).ServeHTTP(rec, req)
		if rec.Code == http.StatusFound {
			t.Fatalf("path %q redirected to %q", target, rec.Header().Get("Location"))
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Fatalf("path %q set Location %q", target, loc)
		}
	}
}

// TestStaticMovedRoutes: the profile pages left settings; the old URLs
// answer 308 to the fixed new route.
func TestStaticMovedRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct{ target, want string }{
		{"/settings/profile", "/profile"},
		{"/settings/profile/", "/profile"},
		{"/settings/api-keys?next=//evil.example", "/profile/api-keys"},
	}
	for _, tt := range tests {
		rec := serve(staticFixture(t, true), http.MethodGet, tt.target)
		if rec.Code != http.StatusPermanentRedirect {
			t.Fatalf("GET %s = %d, want 308", tt.target, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != tt.want {
			t.Fatalf("GET %s Location = %q, want %q", tt.target, loc, tt.want)
		}
	}
	if rec := serve(staticFixture(t, true), http.MethodGet, "/settings/users"); rec.Code != http.StatusOK {
		t.Fatalf("GET /settings/users = %d, want the app", rec.Code)
	}
}

func TestStaticSPAFallback(t *testing.T) {
	t.Parallel()
	rec := serve(staticFixture(t, true), http.MethodGet, "/sql")
	if rec.Code != http.StatusOK || rec.Body.String() != "<app-shell>" {
		t.Fatalf("GET /sql = %d %q, want the app shell", rec.Code, rec.Body.String())
	}
}

func TestStaticSetupFlow(t *testing.T) {
	t.Parallel()
	// No users yet: the root sends the operator to the setup wizard.
	rec := serve(staticFixture(t, false), http.MethodGet, "/")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/setup" {
		t.Fatalf("GET / without users = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	// Configured: /setup goes back to the app.
	rec = serve(staticFixture(t, true), http.MethodGet, "/setup")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("GET /setup configured = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestStaticAPIAndAssets(t *testing.T) {
	t.Parallel()
	handler := staticFixture(t, true)
	rec := serve(handler, http.MethodGet, "/api/v1/unknown")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api = %d, want 404", rec.Code)
	}
	// A real asset is served as a file, not the app shell.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "asset.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = serve(staticHandler(&fakeStaticUsers{configured: true}, dir), http.MethodGet, "/asset.js")
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Fatalf("GET /asset.js = %d %q", rec.Code, rec.Body.String())
	}
}

// The PWA manifest is only installed when it is served as
// application/manifest+json instead of the text/plain FileServer sniffs.
func TestStaticWebManifestContentType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "site.webmanifest"), []byte(`{"name":"Baleia"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := serve(staticHandler(&fakeStaticUsers{configured: true}, dir), http.MethodGet, "/site.webmanifest")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /site.webmanifest = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Fatalf("GET /site.webmanifest Content-Type = %q, want application/manifest+json", ct)
	}
}

// Crawlers only read /robots.txt as plain text, so it must come from the build
// and never fall back to the app shell (#802). Go has no built-in type for .txt.
func TestStaticRobotsTxt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const robots = "User-agent: *\nAllow: /\n"
	files := map[string]string{"app.html": "<app-shell>", "robots.txt": robots}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := serve(staticHandler(&fakeStaticUsers{configured: true}, dir), http.MethodGet, "/robots.txt")
	if rec.Code != http.StatusOK || rec.Body.String() != robots {
		t.Fatalf("GET /robots.txt = %d %q, want the robots file", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("GET /robots.txt Content-Type = %q, want text/plain", ct)
	}
}

// A route that is also a build directory (/connections holds the prerendered
// connections/new.html) used to answer with the directory listing.
func TestStaticDirectoryRouteServesTheApp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.html"), []byte("<app-shell>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "connections"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "connections", "new.html"), []byte("<new>"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := staticHandler(&fakeStaticUsers{configured: true}, dir)
	for _, target := range []string{"/connections", "/connections/"} {
		rec := serve(handler, http.MethodGet, target)
		if rec.Code != http.StatusOK || rec.Body.String() != "<app-shell>" {
			t.Fatalf("GET %s = %d %q, want the app shell", target, rec.Code, rec.Body.String())
		}
	}
}

// TestStatic_CachesConfigured: once set up, the shell no longer asks the
// database on every page load (#375); before that it keeps asking.
func TestStatic_CachesConfigured(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.html"), []byte("<app-shell>"), 0o644); err != nil {
		t.Fatal(err)
	}
	users := &fakeStaticUsers{}
	handler := staticHandler(users, dir)
	serve(handler, http.MethodGet, "/")
	serve(handler, http.MethodGet, "/")
	if users.calls != 2 {
		t.Fatalf("not set up: %d lookups, want 2", users.calls)
	}
	users.configured = true
	for range 3 {
		serve(handler, http.MethodGet, "/")
	}
	if users.calls != 3 {
		t.Fatalf("set up: %d lookups, want 3 (cached after the first yes)", users.calls)
	}
}
