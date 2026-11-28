package api

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/maltzsama/baleia/internal/db"
	"github.com/maltzsama/baleia/internal/dialect"
	"github.com/maltzsama/baleia/internal/semantic"
	"github.com/maltzsama/baleia/internal/semantic/compiler"
	"github.com/maltzsama/baleia/internal/semantic/metrics"
	"github.com/maltzsama/baleia/internal/semanticcatalog"
)

// BISemanticCatalog returns the reader the BI gateway serves as the workspace
// semantic catalog (ADR 0030). publicURL is Baleia's web origin, used for the
// links back to each item; empty leaves the links out.
func (a *API) BISemanticCatalog(publicURL string) semanticcatalog.Reader {
	return &biSemanticCatalog{catalog: a.CatalogHandlers, semantic: a.SemanticHandlers, publicURL: strings.TrimRight(publicURL, "/")}
}

type biSemanticCatalog struct {
	catalog   *CatalogHandlers
	semantic  *SemanticHandlers
	publicURL string
}

// engineCatalogs maps each catalog the workspace may use on one engine to the
// engine's name for it: the name OpenMetadata's Trino connector sees through
// the gateway.
type engineCatalogs map[[16]byte]string

// Read builds the catalog the key's owner can see in the key's workspace.
// Only content in spaces the owner can read is returned, and only physical
// tables granted to the workspace and reachable on the key's engine are named.
func (c *biSemanticCatalog) Read(ctx context.Context, req semanticcatalog.Request) (semanticcatalog.Snapshot, error) {
	h := c.catalog
	ws, err := h.access.Workspace(ctx, req.WorkspaceID)
	if err != nil {
		return semanticcatalog.Snapshot{}, err
	}
	targets, err := h.access.Targets(ctx, req.WorkspaceID)
	if err != nil {
		return semanticcatalog.Snapshot{}, err
	}
	catalogs := engineCatalogs{}
	var engineDialect dialect.Dialect
	for _, t := range targets {
		if !usable(t) || t.EngineID.Bytes != req.EngineID.Bytes {
			continue
		}
		catalogs[t.CatalogID.Bytes] = t.EngineCatalogName
		if engineDialect == nil && h.dialects != nil {
			if d, ok := h.dialects.ForEngineKind(t.EngineKind); ok {
				engineDialect = d
			}
		}
	}

	rows, err := h.lineage.ListLineageModels(ctx, db.ListLineageModelsParams{WorkspaceID: req.WorkspaceID, UserID: req.UserID})
	if err != nil {
		return semanticcatalog.Snapshot{}, err
	}
	metricItems, err := c.semantic.metricDefs.List(ctx, req.WorkspaceID, req.UserID, metrics.Filter{})
	if err != nil {
		return semanticcatalog.Snapshot{}, err
	}
	if len(rows)+len(metricItems) > semanticcatalog.MaxItems {
		return semanticcatalog.Snapshot{}, semanticcatalog.ErrTooLarge
	}

	defs := make(map[string]semantic.Model, len(rows))
	parsed := make([]semantic.Model, len(rows))
	for i, row := range rows {
		// A definition that does not parse still lists the model, without
		// columns: its identity and lineage are still useful.
		_ = json.Unmarshal(row.Definition, &parsed[i])
		parsed[i].Slug = row.Slug
		defs[row.Slug] = parsed[i]
	}

	dialects := map[lineageDialectKey]dialect.Dialect{}
	snap := semanticcatalog.Snapshot{
		Workspace: semanticcatalog.Workspace{ID: req.WorkspaceID.String(), Name: ws.Name, URL: c.link("/?ws=" + req.WorkspaceID.String())},
		Models:    make([]semanticcatalog.Model, 0, len(rows)),
		Metrics:   make([]semanticcatalog.Metric, 0, len(metricItems)),
	}
	for i, row := range rows {
		refs := modelRefs(row.Definition, row.CatalogID, h.lineageDialect(ctx, row.CatalogID, row.BindingID, dialects))
		tables, err := c.sourceTables(ctx, req.WorkspaceID, refs, catalogs)
		if err != nil {
			return semanticcatalog.Snapshot{}, err
		}
		snap.Models = append(snap.Models, c.model(row, parsed[i], defs, catalogs[row.CatalogID.Bytes], engineDialect, tables))
	}
	for _, item := range metricItems {
		m := item.Metric
		ref := lineageTableRef{catalogID: m.CatalogID, namespace: m.Namespace, name: m.TableName}
		tables, err := c.sourceTables(ctx, req.WorkspaceID, []lineageTableRef{ref}, catalogs)
		if err != nil {
			return semanticcatalog.Snapshot{}, err
		}
		snap.Metrics = append(snap.Metrics, semanticcatalog.Metric{
			ID: m.ID.String(), Slug: m.Slug, Label: m.Label, Description: m.Description,
			URL: c.link("/metrics/" + m.ID.String()), UpdatedAt: m.UpdatedAt.Time.UTC(),
			SQL:          metricSQL(m, catalogs[m.CatalogID.Bytes], engineDialect),
			SourceTables: tables,
		})
	}
	sort.Slice(snap.Models, func(i, j int) bool { return snap.Models[i].Slug < snap.Models[j].Slug })
	sort.Slice(snap.Metrics, func(i, j int) bool { return snap.Metrics[i].Slug < snap.Metrics[j].Slug })
	return snap, nil
}

// model maps one semantic model. engineCatalog is the engine's name for the
// model's catalog, empty when the catalog is not on the key's engine.
func (c *biSemanticCatalog) model(row db.ListLineageModelsRow, def semantic.Model, defs map[string]semantic.Model, engineCatalog string, d dialect.Dialect, tables []string) semanticcatalog.Model {
	out := semanticcatalog.Model{
		ID: row.ID.String(), Slug: row.Slug, Label: row.Label, Description: def.Description,
		URL: c.link("/models/" + row.ID.String()), UpdatedAt: row.UpdatedAt.Time.UTC(),
		Source:       semanticcatalog.ModelSource{Kind: string(def.Source.Kind)},
		Columns:      make([]semanticcatalog.Column, 0, len(def.Dimensions)),
		Metrics:      make([]semanticcatalog.ModelField, 0, len(def.Metrics)),
		SourceTables: tables,
	}
	switch def.Source.Kind {
	case semantic.SourceTable:
		if engineCatalog != "" {
			out.Source.Table = qualifiedName(engineCatalog, def.Source.Namespace, def.Source.Table)
		}
	case semantic.SourceSQL:
		out.Source.SQL = strings.TrimSpace(def.Source.SQL)
	}
	for _, dim := range def.Dimensions {
		if dim.Hidden {
			continue
		}
		out.Columns = append(out.Columns, semanticcatalog.Column{
			Name: dim.Name, Label: dim.Label, Description: dim.Description,
			Type: string(dim.Type), Expression: strings.TrimSpace(dim.SQL),
		})
	}
	for _, mt := range def.Metrics {
		field := semanticcatalog.ModelField{Name: mt.Name, Label: mt.Label}
		if d != nil && engineCatalog != "" {
			// The same compiler as Explore, selecting only this metric, so the
			// SQL is what Baleia would run.
			res, err := compiler.Compile(semantic.ExploreQuery{Model: def.Slug, Metrics: []string{mt.Name}}, defs, d, compiler.Options{Catalog: engineCatalog})
			if err == nil {
				field.SQL = res.SQL
			}
		}
		out.Metrics = append(out.Metrics, field)
	}
	return out
}

// metricSQL renders a standalone metric for the key's engine; empty when the
// metric's catalog is not on that engine or the definition does not render.
func metricSQL(m db.SemanticMetric, engineCatalog string, d dialect.Dialect) string {
	if d == nil || engineCatalog == "" {
		return ""
	}
	def, _, err := metrics.ParseDefinitionRaw(m.Definition)
	if err != nil {
		return ""
	}
	sql, err := semantic.Render(def, semantic.TableRef{Catalog: engineCatalog, Schema: m.Namespace, Table: m.TableName}, d, semantic.RenderOptions{Alias: m.Slug})
	if err != nil {
		return ""
	}
	return sql
}

// sourceTables resolves references to the physical tables that exist in the
// workspace's metadata, are granted to the workspace and sit on the key's
// engine, named as that engine names them. Others are left out.
func (c *biSemanticCatalog) sourceTables(ctx context.Context, ws db.UUID, refs []lineageTableRef, catalogs engineCatalogs) ([]string, error) {
	h := c.catalog
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		row, err := h.lineage.GetMetadataTableByRef(ctx, db.GetMetadataTableByRefParams{CatalogID: ref.catalogID, Namespace: ref.namespace, Name: ref.name})
		if errors.Is(err, db.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		table, missing, err := h.grantedTable(ctx, ws, row.ID.Bytes)
		if err != nil {
			return nil, err
		}
		engineCatalog := catalogs[table.CatalogID.Bytes]
		if missing || engineCatalog == "" {
			continue
		}
		name := qualifiedName(engineCatalog, table.Namespace, table.Name)
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// qualifiedName is catalog.schema.table, unquoted, as catalogs display it.
func qualifiedName(catalog string, namespace []string, table string) string {
	parts := make([]string, 0, len(namespace)+2)
	parts = append(parts, catalog)
	parts = append(parts, namespace...)
	parts = append(parts, table)
	return strings.Join(parts, ".")
}

// link is an absolute link into Baleia's web UI, or "" without a public URL.
func (c *biSemanticCatalog) link(path string) string {
	if c.publicURL == "" {
		return ""
	}
	return c.publicURL + path
}

var _ semanticcatalog.Reader = (*biSemanticCatalog)(nil)
