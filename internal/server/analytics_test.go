package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/config"
)

const (
	testWebsiteID = "4cff4cb4-3fb0-4c64-b9c8-275ddc250873"
	realisticHead = `<!doctype html><html><head><title>Baleia</title></head><body><div id="app"></div></body></html>`
)

func TestAnalyticsTag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{name: "nil config", cfg: nil},
		{name: "off without a website id", cfg: &config.Config{AnalyticsUmamiScriptURL: config.DefaultUmamiScriptURL}},
		{name: "off without a script url", cfg: &config.Config{AnalyticsUmamiWebsiteID: testWebsiteID}},
		{
			name: "on",
			cfg: &config.Config{
				AnalyticsUmamiWebsiteID: testWebsiteID,
				AnalyticsUmamiScriptURL: config.DefaultUmamiScriptURL,
			},
			want: `<script defer src="https://cloud.umami.is/script.js" data-website-id="` + testWebsiteID +
				`" data-exclude-search="true"></script>`,
		},
		{
			// Startup validation rejects these; the tag must hold even if it were bypassed.
			name: "values cannot leave their attribute",
			cfg: &config.Config{
				AnalyticsUmamiWebsiteID: `x" onload="alert(1)`,
				AnalyticsUmamiScriptURL: `https://a.example/s.js"><script>alert(1)</script>`,
			},
			want: `<script defer src="https://a.example/s.js&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"` +
				` data-website-id="x&#34; onload=&#34;alert(1)" data-exclude-search="true"></script>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := analyticsTag(tt.cfg); got != tt.want {
				t.Fatalf("analyticsTag() = %q, want %q", got, tt.want)
			}
		})
	}
}

func analyticsFixture(t *testing.T, shell, tag string) http.Handler {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"app.html":   shell,
		"robots.txt": "User-agent: *\nAllow: /\n",
		"asset.js":   "console.log(1)",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return staticHandler(&fakeStaticUsers{configured: true}, dir, withHeadTag(tag))
}

// TestShellHandler_Off: no tag serves the shell exactly as the build wrote it.
func TestShellHandler_Off(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"/", "/sql", "/connections/new"} {
		rec := serve(analyticsFixture(t, realisticHead, ""), http.MethodGet, route)
		if rec.Code != http.StatusOK || rec.Body.String() != realisticHead {
			t.Fatalf("GET %s = %d %q, want the untouched shell", route, rec.Code, rec.Body.String())
		}
	}
}

// TestShellHandler_Injects: every route that answers with the shell carries the
// tag exactly once, inside <head>, and nothing else is rewritten.
func TestShellHandler_Injects(t *testing.T) {
	t.Parallel()
	const tag = `<script defer src="https://cloud.umami.is/script.js" data-website-id="` + testWebsiteID + `"></script>`
	handler := analyticsFixture(t, realisticHead, tag)
	want := strings.Replace(realisticHead, "</head>", tag+"</head>", 1)

	for _, route := range []string{"/", "/sql", "/sql/q/abc", "/connections/new", "/connections/"} {
		rec := serve(handler, http.MethodGet, route)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", route, rec.Code)
		}
		if body := rec.Body.String(); body != want {
			t.Fatalf("GET %s body = %q, want %q", route, body, want)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("GET %s Content-Type = %q, want text/html", route, ct)
		}
		if lm := rec.Header().Get("Last-Modified"); lm != "" {
			t.Fatalf("GET %s sent Last-Modified %q: a 304 would keep a shell cached without the tag", route, lm)
		}
	}
	if n := strings.Count(want, "cloud.umami.is"); n != 1 {
		t.Fatalf("test fixture has %d tags, want 1", n)
	}
}

// TestShellHandler_LeavesOtherResponsesAlone: only the shell is rewritten.
func TestShellHandler_LeavesOtherResponsesAlone(t *testing.T) {
	t.Parallel()
	handler := analyticsFixture(t, realisticHead, `<script src="https://t.example/s.js"></script>`)
	for route, want := range map[string]string{
		"/robots.txt": "User-agent: *\nAllow: /\n",
		"/asset.js":   "console.log(1)",
	} {
		rec := serve(handler, http.MethodGet, route)
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Fatalf("GET %s = %d %q, want the file untouched", route, rec.Code, rec.Body.String())
		}
	}
	if rec := serve(handler, http.MethodGet, "/api/v1/unknown"); rec.Code != http.StatusNotFound || rec.Body.Len() != 0 {
		t.Fatalf("GET /api/v1/unknown = %d %q, want an empty 404", rec.Code, rec.Body.String())
	}
}

// TestShellHandler_NoHead: a shell without </head> is served as it is rather
// than guessed at.
func TestShellHandler_NoHead(t *testing.T) {
	t.Parallel()
	const shell = "<app-shell>"
	rec := serve(analyticsFixture(t, shell, `<script src="https://t.example/s.js"></script>`), http.MethodGet, "/")
	if rec.Code != http.StatusOK || rec.Body.String() != shell {
		t.Fatalf("GET / = %d %q, want the untouched shell", rec.Code, rec.Body.String())
	}
}

// TestShellHandler_HeadRequest: HEAD answers like GET without a body.
func TestShellHandler_HeadRequest(t *testing.T) {
	t.Parallel()
	rec := serve(analyticsFixture(t, realisticHead, `<script src="https://t.example/s.js"></script>`), http.MethodHead, "/")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD / = %d with %d body bytes, want 200 and none", rec.Code, rec.Body.Len())
	}
}
