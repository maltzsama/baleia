package server

import (
	"bytes"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/galileostudio/baleia/internal/config"
)

// analyticsTag is the Umami tracker the operator opted into, or "" when it is
// off. The values are validated at startup (config.Validate) and escaped here
// as well, so a value can never leave the attribute it is written into.
//
// data-exclude-search keeps the query string out of the analytics: the app puts
// the workspace, catalog, table and Explore query there.
func analyticsTag(cfg *config.Config) string {
	if cfg == nil || cfg.AnalyticsUmamiWebsiteID == "" || cfg.AnalyticsUmamiScriptURL == "" {
		return ""
	}
	return `<script defer src="` + html.EscapeString(cfg.AnalyticsUmamiScriptURL) +
		`" data-website-id="` + html.EscapeString(cfg.AnalyticsUmamiWebsiteID) +
		`" data-exclude-search="true"></script>`
}

// staticOption tunes staticHandler.
type staticOption func(*staticConfig)

type staticConfig struct {
	headTag string
}

// withHeadTag inserts tag before </head> of the app shell. An empty tag keeps
// the shell exactly as the build produced it.
func withHeadTag(tag string) staticOption {
	return func(c *staticConfig) { c.headTag = tag }
}

// shellHandler serves the app shell (app.html).
//
// Without a tag it is http.ServeFile, byte for byte. With one it inserts the tag
// before </head> and serves the result without Last-Modified: the file has not
// changed when an operator turns analytics on, so a validator taken from it would
// let a browser keep its cached shell, and the tag, off, with a 304.
func shellHandler(dir, headTag string) http.HandlerFunc {
	name := filepath.Join(dir, "app.html")
	if headTag == "" {
		return func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, name)
		}
	}
	closeHead := []byte("</head>")
	return func(w http.ResponseWriter, r *http.Request) {
		shell, err := os.ReadFile(name) // #nosec G304 -- name is the build's app.html, never request input
		if err != nil || !bytes.Contains(shell, closeHead) {
			http.ServeFile(w, r, name)
			return
		}
		shell = bytes.Replace(shell, closeHead, append([]byte(headTag), closeHead...), 1)
		http.ServeContent(w, r, "app.html", time.Time{}, bytes.NewReader(shell))
	}
}
