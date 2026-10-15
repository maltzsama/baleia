package metadata

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5/pgtype"
)

// PathFor says how a catalog whose connection has provider p is read: from
// the provider itself when it reads metadata directly (an Iceberg catalog
// manager, or a database whose reader walks a catalog in one query), else
// through an engine. Federated engines' connections (catalogs adopted from
// Trino) always go through the engine.
func PathFor(p connections.Provider) string {
	if p == nil || !slices.Contains(p.Roles(), connections.RoleMetadata) || slices.Contains(p.Roles(), connections.RoleFederated) {
		return PathEngine
	}
	if p.Family() == connections.FamilyIceberg {
		return PathProvider
	}
	if w, ok := p.(connections.CatalogWalkerProvider); ok && w.WalksCatalogs() {
		return PathProvider
	}
	return PathEngine
}

// typeSystems names the type system of each walking provider's columns.
var typeSystems = map[connections.Kind]string{
	connections.KindPostgreSQL: "postgresql",
	connections.KindMySQL:      "mysql",
	connections.KindMariaDB:    "mysql",
	connections.KindSQLServer:  "tsql",
	connections.KindOracle:     "oracle",
	connections.KindSQLite:     "sqlite",
	connections.KindClickHouse: "clickhouse",
}

// SourceStore is the slice of the database Sources reads.
type SourceStore interface {
	GetCatalogConnectionByID(ctx context.Context, id pgtype.UUID) (db.CatalogConnection, error)
	ListSyncBindings(ctx context.Context, catalogID pgtype.UUID) ([]db.ListSyncBindingsRow, error)
}

// EngineSource returns engines by id; *queryengine.Manager implements it.
type EngineSource interface {
	Get(ctx context.Context, engineID pgtype.UUID) (queryengine.Engine, error)
}

// Sources is the production Opener.
type Sources struct {
	store     SourceStore
	keyring   *crypto.Keyring
	providers *connections.Registry
	engines   EngineSource
}

// NewSources returns the opener of catalog sources.
func NewSources(store SourceStore, keyring *crypto.Keyring, reg *connections.Registry, engines EngineSource) *Sources {
	return &Sources{store: store, keyring: keyring, providers: reg, engines: engines}
}

// Open picks the catalog's source (PathFor) and opens it.
func (s *Sources) Open(ctx context.Context, c db.GetCatalogForSyncRow) (string, Source, error) {
	kind := connections.Kind(c.ConnectionType)
	p, _ := s.providers.Get(kind)
	path := PathFor(p)
	var src Source
	var err error
	if path == PathProvider {
		src, err = s.openProvider(ctx, p, c)
	} else {
		src, err = s.openEngine(ctx, c)
	}
	return path, src, err
}

// sourceRef is the catalog's name inside its connection: the warehouse or
// database it was created on, or a native catalog's own name.
func sourceRef(c db.GetCatalogForSyncRow) string {
	if c.SourceRef != nil && *c.SourceRef != "" {
		return *c.SourceRef
	}
	if c.Warehouse != nil && *c.Warehouse != "" {
		return *c.Warehouse
	}
	return c.Name
}

func (s *Sources) openProvider(ctx context.Context, p connections.Provider, c db.GetCatalogForSyncRow) (Source, error) {
	conn, err := s.store.GetCatalogConnectionByID(ctx, c.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("load connection: %w", err)
	}
	plain, err := catalogs.OpenCredentials(conn, s.keyring)
	if err != nil {
		return nil, err
	}
	cfg, err := providers.FromStored(s.providers, p.Kind(), conn.ID, conn.Url, conn.Config, plain)
	if err != nil {
		return nil, err
	}
	r, err := p.OpenMetadata(ctx, cfg)
	if err != nil {
		return nil, err
	}
	ref := sourceRef(c)
	if p.Family() == connections.FamilyIceberg {
		return &icebergSource{r: r, catalog: ref}, nil
	}
	w, ok := r.(connections.CatalogWalker)
	if !ok {
		_ = r.Close()
		return nil, fmt.Errorf("%s readers cannot walk a catalog", p.Kind())
	}
	return &walkerSource{r: r, w: w, catalog: ref, typeSystem: typeSystems[p.Kind()]}, nil
}

func (s *Sources) openEngine(ctx context.Context, c db.GetCatalogForSyncRow) (Source, error) {
	bindings, err := s.store.ListSyncBindings(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, queryengine.NewError(queryengine.CodeUnavailable, "", "", "the catalog has no active binding to introspect it through")
	}
	b := bindings[0]
	eng, err := s.engines.Get(ctx, b.EngineID)
	if err != nil {
		return nil, err
	}
	m := eng.Dialect().MetadataQueries()
	if m.Catalog == "" {
		return nil, queryengine.NewError(queryengine.CodeInternal, eng.Kind(), "", "the engine cannot list a catalog in one query")
	}
	return &engineSource{eng: eng, catalog: b.EngineCatalogName, queries: m, typeSystem: eng.Dialect().Name()}, nil
}

// icebergSource walks an Iceberg catalog manager: namespaces recursively,
// tables page by page, and loads a table only when it changed. The
// fingerprint is the table's metadata-location, which changes on every
// commit; servers with ETags (Polaris) answer an unchanged table with 304.
type icebergSource struct {
	r       connections.MetadataReader
	catalog string
}

func (s *icebergSource) TypeSystem() string { return "iceberg" }
func (s *icebergSource) Close() error       { return s.r.Close() }

func (s *icebergSource) Walk(ctx context.Context, known func([]string, string) (Known, bool), v Visitor) error {
	return s.walkNamespace(ctx, nil, known, v)
}

func (s *icebergSource) walkNamespace(ctx context.Context, parent connections.Namespace, known func([]string, string) (Known, bool), v Visitor) error {
	children, err := s.r.ListNamespaces(ctx, s.catalog, parent)
	if err != nil {
		return err
	}
	for _, ns := range children {
		if len(ns) <= len(parent) || !slices.Equal(ns[:len(parent)], parent) {
			continue // not a child of parent: never loop on a bad answer
		}
		if err := v.Namespace(ns); err != nil {
			return err
		}
		if err := s.walkTables(ctx, ns, known, v); err != nil {
			return err
		}
		if err := s.walkNamespace(ctx, ns, known, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *icebergSource) walkTables(ctx context.Context, ns connections.Namespace, known func([]string, string) (Known, bool), v Visitor) error {
	cl, conditional := s.r.(connections.ConditionalLoader)
	token := ""
	for {
		page, next, err := s.r.ListTables(ctx, s.catalog, ns, token)
		if err != nil {
			return err
		}
		for _, ref := range page {
			k, seen := known(ref.Namespace, ref.Name)
			var meta *connections.TableMeta
			etag := ""
			switch {
			case conditional && seen && k.ETag != "":
				meta, etag, err = cl.LoadTableIfChanged(ctx, ref, k.ETag)
			case conditional:
				meta, etag, err = cl.LoadTableIfChanged(ctx, ref, "")
			default:
				meta, err = s.r.LoadTable(ctx, ref)
			}
			if errors.Is(err, connections.ErrNotFound) {
				continue // dropped between the listing and the load
			}
			if err != nil {
				return fmt.Errorf("load %v.%s: %w", ref.Namespace, ref.Name, err)
			}
			if meta == nil {
				if err := v.Table(TableSnapshot{Namespace: ref.Namespace, Name: ref.Name, Fingerprint: k.Fingerprint, Unchanged: true}); err != nil {
					return err
				}
				continue
			}
			t := snapshotOf(*meta)
			t.Fingerprint = meta.Properties["metadata_location"]
			if t.Fingerprint == "" {
				t.Fingerprint = columnsFingerprint(*meta)
			}
			if etag != "" {
				t.Properties["etag"] = etag
			}
			if err := v.Table(t); err != nil {
				return err
			}
		}
		if next == "" || next == token {
			return nil
		}
		token = next
	}
}

// walkerSource reads a database catalog in one query (connections.CatalogWalker).
type walkerSource struct {
	r          connections.MetadataReader
	w          connections.CatalogWalker
	catalog    string
	typeSystem string
}

func (s *walkerSource) TypeSystem() string { return s.typeSystem }
func (s *walkerSource) Close() error       { return s.r.Close() }

func (s *walkerSource) Walk(ctx context.Context, known func([]string, string) (Known, bool), v Visitor) error {
	catalog, err := s.resolveCatalog(ctx)
	if err != nil {
		return err
	}
	// Schemas without tables are recorded too (one cheap query).
	namespaces, err := s.r.ListNamespaces(ctx, catalog, nil)
	if err != nil {
		return err
	}
	for _, ns := range namespaces {
		if err := v.Namespace(ns); err != nil {
			return err
		}
	}
	return s.w.WalkCatalog(ctx, catalog, func(m connections.TableMeta) error {
		return v.Table(diffed(snapshotOf(m), columnsFingerprint(m), known))
	})
}

// resolveCatalog maps the catalog's reference to the reader's catalog name:
// readers of single-catalog servers (MySQL, ClickHouse, Oracle, SQLite) name
// their only catalog themselves.
func (s *walkerSource) resolveCatalog(ctx context.Context) (string, error) {
	cats, err := s.r.ListCatalogs(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range cats {
		if c.Name == s.catalog {
			return c.Name, nil
		}
	}
	if len(cats) == 1 {
		return cats[0].Name, nil
	}
	return "", fmt.Errorf("%w: catalog %q", connections.ErrNotFound, s.catalog)
}

// engineSource introspects a catalog through an engine, in a read-only
// session with the engine's service identity: the dialect's schemas query,
// then its one-query catalog listing (never a DESCRIBE per table).
type engineSource struct {
	eng        queryengine.Engine
	catalog    string
	queries    dialect.MetadataQueries
	typeSystem string
}

func (s *engineSource) TypeSystem() string { return s.typeSystem }
func (s *engineSource) Close() error       { return nil }

func (s *engineSource) Walk(ctx context.Context, known func([]string, string) (Known, bool), v Visitor) error {
	sess, err := s.eng.OpenSession(ctx, queryengine.SessionSpec{
		Catalog:  s.catalog,
		ReadOnly: true,
		Tags:     queryengine.Tags{Source: "sync"},
	})
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	m := s.queries
	err = sess.Execute(ctx, m.Schemas, queryengine.Callbacks{}, &rowSink{row: func(r []nullString) error {
		if len(r) == 0 || r[0].null {
			return nil
		}
		return v.Namespace([]string{r[0].s})
	}})
	if err != nil {
		return fmt.Errorf("list schemas: %w", err)
	}

	g := connections.NewTableGrouper(s.catalog, func(t connections.TableMeta) error {
		return v.Table(diffed(snapshotOf(t), columnsFingerprint(t), known))
	})
	err = sess.Execute(ctx, m.Catalog, queryengine.Callbacks{}, &rowSink{row: func(r []nullString) error {
		if len(r) < 7 {
			return fmt.Errorf("catalog query returned %d columns, want 7", len(r))
		}
		var pos int
		_, _ = fmt.Sscan(r[6].s, &pos)
		return g.Add(connections.CatalogRow{
			Namespace: connections.Namespace{r[0].s},
			Table:     r[1].s,
			Type:      r[2].s,
			Column: connections.Column{
				Name: r[3].s, NativeType: r[4].s, Nullable: r[5].s != "NO", Position: pos,
			},
		})
	}})
	if err != nil {
		return fmt.Errorf("list catalog: %w", err)
	}
	return g.Flush()
}

type nullString struct {
	s    string
	null bool
}

// rowSink hands each row of every result set to row, as strings.
type rowSink struct {
	row func([]nullString) error
}

func (s *rowSink) Begin(*arrow.Schema) error { return nil }

func (s *rowSink) Write(rec arrow.RecordBatch) error {
	defer rec.Release()
	cols := rec.Columns()
	vals := make([]nullString, len(cols))
	for i := range int(rec.NumRows()) {
		for j, c := range cols {
			if c.IsNull(i) {
				vals[j] = nullString{null: true}
			} else {
				vals[j] = nullString{s: c.ValueStr(i)}
			}
		}
		if err := s.row(vals); err != nil {
			return err
		}
	}
	return nil
}

func (s *rowSink) End(queryengine.Stats) error { return nil }

// diffed marks a table whose fingerprint matches the stored one unchanged:
// its columns were read anyway (one query per catalog), but nothing is
// rewritten.
func diffed(t TableSnapshot, fingerprint string, known func([]string, string) (Known, bool)) TableSnapshot {
	t.Fingerprint = fingerprint
	if k, ok := known(t.Namespace, t.Name); ok && k.Fingerprint == fingerprint {
		return TableSnapshot{Namespace: t.Namespace, Name: t.Name, Fingerprint: fingerprint, Unchanged: true}
	}
	return t
}

// snapshotOf converts a reader's table.
func snapshotOf(m connections.TableMeta) TableSnapshot {
	t := TableSnapshot{
		Namespace:  slices.Clone(m.Ref.Namespace),
		Name:       m.Ref.Name,
		Kind:       kindOf(m.Ref.Type),
		Comment:    m.Comment,
		Properties: map[string]string{},
	}
	for k, v := range m.Properties {
		if v != "" {
			t.Properties[k] = v
		}
	}
	if m.Format != "" {
		t.Properties["format"] = m.Format
	}
	for i, c := range m.Columns {
		ord := c.Position
		if ord <= 0 {
			ord = i + 1
		}
		t.Columns = append(t.Columns, ColumnSnapshot{Ordinal: ord, Name: c.Name, NativeType: c.NativeType, Nullable: c.Nullable, Comment: c.Comment})
	}
	return t
}

func kindOf(refType string) string {
	switch strings.ToUpper(refType) {
	case "VIEW":
		return KindView
	case "MATERIALIZED_VIEW":
		return KindMaterializedView
	}
	return KindTable
}

// columnsFingerprint is the md5 of what a database or engine reports about a
// table: its kind, comment and ordered columns.
func columnsFingerprint(m connections.TableMeta) string {
	h := md5.New()
	fmt.Fprintf(h, "%s\x1f%s\x1e", m.Ref.Type, m.Comment)
	for _, c := range m.Columns {
		fmt.Fprintf(h, "%d\x1f%s\x1f%s\x1f%t\x1f%s\x1e", c.Position, c.Name, c.NativeType, c.Nullable, c.Comment)
	}
	return hex.EncodeToString(h.Sum(nil))
}
