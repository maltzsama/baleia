// Package sqlite is the SQLite provider (modernc.org/sqlite, pure Go). It
// opens no network connection; its guard is the file system: a database file
// must resolve, after cleaning and following symlinks, inside
// BALEIA_SQLITE_ROOT, and opens read-only unless both the connection and the
// server allow writes (docs/adr/0011).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

const statementTimeout = 30 * time.Second

// Catalog is the only catalog of a SQLite database.
const Catalog = "main"

// Options are the server-side SQLite settings.
type Options struct {
	Root       string // BALEIA_SQLITE_ROOT; empty disables the provider
	AllowWrite bool   // BALEIA_SQLITE_ALLOW_WRITE
}

type Provider struct {
	opts Options
}

func New(o Options) *Provider {
	p := &Provider{opts: o}
	p.WarnIfRootWritable()
	return p
}

func (*Provider) Kind() connections.Kind     { return connections.KindSQLite }
func (*Provider) Label() string              { return "SQLite" }
func (*Provider) Family() connections.Family { return connections.FamilyDatabase }
func (*Provider) Roles() []connections.Role {
	return []connections.Role{connections.RoleMetadata, connections.RoleDirect}
}
func (*Provider) EngineKind() string { return "sqlite" }

// WalksCatalogs: the reader reads a whole catalog in one query (#15).
func (*Provider) WalksCatalogs() bool { return true }

var _ connections.CatalogWalker = (*reader)(nil)

func (p *Provider) Status() connections.Status {
	if p.opts.Root == "" {
		return connections.Status{State: connections.StateUnsupported, Reason: "BALEIA_SQLITE_ROOT not set"}
	}
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{Key: "path", Label: "Database file", Type: connections.FieldFilePath, Required: true, Help: "Path inside BALEIA_SQLITE_ROOT (relative to it, or absolute)"},
		{Key: "read_write", Label: "Allow writes", Type: connections.FieldBool, Default: "false", Help: "Takes effect only when the server sets BALEIA_SQLITE_ALLOW_WRITE=true"},
	}
}

// ErrOutsideRoot means the path escapes BALEIA_SQLITE_ROOT.
var ErrOutsideRoot = errors.New("database file is outside BALEIA_SQLITE_ROOT")

// Resolve returns the real path of a database file, confined to the
// provider's root.
func (p *Provider) Resolve(path string) (string, error) {
	return ResolvePath(p.opts.Root, path)
}

// ResolvePath returns the real path of a database file, refusing anything that
// leaves root (../, absolute paths elsewhere, symlinks out of it). The
// provider and the query engine (#83) confine through it, so both apply the
// same rule (docs/adr/0011).
func ResolvePath(root, path string) (string, error) {
	if root == "" {
		return "", errors.New("BALEIA_SQLITE_ROOT not set")
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("BALEIA_SQLITE_ROOT: %w", err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: database file not found", connections.ErrNotFound)
		}
		return "", err
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrOutsideRoot
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("database path is not a regular file")
	}
	return real, nil
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	if p.opts.Root == "" {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: "path", Message: "SQLite is disabled: BALEIA_SQLITE_ROOT is not set"}}}
	}
	if _, err := p.Resolve(out.Values["path"]); err != nil {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: "path", Message: err.Error()}}}
	}
	return out, nil
}

// writable reports whether both the connection and the server allow writes.
func (p *Provider) writable(cfg connections.Config) bool {
	return WriteAllowed(p.opts.AllowWrite, cfg)
}

// WriteAllowed reports whether a connection may write: both the server
// (BALEIA_SQLITE_ALLOW_WRITE) and the connection (read_write) must allow it.
func WriteAllowed(serverAllowWrite bool, cfg connections.Config) bool {
	return serverAllowWrite && cfg.Value("read_write") == "true"
}

// DSN is the file: URI of an already-resolved path. It is read-only (mode=ro
// plus query_only) unless writable, and always bounds lock waits. The query
// engine (#83) opens through it, so both share the read-only rule.
func DSN(path string, writable bool) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	if writable {
		q.Set("mode", "rw")
	} else {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
	}
	return (&url.URL{Scheme: "file", Path: path}).String() + "?" + q.Encode()
}

// open opens the resolved file. Confinement is path-based: it holds while
// nobody who must not escape the root can replace a component of a path under
// it (docs/adr/0011). The path is resolved again right after opening, so a
// swap that lands inside that window is detected rather than used.
func (p *Provider) open(cfg connections.Config) (*sql.DB, error) {
	path, err := p.Resolve(cfg.Value("path"))
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", DSN(path, p.writable(cfg)))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if again, err := p.Resolve(cfg.Value("path")); err != nil || again != path {
		db.Close()
		return nil, ErrOutsideRoot
	}
	return db, nil
}

// WarnIfRootWritable logs when the root is writable by group or others: then
// someone other than the operator may swap a path component (docs/adr/0011).
func (p *Provider) WarnIfRootWritable() {
	if p.opts.Root == "" {
		return
	}
	info, err := os.Stat(p.opts.Root)
	if err == nil && info.Mode().Perm()&0o022 != 0 {
		slog.Warn("BALEIA_SQLITE_ROOT is writable by group or others; SQLite confinement assumes only operators can change files under it", "root", p.opts.Root)
	}
}

func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	db, err := p.open(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	var one int
	return db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

func (p *Provider) OpenMetadata(ctx context.Context, cfg connections.Config) (connections.MetadataReader, error) {
	db, err := p.open(cfg)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &reader{db: db}, nil
}

// reader exposes the file as catalog "main" with one, unnamed namespace.
type reader struct{ db *sql.DB }

func (r *reader) ListCatalogs(context.Context) ([]connections.CatalogRef, error) {
	return []connections.CatalogRef{{Name: Catalog, Kind: "database"}}, nil
}

func check(catalog string, ns connections.Namespace) error {
	if catalog != Catalog || len(ns) != 0 {
		return fmt.Errorf("%w: SQLite has catalog %q and no schemas", connections.ErrNotFound, Catalog)
	}
	return nil
}

func (r *reader) ListNamespaces(_ context.Context, catalog string, parent connections.Namespace) ([]connections.Namespace, error) {
	if catalog != Catalog {
		return nil, fmt.Errorf("%w: catalog", connections.ErrNotFound)
	}
	if len(parent) > 0 {
		return []connections.Namespace{}, nil
	}
	return []connections.Namespace{{}}, nil
}

func (r *reader) ListTables(ctx context.Context, catalog string, ns connections.Namespace, pageToken string) ([]connections.TableRef, string, error) {
	if err := check(catalog, ns); err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT name, type FROM sqlite_schema
		WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name > ?
		ORDER BY name`, pageToken)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []connections.TableRef{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, "", err
		}
		kind := "TABLE"
		if typ == "view" {
			kind = "VIEW"
		}
		out = append(out, connections.TableRef{Catalog: catalog, Namespace: connections.Namespace{}, Name: name, Type: kind})
	}
	return out, "", rows.Err()
}

func (r *reader) LoadTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error) {
	if err := check(t.Catalog, t.Namespace); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	var typ string
	err := r.db.QueryRowContext(ctx, `SELECT type FROM sqlite_schema WHERE type IN ('table', 'view') AND name = ?`, t.Name).Scan(&typ)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: table", connections.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT cid, name, type, "notnull" FROM pragma_table_info(?)`, t.Name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []connections.Column
	for rows.Next() {
		var cid, notnull int
		var c connections.Column
		if err := rows.Scan(&cid, &c.Name, &c.NativeType, &notnull); err != nil {
			return nil, err
		}
		c.Nullable = notnull == 0
		c.Position = cid + 1
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ref := t
	ref.Type = "TABLE"
	if typ == "view" {
		ref.Type = "VIEW"
	}
	return &connections.TableMeta{Ref: ref, Columns: cols, Format: "sqlite", Properties: map[string]string{}}, nil
}

// WalkCatalog reads every table and view of the database with its columns in
// one query.
func (r *reader) WalkCatalog(ctx context.Context, catalog string, visit func(connections.TableMeta) error) error {
	if err := check(catalog, nil); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, connections.WalkTimeout)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.name, s.type, COALESCE(p.name, ''), COALESCE(p.type, ''), COALESCE(p."notnull", 0), COALESCE(p.cid, -1)
		FROM sqlite_schema s
		LEFT JOIN pragma_table_info(s.name) p
		WHERE s.type IN ('table', 'view') AND s.name NOT LIKE 'sqlite\_%' ESCAPE '\'
		ORDER BY s.name, p.cid`)
	if err != nil {
		return err
	}
	defer rows.Close()
	g := connections.NewTableGrouper(catalog, visit)
	for rows.Next() {
		var typ string
		var notnull, cid int
		row := connections.CatalogRow{Namespace: connections.Namespace{}}
		if err := rows.Scan(&row.Table, &typ, &row.Column.Name, &row.Column.NativeType, &notnull, &cid); err != nil {
			return err
		}
		row.Column.Nullable, row.Column.Position = notnull == 0, cid+1
		row.Type, row.Format = "TABLE", "sqlite"
		if typ == "view" {
			row.Type = "VIEW"
		}
		if err := g.Add(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return g.Flush()
}

func (r *reader) Close() error { return r.db.Close() }
