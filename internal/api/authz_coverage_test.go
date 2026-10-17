package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file is the guard rail of the authorization rework (#16): every
// operation of the generated StrictServerInterface and every hand-registered
// chi route must be listed below with how it is protected. A new handler that
// is not listed fails the build, and a listed handler whose body no longer
// contains the check it declares fails too, so the table cannot drift into
// documentation.
//
// The check is structural (the handler's body must call the named guard), not a
// behavioral proof: the behavior of each guard is pinned by the handler tests
// and by internal/authz.

type accessKind string

const (
	// kindPublic is reachable without a token. For strict operations it must match
	// publicOps in internal/server/server.go.
	kindPublic accessKind = "public"
	// kindAuthenticated needs a valid token and nothing else (the data is not
	// sensitive or the action only affects the caller's own session). The reason
	// is required.
	kindAuthenticated accessKind = "authenticated"
	// kindSelf scopes everything to the caller.
	kindSelf accessKind = "self"
	// kindAdmin is for global administrators only.
	kindAdmin accessKind = "admin"
	// kindChecked runs the listed guards (authz checks, workspace membership).
	kindChecked accessKind = "checked"
	// kindStub is a placeholder that answers a constant; it must not touch the
	// receiver (no database, no Trino).
	kindStub accessKind = "stub"
)

type access struct {
	kind  accessKind
	calls []string // calls the handler body must contain
	why   string   // required for kindAuthenticated
	pkg   string   // routes only: directory of the handler, default "."
}

var (
	selfID        = []string{"UserIDFromContext"}
	selfPrincipal = []string{"PrincipalFromContext"}
	adminRole     = []string{"RoleFromContext"}
	wsMember      = []string{"requireWorkspaceAccess"}
)

func checked(calls ...string) access { return access{kind: kindChecked, calls: calls} }

var operationAccess = map[string]access{
	// ── public (see publicOps in internal/server/server.go)
	"GetSetupStatus":       {kind: kindPublic},
	"CreateFirstAdmin":     {kind: kindPublic},
	"Login":                {kind: kindPublic},
	"ListOIDCProviders":    {kind: kindPublic},
	"OidcRedirect":         {kind: kindPublic},
	"OidcCallback":         {kind: kindPublic},
	"RequestPasswordReset": {kind: kindPublic},
	"ConfirmPasswordReset": {kind: kindPublic},
	"RefreshToken":         {kind: kindPublic},

	// ── any valid token
	"Logout":                  {kind: kindAuthenticated, why: "ends the caller's own session"},
	"ListConnectionProviders": {kind: kindAuthenticated, why: "static provider metadata (kinds and config schemas), no tenant data"},
	"GetDialect":              {kind: kindAuthenticated, why: "static dialect metadata (keyword and function lists), no tenant data"},
	"SplitStatements":         {kind: kindAuthenticated, why: "splits the caller's own text with static rules; reads no data"},

	// ── scoped to the caller
	"GetMe":                   {kind: kindSelf, calls: selfID},
	"GetProfile":              {kind: kindSelf, calls: selfID},
	"UpdateProfile":           {kind: kindSelf, calls: selfID},
	"UpdatePassword":          {kind: kindSelf, calls: selfID},
	"ListApiKeys":             {kind: kindSelf, calls: selfID},
	"CreateApiKey":            {kind: kindSelf, calls: selfID},
	"DeleteApiKey":            {kind: kindSelf, calls: selfID},
	"ListWorkspaces":          {kind: kindSelf, calls: selfID},
	"ListCatalogConnections":  {kind: kindSelf, calls: selfID},        // only the connections the caller created
	"CreateCatalogConnection": {kind: kindSelf, calls: selfID},        // any user; becomes the owner
	"ListTeams":               {kind: kindSelf, calls: selfPrincipal}, // the caller's teams; admin sees all
	"ListEngines":             {kind: kindSelf, calls: selfPrincipal}, // engines on the caller's connections or hosting a granted catalog; admin sees all

	// ── global administrators
	"ListUsers":         {kind: kindAdmin, calls: adminRole},
	"InviteUser":        {kind: kindAdmin, calls: adminRole},
	"RemoveUser":        {kind: kindAdmin, calls: adminRole},
	"UpdateUserRole":    {kind: kindAdmin, calls: adminRole},
	"CreateTeam":        {kind: kindAdmin, calls: adminRole},
	"DeleteTeam":        {kind: kindAdmin, calls: adminRole},
	"AddTeamMember":     {kind: kindAdmin, calls: adminRole},
	"RemoveTeamMember":  {kind: kindAdmin, calls: adminRole},
	"ReconcileTrino":    {kind: kindAdmin, calls: []string{"RequireAdmin"}},
	"ReconcileEngine":   {kind: kindAdmin, calls: []string{"RequireAdmin"}},
	"ListReconcileRuns": {kind: kindAdmin, calls: []string{"RequireAdmin"}},

	// ── connections and catalogs (internal/authz)
	"GetCatalogConnection":    checked("Connection"),
	"UpdateCatalogConnection": checked("Connection"),
	"DeleteCatalogConnection": checked("Connection"),
	"TestCatalogConnection":   checked("Connection"),
	// Any signed-in user tests settings they hold; bind: intents go through the engine check.
	"TestConnectionDraft": checked("PrincipalFromContext", "planTest"),
	"CreateEngine":        checked("Connection"),
	"GetEngine":           checked("Engine"),
	"UpdateEngine":        checked("Engine"),
	"DeleteEngine":        checked("Engine"),
	"ListEngineBindings":  checked("Engine"),
	"ListCatalogBindings": checked("Catalog"),
	// bindingAccess and managedBinding run Catalog and Engine with ActWrite
	// (handlers_bindings.go; TestBindingAPI_Authz).
	"CreateCatalogBinding":        checked("bindingAccess"),
	"UpdateCatalogBinding":        checked("managedBinding"),
	"DeleteCatalogBinding":        checked("managedBinding"),
	"SyncCatalogBinding":          checked("managedBinding"),
	"GetEngineBindingSupport":     checked("Engine"),
	"ListCatalogs":                checked("Connection"),
	"CreateCatalog":               checked("Connection"),
	"GetCatalog":                  checked("CatalogOfConnection"),
	"UpdateCatalog":               checked("CatalogOfConnection"),
	"DeleteCatalog":               checked("CatalogOfConnection"),
	"SyncCatalog":                 checked("Catalog"),
	"SyncCatalogMetadata":         checked("Catalog"),
	"ListCatalogMetadataSyncRuns": checked("Catalog"),

	// ── teams and workspaces
	"GetTeam":                    checked("Team"),
	"ListTeamMembers":            checked("Team"),
	"CreateWorkspace":            {kind: kindChecked, calls: []string{"RoleFromContext", "UserIDFromContext"}}, // shared needs an admin
	"GetWorkspace":               {kind: kindChecked, calls: wsMember},
	"ListWorkspaceTargets":       {kind: kindChecked, calls: wsMember},
	"UpdateWorkspace":            checked("Workspace"),
	"ListWorkspaceCatalogAccess": checked("Workspace"),
	"GrantCatalogAccess":         checked("Workspace", "Catalog"),
	"RevokeCatalogAccess":        checked("Workspace", "Catalog"),

	// ── workspace members (query editor, retrievers)
	"ListExecutions":          {kind: kindChecked, calls: wsMember},
	"RunQuery":                checked("requireWorkspaceAccess", "requireSpace"),
	"GetExecution":            {kind: kindChecked, calls: wsMember},
	"CancelExecution":         {kind: kindChecked, calls: wsMember},
	"GetResults":              {kind: kindChecked, calls: wsMember},
	"ListStatements":          {kind: kindChecked, calls: wsMember},
	"GetStatementResults":     {kind: kindChecked, calls: wsMember},
	"ListFolders":             {kind: kindChecked, calls: wsMember},
	"CreateFolder":            checked("requireWorkspaceAccess", "spaceForWrite"),
	"DeleteFolder":            checked("requireWorkspaceAccess", "requireSpace"),
	"ListSavedQueries":        {kind: kindChecked, calls: wsMember},
	"CreateSavedQuery":        checked("requireWorkspaceAccess", "spaceForWrite", "savedQueryTarget"),
	"DeleteSavedQuery":        checked("requireWorkspaceAccess", "requireSpace"),
	"GetSavedQuery":           checked("requireWorkspaceAccess", "requireSpace"),
	"UpdateSavedQuery":        checked("requireWorkspaceAccess", "requireSpace", "savedQueryTarget"),
	"ListRetrievers":          {kind: kindChecked, calls: wsMember},
	"CreateRetriever":         {kind: kindChecked, calls: wsMember},
	"DeleteRetriever":         {kind: kindChecked, calls: wsMember},
	"GetRetriever":            {kind: kindChecked, calls: wsMember},
	"UpdateRetriever":         {kind: kindChecked, calls: wsMember},
	"GetRetrieverHistory":     {kind: kindChecked, calls: wsMember},
	"TriggerRetrieverRefresh": {kind: kindChecked, calls: wsMember},

	// ── placeholders that answer a constant
	// Metadata snapshot reads (#18): workspace members (metadataWorkspace
	// wraps requireWorkspaceAccess), granted catalogs only (in each query).
	"ListMetadataCatalogs":   checked("metadataWorkspace"),
	"ListMetadataNamespaces": checked("metadataWorkspace", "grantedCatalog"),
	"ListMetadataTables":     checked("metadataWorkspace", "grantedCatalog"),
	"GetMetadataTable":       checked("metadataWorkspace", "GetGrantedMetadataTable"),
	"SearchMetadata":         checked("metadataWorkspace", "SearchMetadata"),
	// Semantic layer v1 (#33): workspace members; docs only on catalogs the
	// workspace may use (semanticCatalog wraps grantedCatalog). Docs are not
	// content items, so #40 does not add a space check here.
	"ListSemanticDocs":   checked("metadataWorkspace", "semanticCatalog"),
	"UpsertSemanticDoc":  checked("metadataWorkspace", "semanticCatalog"),
	"DeleteSemanticDoc":  checked("metadataWorkspace", "semanticCatalog"),
	"SearchSemanticDocs": checked("metadataWorkspace", "SearchSemanticDocs"),
	// Metrics are validated against a real engine in a read-only session
	// (validateSemanticMetric); the catalog must be granted and the caller
	// needs the space role (contentSpace wraps requireSpace).
	"ListSemanticMetrics":  checked("metadataWorkspace"),
	"GetSemanticMetric":    checked("metadataWorkspace", "GetSemanticMetric", "contentSpace"),
	"CreateSemanticMetric": checked("metadataWorkspace", "semanticCatalog", "validateSemanticMetric", "contentSpace"),
	"UpdateSemanticMetric": checked("metadataWorkspace", "GetSemanticMetric", "semanticCatalog", "validateSemanticMetric", "contentSpace"),
	"DeleteSemanticMetric": checked("metadataWorkspace", "GetSemanticMetric", "contentSpace", "DeleteSemanticMetric"),
	"GetSemanticMetricSql": checked("metadataWorkspace", "GetSemanticMetric", "semanticCatalog", "semanticTarget", "contentSpace"),
	// Spaces (#40): the effective role comes from the SQL function through
	// internal/authz. Invisible is 404, below the required role is 403.
	"ListSpaces":        checked("PrincipalFromContext", "Workspace", "ListSpacesForPrincipal"),
	"CreateSpace":       checked("PrincipalFromContext", "Workspace", "CreateSpace", "SetSpaceMember"),
	"GetSpace":          checked("requireSpace", "GetSpace"),
	"UpdateSpace":       checked("requireSpace", "GetSpace", "UpdateSpace"),
	"DeleteSpace":       checked("requireSpace", "GetSpace", "CountSpaceContent", "DeleteSpace"),
	"ListSpaceMembers":  checked("requireSpace", "ListSpaceMembers"),
	"SetSpaceMember":    checked("PrincipalFromContext", "requireSpace", "GetUserByID", "GetTeamByID", "SetSpaceMember"),
	"DeleteSpaceMember": checked("requireSpace", "DeleteSpaceMember"),
	// Content versions and trash (#34): viewer reads the history, editor
	// restores; a deleted entity is authorized against its last version's space.
	"ListSavedQueryVersions":       checked("savedQuerySpaceForHistory", "contentSpace", "ListContentVersions"),
	"GetSavedQueryVersion":         checked("GetContentVersion", "contentSpace"),
	"RestoreSavedQueryVersion":     checked("GetContentVersion", "contentSpace", "RestoreSavedQuery"),
	"ListSemanticMetricVersions":   checked("metricSpaceForHistory", "contentSpace", "ListContentVersions"),
	"GetSemanticMetricVersion":     checked("GetContentVersion", "contentSpace"),
	"RestoreSemanticMetricVersion": checked("GetContentVersion", "contentSpace", "ParseMetricSnapshot", "validateSemanticMetric", "RestoreSemanticMetric"),
	"ListTrash":                    checked("requireWorkspaceAccess", "ListDeletedContent"),
	"ExportSpaceBundle":            checked("contentSpace", "Export"),
	"ImportSpaceBundle":            checked("contentSpace", "Import"),
	// Audit trail (#45): admins see all, anyone else only their own events.
	"ListAuditEvents":    checked("PrincipalFromContext", "IsAdmin"),
	"StreamExecution":    {kind: kindStub}, // the real one is the ws route below
	"ListMigrations":     {kind: kindStub},
	"CreateMigration":    {kind: kindStub},
	"GetMigration":       {kind: kindStub},
	"CancelMigration":    {kind: kindStub},
	"ListMigrationItems": {kind: kindStub},
	"StartMigration":     {kind: kindStub},
}

// routeAccess covers the routes registered by hand with chi (everything that is
// not generated from the OpenAPI document). Keys are "METHOD path" with the
// path as written at the registration site.
var routeAccess = map[string]access{
	"GET /healthz":      {kind: kindPublic},
	"GET /readyz":       {kind: kindPublic},
	"GET /openapi.yaml": {kind: kindPublic},
	// Prometheus cannot log in: /metrics carries no tenant data and is gated by
	// the optional BALEIA_METRICS_TOKEN instead of the JWT (#87).
	"GET /metrics": {kind: kindPublic},
	"ANY /*":       {kind: kindPublic}, // the web app

	"GET /api/v1/auth/oidc/{provider_id}/login":    {kind: kindPublic},
	"GET /api/v1/auth/oidc/{provider_id}/callback": {kind: kindPublic},

	// The WebSocket authenticates with the token in the query string.
	"GET /api/v1/ws/workspaces/{id}/executions/{execution_id}": {kind: kindChecked, pkg: "../ws", calls: []string{"ParseToken", "CheckWorkspaceAccess"}},

	"GET /workspaces/{id}/catalogs":                                                                          checked("Workspace"),
	"GET /connections/{connection_id}/catalogs/{catalog_name}/namespaces":                                    checked("explorerTarget"),
	"GET /connections/{connection_id}/catalogs/{catalog_name}/namespaces/{namespace}/tables":                 checked("explorerTarget"),
	"GET /connections/{connection_id}/catalogs/{catalog_name}/namespaces/{namespace}/tables/{table}/columns": checked("explorerTarget"),

	"GET /lens/queries":           checked("visibleUser"),
	"GET /lens/queries/{id}":      checked("visibleUser"),
	"GET /lens/analytics/queries": checked("visibleUser"),
	"POST /lens/tables/{conn_id}/{catalog}/{namespace}/{table}/maintain": checked("Connection"),

	// No SQL text or credentials: cluster state and table health only (matrix row
	// "Lens: cluster state/history, table health").
	"GET /lens/cluster":                 {kind: kindAuthenticated, why: "cluster state, no SQL text"},
	"GET /lens/cluster/history":         {kind: kindAuthenticated, why: "cluster history, no SQL text"},
	"GET /lens/tables/health":           {kind: kindAuthenticated, why: "table health, no SQL text"},
	"GET /lens/tables/health/{conn_id}": {kind: kindAuthenticated, why: "table health, no SQL text"},
}

// ── source model ─────────────────────────────────────────────────────────────

// funcBody is what the audit needs to know about one function.
type funcBody struct {
	calls        map[string]bool // names of every function or method called
	usesReceiver bool
	file         string
}

// loadFuncs parses the non-test, non-generated Go files of dir and indexes the
// functions and methods by name. A name declared twice is recorded as ambiguous.
func loadFuncs(t *testing.T, dir string) (map[string]*funcBody, map[string]bool) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*funcBody{}
	dup := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, ".gen.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			recv := ""
			if fd.Recv != nil && len(fd.Recv.List) == 1 && len(fd.Recv.List[0].Names) == 1 {
				recv = fd.Recv.List[0].Names[0].Name
			}
			fb := bodyOf(fd.Body, recv)
			fb.file = filepath.Base(f)
			if _, seen := out[fd.Name.Name]; seen {
				dup[fd.Name.Name] = true
			}
			out[fd.Name.Name] = fb
		}
	}
	return out, dup
}

func bodyOf(n ast.Node, recv string) *funcBody {
	fb := &funcBody{calls: map[string]bool{}}
	ast.Inspect(n, func(x ast.Node) bool {
		switch v := x.(type) {
		case *ast.CallExpr:
			switch fn := v.Fun.(type) {
			case *ast.Ident:
				fb.calls[fn.Name] = true
			case *ast.SelectorExpr:
				fb.calls[fn.Sel.Name] = true
			}
		case *ast.Ident:
			if recv != "" && v.Name == recv {
				fb.usesReceiver = true
			}
		}
		return true
	})
	return fb
}

type route struct {
	key     string // "METHOD path"
	handler string // function name, "" for a literal
	lit     *funcBody
}

// loadRoutes finds the routes registered with chi in the given files:
// calls like r.Get("/x", handler) with a string-literal path.
func loadRoutes(t *testing.T, files ...string) []route {
	t.Helper()
	methods := map[string]string{"Get": "GET", "Post": "POST", "Put": "PUT", "Patch": "PATCH", "Delete": "DELETE", "Handle": "ANY", "HandleFunc": "ANY"}
	var out []route
	fset := token.NewFileSet()
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			method, ok := methods[sel.Sel.Name]
			if !ok {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			path, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.HasPrefix(path, "/") {
				return true
			}
			r := route{key: method + " " + path}
			switch h := call.Args[1].(type) {
			case *ast.SelectorExpr:
				r.handler = h.Sel.Name
			case *ast.Ident:
				r.handler = h.Name
			case *ast.FuncLit:
				r.lit = bodyOf(h, "")
			case *ast.CallExpr: // http.HandlerFunc(func...)
				if len(h.Args) == 1 {
					if fl, ok := h.Args[0].(*ast.FuncLit); ok {
						r.lit = bodyOf(fl, "")
					}
				}
			}
			out = append(out, r)
			return true
		})
	}
	return out
}

// publicOps reads the literal publicOps map of authMiddlewareFunc.
func publicOps(t *testing.T, serverFile string) map[string]bool {
	t.Helper()
	af, err := parser.ParseFile(token.NewFileSet(), serverFile, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	ast.Inspect(af, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 {
			return true
		}
		if id, ok := as.Lhs[0].(*ast.Ident); !ok || id.Name != "publicOps" {
			return true
		}
		cl, ok := as.Rhs[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, e := range cl.Elts {
			kv := e.(*ast.KeyValueExpr)
			k, _ := strconv.Unquote(kv.Key.(*ast.BasicLit).Value)
			out[k] = true
		}
		return false
	})
	return out
}

// ── the audit ────────────────────────────────────────────────────────────────

func validAccess(name string, a access) []string {
	var p []string
	switch a.kind {
	case kindPublic, kindStub:
	case kindAuthenticated:
		if a.why == "" {
			p = append(p, name+": kindAuthenticated needs a reason")
		}
	case kindSelf, kindAdmin, kindChecked:
		if len(a.calls) == 0 {
			p = append(p, name+": "+string(a.kind)+" must name the calls that enforce it")
		}
	default:
		p = append(p, name+": unknown kind "+string(a.kind))
	}
	return p
}

func auditBody(name string, a access, fb *funcBody) []string {
	var p []string
	for _, c := range a.calls {
		if !fb.calls[c] {
			p = append(p, fmt.Sprintf("%s (%s, %s): body no longer calls %s", name, a.kind, fb.file, c))
		}
	}
	if a.kind == kindStub && fb.usesReceiver {
		p = append(p, fmt.Sprintf("%s: a stub must not use its receiver (%s); give it a real access entry", name, fb.file))
	}
	return p
}

// auditOperations checks the strict-interface side.
func auditOperations(ops []string, table map[string]access, funcs map[string]*funcBody, dup map[string]bool, public map[string]bool) []string {
	var p []string
	listed := map[string]bool{}
	for _, op := range ops {
		listed[op] = true
		a, ok := table[op]
		if !ok {
			p = append(p, op+": operation has no entry in operationAccess (add how it is protected)")
			continue
		}
		p = append(p, validAccess(op, a)...)
		if (a.kind == kindPublic) != public[op] {
			p = append(p, op+": kindPublic must match publicOps in internal/server/server.go")
		}
		if dup[op] {
			p = append(p, op+": more than one function with this name; cannot audit")
			continue
		}
		fb, ok := funcs[op]
		if !ok {
			p = append(p, op+": no handler function found")
			continue
		}
		p = append(p, auditBody(op, a, fb)...)
	}
	for op := range table {
		if !listed[op] {
			p = append(p, op+": entry in operationAccess has no operation any more (remove it)")
		}
	}
	return p
}

// auditRoutes checks the hand-registered routes.
func auditRoutes(routes []route, table map[string]access, lookup func(pkg, name string) (*funcBody, bool, bool)) []string {
	var p []string
	seen := map[string]bool{}
	for _, r := range routes {
		seen[r.key] = true
		a, ok := table[r.key]
		if !ok {
			p = append(p, r.key+": route has no entry in routeAccess (add how it is protected)")
			continue
		}
		p = append(p, validAccess(r.key, a)...)
		fb := r.lit
		if fb == nil {
			var found, ambiguous bool
			fb, found, ambiguous = lookup(a.pkg, r.handler)
			if ambiguous {
				p = append(p, r.key+": handler "+r.handler+" is declared more than once; cannot audit")
				continue
			}
			if !found {
				p = append(p, r.key+": handler "+r.handler+" not found")
				continue
			}
		}
		p = append(p, auditBody(r.key, a, fb)...)
	}
	for k := range table {
		if !seen[k] {
			p = append(p, k+": entry in routeAccess has no route any more (remove it)")
		}
	}
	return p
}

func strictOperations() []string {
	typ := reflect.TypeOf((*StrictServerInterface)(nil)).Elem()
	out := make([]string, 0, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		out = append(out, typ.Method(i).Name)
	}
	sort.Strings(out)
	return out
}

func TestAuthzCoverage(t *testing.T) {
	funcs, dup := loadFuncs(t, ".")
	pkgs := map[string]struct {
		f map[string]*funcBody
		d map[string]bool
	}{".": {funcs, dup}}
	lookup := func(pkg, name string) (*funcBody, bool, bool) {
		if pkg == "" {
			pkg = "."
		}
		e, ok := pkgs[pkg]
		if !ok {
			f, d := loadFuncs(t, pkg)
			e = struct {
				f map[string]*funcBody
				d map[string]bool
			}{f, d}
			pkgs[pkg] = e
		}
		fb, found := e.f[name]
		return fb, found, e.d[name]
	}

	ops := strictOperations()
	if len(ops) < 50 {
		t.Fatalf("found only %d strict operations; the enumeration is broken", len(ops))
	}
	problems := auditOperations(ops, operationAccess, funcs, dup, publicOps(t, "../server/server.go"))

	routes := loadRoutes(t, "../server/server.go", "handlers_lens.go")
	if len(routes) < 15 {
		t.Fatalf("found only %d hand-registered routes; the enumeration is broken", len(routes))
	}
	problems = append(problems, auditRoutes(routes, routeAccess, lookup)...)

	sort.Strings(problems)
	for _, pr := range problems {
		t.Error(pr)
	}
}

// TestAuthzCoverage_Detects proves the audit fails when it should: an unlisted
// operation, a stale entry, a guard that left the handler body, and a stub that
// grew logic.
func TestAuthzCoverage_Detects(t *testing.T) {
	body := func(recv bool, calls ...string) *funcBody {
		fb := &funcBody{calls: map[string]bool{}, usesReceiver: recv, file: "x.go"}
		for _, c := range calls {
			fb.calls[c] = true
		}
		return fb
	}
	funcs := map[string]*funcBody{
		"Guarded":   body(true, "Connection"),
		"Unguarded": body(true),
		"Stub":      body(true, "Query"),
		"NewOne":    body(true),
	}
	table := map[string]access{
		"Guarded":   checked("Connection"),
		"Unguarded": checked("Connection"),
		"Stub":      {kind: kindStub},
		"Gone":      {kind: kindPublic},
	}
	got := strings.Join(auditOperations([]string{"Guarded", "Unguarded", "Stub", "NewOne"}, table, funcs, nil, map[string]bool{"Gone": true}), "\n")
	for _, want := range []string{
		"NewOne: operation has no entry",
		"Unguarded (checked, x.go): body no longer calls Connection",
		"Stub: a stub must not use its receiver",
		"Gone: entry in operationAccess has no operation any more",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("audit did not report %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Guarded (") {
		t.Errorf("a correctly guarded handler was reported:\n%s", got)
	}

	// A public flag that disagrees with publicOps is reported.
	got = strings.Join(auditOperations([]string{"Open"}, map[string]access{"Open": {kind: kindPublic}}, map[string]*funcBody{"Open": body(false)}, nil, map[string]bool{}), "\n")
	if !strings.Contains(got, "kindPublic must match publicOps") {
		t.Errorf("public/publicOps mismatch not reported:\n%s", got)
	}

	// Routes: unlisted, and a handler that lost its guard.
	routes := []route{{key: "GET /new", handler: "h"}, {key: "GET /old", handler: "h"}}
	lookup := func(_, name string) (*funcBody, bool, bool) { return body(false), name == "h", false }
	got = strings.Join(auditRoutes(routes, map[string]access{"GET /old": checked("Workspace")}, lookup), "\n")
	for _, want := range []string{"GET /new: route has no entry", "GET /old (checked, x.go): body no longer calls Workspace"} {
		if !strings.Contains(got, want) {
			t.Errorf("route audit did not report %q; got:\n%s", want, got)
		}
	}
}
