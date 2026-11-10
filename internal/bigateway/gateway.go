// Package bigateway serves Trino's HTTP client protocol to external BI tools
// through Baleia (ADR 0017). It authenticates a workspace-bound BI key, applies
// the same workspace catalog scope as the editor, and relays one inline page
// at a time without storing results.
package bigateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/execscope"
	"github.com/galileostudio/baleia/internal/identity/bikeys"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/workload"
)

// Deps is everything the gateway needs, built at the wiring site.
type Deps struct {
	// Secret is the HMAC key shared by every gateway replica; at least 32
	// bytes. Rewritten URIs are stateless and validate on any replica.
	Secret     []byte
	Keys       Keys
	Engines    Engines
	Scope      execscope.Store
	Rules      Rules
	Workspaces Workspaces
	Executions Executions
	Settings   Settings
	Audit      audit.Recorder
	// MaxPerKey and MaxPerWorkspace bound running BI statements; 0 disables
	// the limit.
	MaxPerKey       int
	MaxPerWorkspace int
	// TrinoIdentity maps a Baleia user to the X-Trino-User of the session.
	TrinoIdentity TrinoIdentity
	// CatalogCredentials fills the per-user catalog token (ADR 0015); nil
	// leaves the extra credential out.
	CatalogCredentials queryengine.CatalogCredentials
	// Dialect splits statements for the read-only check; it must be Trino.
	Dialect dialect.Dialect
	// PublicURL is the externally reachable base URL used to rewrite Trino
	// URIs. Empty derives the scheme and host from each request.
	PublicURL string
	Logger    *slog.Logger
}

// Keys authenticates an HTTP Basic credential; *bikeys.Service implements it.
type Keys interface {
	Authenticate(ctx context.Context, email, secret string) (bikeys.Credential, error)
}

// Engines finds the key's engine; *queryengine.Manager implements it.
type Engines interface {
	Get(ctx context.Context, id db.UUID) (queryengine.Engine, error)
}

// Rules lists the workload rules; *repo.Repo implements it.
type Rules interface {
	ListWorkloadRules(ctx context.Context) ([]db.WorkloadRule, error)
}

// Workspaces reads the workspace whose owner team feeds the workload rules
// and whose BI switch gates the request.
type Workspaces interface {
	GetWorkspaceByID(ctx context.Context, id db.UUID) (db.Workspace, error)
}

// Executions records BI statements in the shared execution history and counts
// their running rows for the concurrency limits; *repo.Repo implements it.
type Executions interface {
	CreateQueryExecution(ctx context.Context, arg db.CreateQueryExecutionParams) (db.QueryExecution, error)
	UpdateQueryExecution(ctx context.Context, arg db.UpdateQueryExecutionParams) (db.QueryExecution, error)
	FailQueryExecution(ctx context.Context, arg db.FailQueryExecutionParams) (db.QueryExecution, error)
	CountRunningBIExecutionsByKey(ctx context.Context, keyID db.UUID) (int64, error)
	CountRunningBIExecutionsByWorkspace(ctx context.Context, workspaceID db.UUID) (int64, error)
}

// Settings reads the global BI gateway switch.
type Settings interface {
	GetBIGatewaySetting(ctx context.Context) (db.BIGatewaySetting, error)
}

// TrinoIdentity maps a user to their Trino identity.
type TrinoIdentity func(ctx context.Context, userID db.UUID) (string, error)

// Gateway is the BI listener. It is stateless: every request carries its key,
// and the Trino query id lives in the rewritten URI.
type Gateway struct {
	deps Deps
	now  func() time.Time
}

// URI lifetime: long enough for the largest execution, short enough that a
// leaked URI expires. #710 signs it with the same expiry.
const uriTTL = 24 * time.Hour

// maxStatementBytes bounds the SQL body a BI client may submit.
const maxStatementBytes = 1 << 20

// maxPageBytes bounds one response page held in memory. Trino 484 already
// caps a page at 1 MiB server-side (`TARGET_RESULT_SIZE`); this is a safety
// net, not a client-requested page size.
const maxPageBytes = 16 << 20

// New builds the gateway.
func New(d Deps) (*Gateway, error) {
	if d.Keys == nil || d.Engines == nil || d.Scope == nil || d.Dialect == nil || d.TrinoIdentity == nil ||
		d.Workspaces == nil || d.Executions == nil || d.Settings == nil {
		return nil, errors.New("bi gateway: missing dependencies")
	}
	if len(d.Secret) < 32 {
		return nil, errors.New("bi gateway: BALEIA_BI_GATEWAY_SECRET must be at least 32 bytes")
	}
	if d.PublicURL == "" && d.Logger != nil {
		d.Logger.Warn("bi gateway: BALEIA_BI_GATEWAY_URL is unset; rewritten URIs use the request scheme and host. Set an https URL when TLS terminates before the gateway")
	}
	return &Gateway{deps: d, now: time.Now}, nil
}

// ServeHTTP routes only the Trino protocol paths this gateway serves.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/info":
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		g.info(w, r)
	case r.URL.Path == "/v1/statement":
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		g.statement(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/statement/") && (r.Method == http.MethodGet || r.Method == http.MethodDelete):
		// A cleaned path that differs would let ../ reach another Trino
		// endpoint under the statement prefix.
		if path.Clean(r.URL.Path) != r.URL.Path {
			http.NotFound(w, r)
			return
		}
		g.follow(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/query/"):
		// infoUri points at the gateway, but the Trino web UI is never exposed.
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

// info proxies the coordinator's /v1/info after authenticating the key.
func (g *Gateway) info(w http.ResponseWriter, r *http.Request) {
	cred, ok := g.authorize(w, r)
	if !ok {
		return
	}
	base, client, ok := g.target(r.Context(), w, cred)
	if !ok {
		return
	}
	headers := http.Header{}
	headers.Set("Accept-Encoding", "identity")
	headers.Set("User-Agent", "baleia-bi-gateway")
	up, err := g.do(r.Context(), client, http.MethodGet, base, "/v1/info", nil, nil, headers)
	if err != nil {
		g.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "the coordinator is unreachable")
		return
	}
	g.write(w, up, up.body)
}

// statement authenticates, rejects writes and relays one Trino statement.
func (g *Gateway) statement(w http.ResponseWriter, r *http.Request) {
	cred, ok := g.authorize(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStatementBytes)
	sql, err := io.ReadAll(r.Body)
	if err != nil {
		g.writeError(w, http.StatusRequestEntityTooLarge, "INVALID_ARGUMENTS", "the statement is too large")
		return
	}
	if err := ReadOnly(g.deps.Dialect, string(sql)); err != nil {
		g.writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "the BI gateway only accepts read-only statements")
		return
	}
	if !g.withinLimits(w, r, cred) {
		return
	}
	execID, ok := g.createExecution(w, r, cred, string(sql))
	if !ok {
		return
	}
	g.relay(w, r, cred, "/v1/statement", sql, execID)
}

// follow relays GET/DELETE on a URI this gateway issued.
func (g *Gateway) follow(w http.ResponseWriter, r *http.Request) {
	cred, ok := g.authorize(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	execID, ok := g.validQueryURI(q, r.URL.Path, cred.Key.ID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Our parameters are not part of the Trino request.
	q.Del("baleia_exec")
	q.Del("baleia_qid")
	q.Del("baleia_key")
	q.Del("baleia_exp")
	q.Del("baleia_sig")
	var body []byte
	if r.Method == http.MethodDelete {
		// No statement body on cancel.
		body = nil
	}
	g.relayWithQuery(w, r, cred, r.URL.Path, q, body, execID)
}

// relay builds the Trino request for path and forwards it.
func (g *Gateway) relay(w http.ResponseWriter, r *http.Request, cred bikeys.Credential, path string, body []byte, execID db.UUID) {
	g.relayWithQuery(w, r, cred, path, r.URL.Query(), body, execID)
}

// relayWithQuery forwards one statement or follow request after resolving the
// workspace scope, the owner's Trino identity and the per-user catalog token.
func (g *Gateway) relayWithQuery(w http.ResponseWriter, r *http.Request, cred bikeys.Credential, path string, query url.Values, body []byte, execID db.UUID) {
	base, client, ok := g.target(r.Context(), w, cred)
	if !ok {
		return
	}
	scope, err := execscope.For(g.deps.Scope, cred.Key.WorkspaceID, cred.Key.EngineID)(r.Context())
	if err != nil {
		g.writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "the workspace scope is unavailable")
		return
	}
	if len(scope) == 0 {
		g.writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "the workspace has no catalog on this engine")
		return
	}
	trinoUser, err := g.deps.TrinoIdentity(r.Context(), cred.User.ID)
	if err != nil {
		g.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "the key owner no longer exists")
		return
	}
	headers, ok := g.requestHeaders(w, r, cred, scope, trinoUser, body)
	if !ok {
		return
	}
	up, err := g.do(r.Context(), client, r.Method, base, path, query, body, headers)
	if err != nil {
		g.failExecution(r.Context(), execID, "engine_unavailable")
		g.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "the coordinator is unreachable")
		return
	}
	g.recordPage(r.Context(), execID, up.body, r.Method, up.status)
	page := g.rewritePage(r, cred, execID, up.body)
	g.write(w, up, page)
}

// target resolves the key's coordinated Trino engine.
func (g *Gateway) target(ctx context.Context, w http.ResponseWriter, cred bikeys.Credential) (string, *http.Client, bool) {
	eng, err := g.deps.Engines.Get(ctx, cred.Key.EngineID)
	if err != nil {
		g.writeError(w, http.StatusNotFound, "CATALOG_NOT_FOUND", "the key's engine is unavailable")
		return "", nil, false
	}
	if eng.Kind() != "trino" {
		g.writeError(w, http.StatusUnprocessableEntity, "NOT_SUPPORTED", "the key's engine is not a Trino engine")
		return "", nil, false
	}
	proxy, ok := eng.(queryengine.TrinoProxy)
	if !ok {
		g.writeError(w, http.StatusUnprocessableEntity, "NOT_SUPPORTED", "the key's engine is not a Trino engine")
		return "", nil, false
	}
	base, client := proxy.ProxyClient()
	if base == "" || client == nil {
		g.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "the engine has no reachable coordinator")
		return "", nil, false
	}
	return strings.TrimRight(base, "/"), client, true
}

// requestHeaders applies the allow-list and Baleia's identity, scope and tags.
func (g *Gateway) requestHeaders(w http.ResponseWriter, r *http.Request, cred bikeys.Credential, scope []string, trinoUser string, body []byte) (http.Header, bool) {
	h := http.Header{}
	// The allow-list: everything else the client sent is dropped, in
	// particular X-Trino-User, X-Trino-Role, X-Trino-Extra-Credential,
	// X-Trino-Transaction-Id and X-Trino-Query-Data-Encoding.
	for _, name := range []string{
		"X-Trino-Catalog",
		"X-Trino-Schema",
		"X-Trino-Time-Zone",
		"X-Trino-Language",
		"X-Trino-Source",
		"X-Trino-Client-Tags",
		"X-Trino-Client-Info",
		"X-Trino-Client-Capabilities",
		"X-Trino-Session",
		"X-Trino-Prepared-Statement",
		"X-Trino-Trace-Token",
	} {
		if v := r.Header.Get(name); v != "" {
			h.Set(name, v)
		}
	}
	if h.Get("X-Trino-Source") == "" {
		h.Set("X-Trino-Source", "baleia-bi")
	}
	class := g.classify(r.Context(), cred)
	tags := dropReservedTags(splitTags(h.Get("X-Trino-Client-Tags")))
	tags = append(tags, "baleia", "bi")
	if class != "" {
		tags = append(tags, "baleia.class="+string(class))
	}
	h.Set("X-Trino-Client-Tags", strings.Join(dedupe(tags), ","))
	h.Set("X-Trino-User", trinoUser)
	h.Set("Accept-Encoding", "identity")
	h.Set("User-Agent", "baleia-bi-gateway")
	if body != nil {
		h.Set("Content-Type", "text/plain")
	}

	extra := map[string]string{
		"baleia.catalogs": strings.Join(scope, ","),
		"baleia.mode":     "readonly",
	}
	catalog := r.Header.Get("X-Trino-Catalog")
	if g.deps.CatalogCredentials != nil {
		token, err := g.deps.CatalogCredentials.CatalogToken(r.Context(), cred.Key.EngineID.String(), queryengine.SessionSpec{
			Catalog:  catalog,
			Identity: queryengine.Identity{UserID: trinoUser, Principal: cred.User.ID.String()},
		})
		if err != nil {
			g.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "the catalog identity could not be resolved")
			return nil, false
		}
		if token != "" {
			extra["token"] = token
		}
	}
	h.Set("X-Trino-Extra-Credential", encodeExtraCredentials(extra))
	return h, true
}

// classify returns the workload class for the BI statement; the rules are a
// hint to Trino's resource groups, never an admission queue here.
func (g *Gateway) classify(ctx context.Context, cred bikeys.Credential) queryengine.WorkloadClass {
	rules := []workload.Rule{}
	if g.deps.Rules != nil {
		rows, err := g.deps.Rules.ListWorkloadRules(ctx)
		if err != nil {
			if g.deps.Logger != nil {
				g.deps.Logger.WarnContext(ctx, "bi gateway: could not load workload rules", "err", err)
			}
		} else {
			for _, row := range rows {
				match, err := workload.ParseRuleMatch(row.Match)
				if err != nil {
					continue
				}
				rule := workload.Rule{
					ID: row.ID.String(), Position: int(row.Position), Enabled: row.Enabled,
					Match: match, Class: queryengine.WorkloadClass(row.Class), Weight: int(row.Weight),
				}
				if row.MaxClass != nil {
					rule.MaxClass = queryengine.WorkloadClass(*row.MaxClass)
				}
				rules = append(rules, rule)
			}
		}
	}
	team := ""
	if g.deps.Workspaces != nil {
		if ws, err := g.deps.Workspaces.GetWorkspaceByID(ctx, cred.Key.WorkspaceID); err == nil {
			team = ws.OwnerTeamID.String()
		}
	}
	decision := workload.Decide(workload.OriginBI, workload.RuleInput{
		WorkspaceID: cred.Key.WorkspaceID.String(),
		TeamID:      team,
		Role:        cred.User.Role,
		UserID:      cred.User.ID.String(),
		EngineID:    cred.Key.EngineID.String(),
		Statements:  1,
	}, rules)
	return decision.Class
}

// authenticate reads HTTP Basic and validates the key. Every failure is a
// uniform 401 so the key, its owner and the workspace are not revealed.
func (g *Gateway) authenticate(w http.ResponseWriter, r *http.Request) (bikeys.Credential, bool) {
	email, secret, ok := r.BasicAuth()
	if !ok {
		g.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "a BI key is required")
		return bikeys.Credential{}, false
	}
	cred, err := g.deps.Keys.Authenticate(r.Context(), email, secret)
	if err != nil {
		g.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid BI key")
		return bikeys.Credential{}, false
	}
	return cred, true
}

// upstream is one page of a coordinator response.
type upstream struct {
	status int
	header http.Header
	body   []byte
}

// do sends one request and reads exactly one response page.
func (g *Gateway) do(ctx context.Context, client *http.Client, method, base, path string, query url.Values, body []byte, headers http.Header) (*upstream, error) {
	u, err := url.Parse(base + path)
	if err != nil {
		return nil, err
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	var reader io.Reader = http.NoBody
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader) //nolint:gosec // base is the configured coordinator; ServeHTTP restricts the path
	if err != nil {
		return nil, err
	}
	req.Header = headers
	resp, err := client.Do(req) //nolint:gosec // the request targets the configured coordinator only
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	page, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(page) > maxPageBytes {
		return nil, fmt.Errorf("bi gateway: page exceeds %d bytes", maxPageBytes)
	}
	return &upstream{status: resp.StatusCode, header: resp.Header.Clone(), body: page}, nil
}

// write relays the upstream status and headers, replacing the body.
func (g *Gateway) write(w http.ResponseWriter, up *upstream, body []byte) {
	for name, values := range up.header {
		switch http.CanonicalHeaderKey(name) {
		case "Content-Length", "Content-Encoding", "Transfer-Encoding", "Connection",
			"X-Trino-Set-Authorization-User", "X-Trino-Reset-Authorization-User":
			continue
		}
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(up.status)
	_, _ = w.Write(body) //nolint:gosec // a JSON protocol page from the trusted coordinator
}

// writeError writes a Trino-shaped error so a JDBC client can surface it.
func (g *Gateway) writeError(w http.ResponseWriter, status int, name, message string) {
	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="baleia"`)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"message": message, "errorCode": status, "errorName": name, "errorType": "USER_ERROR",
	}})
}

// splitTags splits a comma-separated client tag header.
func splitTags(raw string) []string {
	var out []string
	for _, tag := range strings.Split(raw, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

// dedupe keeps the first occurrence of each tag, preserving order.
func dedupe(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// encodeExtraCredentials renders the extra credentials the way Trino decodes
// them: sorted keys, values percent-encoded, comma-separated.
func encodeExtraCredentials(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+url.QueryEscape(values[key]))
	}
	return strings.Join(parts, ",")
}

// validQueryURI checks the unsigned key binding and expiry #710 will sign.
// dropReservedTags removes the tags only the gateway may set, so a client
// cannot override the workload class or impersonate Baleia's own tags.
func dropReservedTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag == "baleia" || tag == "bi" || strings.HasPrefix(tag, "baleia.") {
			continue
		}
		out = append(out, tag)
	}
	return out
}
