package bigateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/maltzsama/baleia/internal/audit"
	"github.com/maltzsama/baleia/internal/identity/bikeys"
	"github.com/maltzsama/baleia/internal/semanticcatalog"
)

// Semantic catalog routes (ADR 0030). They live under /baleia/ so they never
// collide with Trino's /v1/ protocol paths, and they authenticate exactly like
// a statement: the BI key, then the global and workspace BI switches.
const (
	semanticPath        = "/baleia/v1/semantic"
	semanticModelsPath  = semanticPath + "/models"
	semanticMetricsPath = semanticPath + "/metrics"
)

// Page sizes of the semantic catalog listings.
const (
	defaultSemanticPageSize = 100
	maxSemanticPageSize     = 500
)

// semanticRoute reports whether path is a semantic catalog route.
func semanticRoute(path string) bool {
	return path == semanticPath || path == semanticModelsPath || path == semanticMetricsPath
}

// semanticSummary is the response of GET /baleia/v1/semantic.
type semanticSummary struct {
	ContractVersion int                       `json:"contract_version"`
	Workspace       semanticcatalog.Workspace `json:"workspace"`
	Models          int                       `json:"models"`
	Metrics         int                       `json:"metrics"`
}

// semanticPage is one page of models or metrics. NextAfter is the cursor of
// the next page; it is absent on the last page.
type semanticPage[T any] struct {
	ContractVersion int    `json:"contract_version"`
	Items           []T    `json:"items"`
	NextAfter       string `json:"next_after,omitempty"`
}

// semantic serves the read-only semantic catalog of the key's workspace.
func (g *Gateway) semantic(w http.ResponseWriter, r *http.Request) {
	if g.deps.Semantic == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		g.writeError(w, http.StatusMethodNotAllowed, "NOT_SUPPORTED", "the semantic catalog is read-only")
		return
	}
	after, limit, ok := semanticPaging(w, r, g)
	if !ok {
		return
	}
	cred, ok := g.authorize(w, r)
	if !ok {
		return
	}
	snap, err := g.deps.Semantic.Read(r.Context(), semanticcatalog.Request{
		WorkspaceID: cred.Key.WorkspaceID, UserID: cred.User.ID, EngineID: cred.Key.EngineID,
	})
	if errors.Is(err, semanticcatalog.ErrTooLarge) {
		g.writeError(w, http.StatusUnprocessableEntity, "EXCEEDED_LIMIT", "the semantic catalog exceeds the snapshot limit")
		return
	}
	if err != nil {
		if g.deps.Logger != nil {
			g.deps.Logger.ErrorContext(r.Context(), "bi gateway: semantic catalog read failed",
				"workspace_id", cred.Key.WorkspaceID.String(), "key_id", cred.Key.ID.String(), "err", err)
		}
		g.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "the semantic catalog is unavailable")
		return
	}
	g.auditSemanticRead(r, cred, r.URL.Path)

	var body any
	switch r.URL.Path {
	case semanticModelsPath:
		items, next := pageBySlug(snap.Models, func(m semanticcatalog.Model) string { return m.Slug }, after, limit)
		body = semanticPage[semanticcatalog.Model]{ContractVersion: semanticcatalog.ContractVersion, Items: items, NextAfter: next}
	case semanticMetricsPath:
		items, next := pageBySlug(snap.Metrics, func(m semanticcatalog.Metric) string { return m.Slug }, after, limit)
		body = semanticPage[semanticcatalog.Metric]{ContractVersion: semanticcatalog.ContractVersion, Items: items, NextAfter: next}
	default:
		body = semanticSummary{
			ContractVersion: semanticcatalog.ContractVersion, Workspace: snap.Workspace,
			Models: len(snap.Models), Metrics: len(snap.Metrics),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(body)
}

// semanticPaging reads the after and limit query parameters.
func semanticPaging(w http.ResponseWriter, r *http.Request, g *Gateway) (string, int, bool) {
	q := r.URL.Query()
	limit := defaultSemanticPageSize
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxSemanticPageSize {
			g.writeError(w, http.StatusBadRequest, "INVALID_ARGUMENTS", "limit must be between 1 and 500")
			return "", 0, false
		}
		limit = n
	}
	return q.Get("after"), limit, true
}

// pageBySlug returns up to limit items whose slug sorts after the cursor, and
// the cursor of the next page. items must be sorted by slug.
func pageBySlug[T any](items []T, slug func(T) string, after string, limit int) ([]T, string) {
	start := 0
	if after != "" {
		start = sort.Search(len(items), func(i int) bool { return slug(items[i]) > after })
	}
	end := min(start+limit, len(items))
	page := make([]T, 0, end-start)
	page = append(page, items[start:end]...)
	if end < len(items) {
		return page, slug(items[end-1])
	}
	return page, ""
}

// auditSemanticRead records the read. The key is never part of the event.
func (g *Gateway) auditSemanticRead(r *http.Request, cred bikeys.Credential, path string) {
	if g.deps.Audit == nil {
		return
	}
	ev := audit.Event{
		Action:       audit.BISemanticCatalogRead,
		ResourceType: "workspace",
		ResourceID:   cred.Key.WorkspaceID.String(),
		WorkspaceID:  cred.Key.WorkspaceID.String(),
		Changes: map[string]audit.Change{
			"path":   {To: path},
			"key_id": {To: cred.Key.ID.String()},
		},
	}
	_ = g.deps.Audit.Record(r.Context(), ev.Allowed())
}
