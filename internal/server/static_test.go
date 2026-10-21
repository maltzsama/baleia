package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
)

type fakeStaticUsers struct {
	users []db.User
	err   error
}

func (f fakeStaticUsers) ListUsers(context.Context) ([]db.User, error) {
	return f.users, f.err
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
	users := fakeStaticUsers{}
	if configured {
		users.users = []db.User{{}}
	}
	return staticHandler(users, dir)
}

func serve(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// TestStaticRootServesTheApp guards #219: the root used to list web/build
// (login.html, sql.html, …), and those .html links landed on the SPA's 404.
func TestStaticRootServesTheApp(t *testing.T) {
	rec := serve(staticFixture(t, true), http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "<app-shell>" {
		t.Fatalf("GET / body = %q, want the app shell", body)
	}
}

func TestStaticHTMLRedirectsToTheRoute(t *testing.T) {
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

func TestStaticSPAFallback(t *testing.T) {
	rec := serve(staticFixture(t, true), http.MethodGet, "/sql")
	if rec.Code != http.StatusOK || rec.Body.String() != "<app-shell>" {
		t.Fatalf("GET /sql = %d %q, want the app shell", rec.Code, rec.Body.String())
	}
}

func TestStaticSetupFlow(t *testing.T) {
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
	rec = serve(staticHandler(fakeStaticUsers{users: []db.User{{}}}, dir), http.MethodGet, "/asset.js")
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Fatalf("GET /asset.js = %d %q", rec.Code, rec.Body.String())
	}
}

// A route that is also a build directory (/connections holds the prerendered
// connections/new.html) used to answer with the directory listing.
func TestStaticDirectoryRouteServesTheApp(t *testing.T) {
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
	handler := staticHandler(fakeStaticUsers{users: []db.User{{}}}, dir)
	for _, target := range []string{"/connections", "/connections/"} {
		rec := serve(handler, http.MethodGet, target)
		if rec.Code != http.StatusOK || rec.Body.String() != "<app-shell>" {
			t.Fatalf("GET %s = %d %q, want the app shell", target, rec.Code, rec.Body.String())
		}
	}
}
