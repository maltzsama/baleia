package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/maltzsama/baleia/internal/authz"
	"github.com/maltzsama/baleia/internal/db"
	"github.com/maltzsama/baleia/internal/dialect"
	"github.com/maltzsama/baleia/internal/semantic"
	"github.com/maltzsama/baleia/internal/sqlref"
)

// Lineage (B4, #558): the graph of what depends on what, across tables,
// semantic models, metrics and saved queries. A model or metric names its base
// table directly in its definition; a saved query's tables come from its SQL
// (sqlref). Name resolution is best-effort and exact by natural key: a
// reference whose table no longer exists contributes no edge.

const (
	lineageDefaultDepth = 1
	lineageMaxDepth     = 8
)

// lineageReads reads the rows the lineage graph and the broken-lineage
// attention item need. *repo.Repo implements it over the GORM store (ADR 0020, #772).
type lineageReads interface {
	GetMetadataTableByRef(ctx context.Context, arg db.GetMetadataTableByRefParams) (db.GetMetadataTableByRefRow, error)
	ListMetadataColumns(ctx context.Context, tableID db.UUID) ([]db.ListMetadataColumnsRow, error)
	GetMetadataTableByName(ctx context.Context, arg db.GetMetadataTableByNameParams) (db.UUID, error)
	ListLineageModels(ctx context.Context, arg db.ListLineageModelsParams) ([]db.ListLineageModelsRow, error)
	ListLineageMetrics(ctx context.Context, arg db.ListLineageMetricsParams) ([]db.ListLineageMetricsRow, error)
	ListLineageSavedQueries(ctx context.Context, arg db.ListLineageSavedQueriesParams) ([]db.ListLineageSavedQueriesRow, error)
	ListLineageBrokenModels(ctx context.Context) ([]db.ListLineageBrokenModelsRow, error)
	ListLineageBrokenMetrics(ctx context.Context) ([]db.ListLineageBrokenMetricsRow, error)
	// GetCatalogBindingForDisplay resolves a catalog's engine kind, so content
	// SQL is parsed with the dialect that will run it (#1091).
	GetCatalogBindingForDisplay(ctx context.Context, arg db.GetCatalogBindingForDisplayParams) (db.GetCatalogBindingForDisplayRow, error)
}

// popularTablesReads reads the workspace's popular tables.
type popularTablesReads interface {
	ListPopularTables(ctx context.Context, arg db.ListPopularTablesParams) ([]db.ListPopularTablesRow, error)
}

// lineageTableRef is a content item's reference to a table, by natural key.
type lineageTableRef struct {
	catalogID db.UUID
	namespace []string
	name      string
}

// key is the natural key as a comparable string (namespaces joined).
func (r lineageTableRef) key() string {
	return idString(r.catalogID) + "\x00" + strings.Join(r.namespace, "\x1f") + "\x00" + r.name
}

// ListLineage — GET /workspaces/{id}/lineage
func (h *CatalogHandlers) ListLineage(ctx context.Context, request ListLineageRequestObject) (ListLineageResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListLineage404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListLineage401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{
			Code: ErrorCodeUnauthorized, Message: "invalid user",
		}}, nil
	}

	kind, id, err := parseLineageNode(request.Params.Node)
	if err != nil {
		return ListLineage404JSONResponse{NotFoundJSONResponse: metadataNotFound("invalid node")}, nil
	}
	direction := Down
	if request.Params.Direction != nil {
		direction = *request.Params.Direction
	}
	depth := lineageDefaultDepth
	if request.Params.Depth != nil {
		depth = *request.Params.Depth
		if depth < 1 {
			depth = 1
		}
		if depth > lineageMaxDepth {
			depth = lineageMaxDepth
		}
	}

	content, dependents, err := h.lineageContent(ctx, ws, p.UserID)
	if err != nil {
		return nil, err
	}
	g := &lineageGraph{
		nodes:     map[string]LineageNode{},
		edgeSet:   map[string]bool{},
		tableKeys: map[string]string{},
		ws:        ws,
		ctx:       ctx,
		q:         h.lineage,
	}

	var start string
	switch kind {
	case LineageNodeKindTable:
		t, notFound, err := h.grantedTable(ctx, ws, id)
		if err != nil {
			return nil, err
		}
		if notFound {
			return ListLineage404JSONResponse{NotFoundJSONResponse: metadataNotFound("table not found")}, nil
		}
		start = g.addTable(t)
	default:
		nodeID := nodeID(kind, db.UUID{Bytes: id, Valid: true})
		item, ok := content[nodeID]
		if !ok {
			return ListLineage404JSONResponse{NotFoundJSONResponse: metadataNotFound("node not found")}, nil
		}
		start = nodeID
		g.addNode(item.node)
	}

	g.walk(start, direction, depth, content, dependents)
	return ListLineage200JSONResponse(LineageGraph{Nodes: g.nodeSlice(), Edges: g.edgeSlice()}), nil
}

// lineageItem is one content node and the tables it reads.
type lineageItem struct {
	node LineageNode
	refs []lineageTableRef
}

// lineageContent loads the models, metrics and saved queries the caller can
// read in the workspace, indexed by node id, plus the reverse map from a
// table's natural key to the content that references it.
func (h *CatalogHandlers) lineageContent(ctx context.Context, ws, userID db.UUID) (map[string]lineageItem, map[string][]string, error) {
	content := map[string]lineageItem{}
	dependents := map[string][]string{}
	// One dialect per target for the whole request: a content item's SQL is
	// parsed with the dialect of the engine it runs on (#1091).
	dialects := map[lineageDialectKey]dialect.Dialect{}

	models, err := h.lineage.ListLineageModels(ctx, db.ListLineageModelsParams{WorkspaceID: ws, UserID: userID})
	if err != nil {
		return nil, nil, err
	}
	for _, m := range models {
		item := lineageItem{
			node: LineageNode{
				Kind:   LineageNodeKindModel,
				Id:     nodeID(LineageNodeKindModel, m.ID),
				Label:  m.Label,
				Detail: ptrString(m.Slug),
				Href:   ptrString("/models/" + idString(m.ID)),
			},
			refs: modelRefs(m.Definition, m.CatalogID, h.lineageDialect(ctx, m.CatalogID, m.BindingID, dialects)),
		}
		content[item.node.Id] = item
		for _, r := range item.refs {
			dependents[r.key()] = append(dependents[r.key()], item.node.Id)
		}
	}

	metrics, err := h.lineage.ListLineageMetrics(ctx, db.ListLineageMetricsParams{WorkspaceID: ws, UserID: userID})
	if err != nil {
		return nil, nil, err
	}
	for _, mt := range metrics {
		item := lineageItem{
			node: LineageNode{
				Kind:   LineageNodeKindMetric,
				Id:     nodeID(LineageNodeKindMetric, mt.ID),
				Label:  mt.Label,
				Detail: ptrString(mt.Slug),
				Href:   ptrString("/metrics/" + idString(mt.ID)),
			},
			refs: []lineageTableRef{{catalogID: mt.CatalogID, namespace: mt.Namespace, name: mt.TableName}},
		}
		content[item.node.Id] = item
		for _, r := range item.refs {
			dependents[r.key()] = append(dependents[r.key()], item.node.Id)
		}
	}

	queries, err := h.lineage.ListLineageSavedQueries(ctx, db.ListLineageSavedQueriesParams{WorkspaceID: ws, UserID: userID})
	if err != nil {
		return nil, nil, err
	}
	for _, q := range queries {
		item := lineageItem{
			node: LineageNode{
				Kind:   LineageNodeKindSavedQuery,
				Id:     nodeID(LineageNodeKindSavedQuery, q.ID),
				Label:  q.Name,
				Detail: ptrString(q.Slug),
				Href:   ptrString("/sql/q/" + idString(q.ID)),
			},
			refs: savedQueryRefs(q.Sql, q.TargetCatalogID, q.TargetSchema, h.lineageDialect(ctx, q.TargetCatalogID, q.TargetBindingID, dialects)),
		}
		content[item.node.Id] = item
		for _, r := range item.refs {
			dependents[r.key()] = append(dependents[r.key()], item.node.Id)
		}
	}
	return content, dependents, nil
}

// lineageGraph accumulates nodes and edges, deduplicating both.
type lineageGraph struct {
	nodes     map[string]LineageNode
	edges     []LineageEdge
	edgeSet   map[string]bool
	tableKeys map[string]string // table node id -> natural key
	ws        db.UUID           // workspace, for a table's deep link
	ctx       context.Context
	q         lineageReads
}

func (g *lineageGraph) addNode(n LineageNode) string {
	if _, ok := g.nodes[n.Id]; !ok {
		g.nodes[n.Id] = n
	}
	return n.Id
}

func (g *lineageGraph) addTable(t db.GetGrantedMetadataTableRow) string {
	id := nodeID(LineageNodeKindTable, t.ID)
	g.addNode(LineageNode{
		Kind:   LineageNodeKindTable,
		Id:     id,
		Label:  t.Name,
		Detail: ptrString(tableDetail(t.Namespace, t.Name)),
		Href:   ptrString(g.tableHref(t.CatalogID, t.Namespace, t.Name)),
	})
	g.tableKeys[id] = lineageTableRef{catalogID: t.CatalogID, namespace: t.Namespace, name: t.Name}.key()
	return id
}

func (g *lineageGraph) addEdge(from, to string) {
	key := from + "\x00" + to
	if g.edgeSet[key] {
		return
	}
	g.edgeSet[key] = true
	g.edges = append(g.edges, LineageEdge{From: from, To: to})
}

func (g *lineageGraph) nodeSlice() []LineageNode {
	out := make([]LineageNode, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, n)
	}
	return out
}

func (g *lineageGraph) edgeSlice() []LineageEdge { return g.edges }

// walk expands the graph from start, at most depth hops, in direction. Edges
// are always "content depends on table" regardless of traversal direction.
func (g *lineageGraph) walk(start string, direction ListLineageParamsDirection, depth int, content map[string]lineageItem, dependents map[string][]string) {
	frontier := []string{start}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []string
		for _, n := range frontier {
			switch direction {
			case Down:
				item, ok := content[n]
				if !ok {
					continue // a table: it depends on nothing
				}
				for _, ref := range item.refs {
					tableID, ok := g.resolveTable(ref)
					if !ok {
						continue
					}
					g.addEdge(n, tableID)
					next = append(next, tableID)
				}
			case Up:
				key, ok := g.tableKeys[n]
				if !ok {
					continue // content has no dependents in this graph
				}
				for _, cid := range dependents[key] {
					g.addNode(content[cid].node)
					g.addEdge(cid, n)
					next = append(next, cid)
				}
			}
		}
		frontier = next
	}
}

// resolveTable resolves a content reference to a live table node; ok is false
// when the table no longer exists.
func (g *lineageGraph) resolveTable(ref lineageTableRef) (string, bool) {
	row, err := g.q.GetMetadataTableByRef(g.ctx, db.GetMetadataTableByRefParams{
		CatalogID: ref.catalogID, Namespace: ref.namespace, Name: ref.name,
	})
	if errors.Is(err, db.ErrNoRows) || err != nil {
		return "", false
	}
	id := nodeID(LineageNodeKindTable, row.ID)
	g.addNode(LineageNode{
		Kind:   LineageNodeKindTable,
		Id:     id,
		Label:  row.Name,
		Detail: ptrString(tableDetail(row.Namespace, row.Name)),
		Href:   ptrString(g.tableHref(row.CatalogID, row.Namespace, row.Name)),
	})
	g.tableKeys[id] = lineageTableRef{catalogID: row.CatalogID, namespace: row.Namespace, name: row.Name}.key()
	return id, true
}

// lineageDialectKey identifies a content target in the per-request dialect
// cache: its binding when it has one, else its catalog.
type lineageDialectKey struct {
	binding db.UUID
	catalog db.UUID
}

// lineageDialect resolves the dialect to parse a content item's SQL with: the
// engine of its target binding (#1091), cached per target for one request. A
// content item with no binding falls back to the catalog's enabled binding,
// and the generic dialect is the last resort, so the graph stays best-effort.
func (h *CatalogHandlers) lineageDialect(ctx context.Context, catalogID, bindingID db.UUID, cache map[lineageDialectKey]dialect.Dialect) dialect.Dialect {
	if h.dialects == nil {
		return nil
	}
	key := lineageDialectKey{binding: bindingID, catalog: catalogID}
	if d, ok := cache[key]; ok {
		return d
	}
	d := h.dialects.MustGet(dialect.FlightSQL)
	if h.lineage != nil {
		arg := db.GetCatalogBindingForDisplayParams{CatalogID: catalogID, BindingID: bindingID}
		if row, err := h.lineage.GetCatalogBindingForDisplay(ctx, arg); err == nil {
			if resolved, ok := h.dialects.ForEngineKind(row.EngineKind); ok {
				d = resolved
			}
		}
	}
	cache[key] = d
	return d
}

// modelRefs extracts the tables a model reads from its definition, parsed with
// the target's dialect.
func modelRefs(definition []byte, catalogID db.UUID, d dialect.Dialect) []lineageTableRef {
	var m semantic.Model
	if err := json.Unmarshal(definition, &m); err != nil {
		return nil
	}
	switch m.Source.Kind {
	case semantic.SourceTable:
		return []lineageTableRef{{catalogID: catalogID, namespace: m.Source.Namespace, name: m.Source.Table}}
	case semantic.SourceSQL:
		return sqlRefs(m.Source.SQL, catalogID, m.Source.Namespace, d)
	}
	return nil
}

// savedQueryRefs extracts the tables a saved query reads from its SQL, parsed
// with the target's dialect.
func savedQueryRefs(sql string, catalogID db.UUID, schema *string, d dialect.Dialect) []lineageTableRef {
	var fallback []string
	if schema != nil && *schema != "" {
		fallback = []string{*schema}
	}
	return sqlRefs(sql, catalogID, fallback, d)
}

// sqlRefs runs the reference extractor over a statement and resolves each read
// table to a natural key, using fallback as the default namespace for
// unqualified names. A nil dialect keeps the generic extractor.
func sqlRefs(sql string, catalogID db.UUID, fallback []string, d dialect.Dialect) []lineageTableRef {
	var ref sqlref.Reference
	if d == nil {
		ref = sqlref.Extract(sql)
	} else {
		defaultSchema := ""
		if len(fallback) == 1 {
			defaultSchema = fallback[0]
		}
		ref = sqlref.ExtractResolved(d, defaultSchema, sql)
	}
	var out []lineageTableRef
	for _, t := range ref.Tables {
		if len(t.Path) == 0 || t.Kind != "read" {
			continue
		}
		ns := fallback
		if len(t.Path) >= 2 {
			ns = []string{t.Path[len(t.Path)-2]}
		}
		out = append(out, lineageTableRef{catalogID: catalogID, namespace: ns, name: t.Path[len(t.Path)-1]})
	}
	return out
}

func tableDetail(namespace []string, name string) string {
	return strings.Join(append(append([]string{}, namespace...), name), ".")
}

// tableHref is the data explorer route that opens the table. It carries the
// workspace: the URL is the source of truth for the selected one (#732).
func (g *lineageGraph) tableHref(catalogID db.UUID, namespace []string, name string) string {
	q := url.Values{}
	q.Set("ws", idString(g.ws))
	q.Set("catalog", idString(catalogID))
	for _, part := range namespace {
		q.Add("ns", part)
	}
	q.Set("table", name)
	return "/data?" + q.Encode()
}

func nodeID(kind LineageNodeKind, id db.UUID) string {
	return string(kind) + ":" + uuid.UUID(id.Bytes).String()
}

func splitNodeID(id string) (LineageNodeKind, uuid.UUID) {
	i := strings.IndexByte(id, ':')
	if i < 0 {
		return "", uuid.Nil
	}
	raw, err := uuid.Parse(id[i+1:])
	if err != nil {
		return "", uuid.Nil
	}
	return LineageNodeKind(id[:i]), raw
}

func parseLineageNode(node string) (LineageNodeKind, uuid.UUID, error) {
	kind, id := splitNodeID(node)
	switch kind {
	case LineageNodeKindTable, LineageNodeKindModel, LineageNodeKindMetric, LineageNodeKindSavedQuery:
		return kind, id, nil
	}
	return "", uuid.Nil, errors.New("invalid lineage node")
}
