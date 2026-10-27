package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/conntest"
)

// fixture creates root/app.db with a table and a view, plus a database outside
// the root and a symlink inside the root pointing at it.
func fixture(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	create := func(path string) {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, s := range []string{
			`CREATE TABLE orders (id INTEGER NOT NULL PRIMARY KEY, amount NUMERIC(12,2), note TEXT)`,
			`CREATE VIEW big_orders AS SELECT * FROM orders WHERE amount > 100`,
		} {
			if _, err := db.Exec(s); err != nil {
				t.Fatal(err)
			}
		}
	}
	create(filepath.Join(root, "app.db"))
	outside = filepath.Join(base, "secret.db")
	create(outside)
	if err := os.Symlink(outside, filepath.Join(root, "link.db")); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

func TestSQLiteRootConfinement(t *testing.T) {
	t.Parallel()
	root, outside := fixture(t)
	p := New(Options{Root: root})
	for _, path := range []string{"../secret.db", outside, "link.db", filepath.Join(root, "..", "secret.db")} {
		if _, err := p.Resolve(path); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("%s: err = %v, want ErrOutsideRoot", path, err)
		}
		if _, err := p.Validate(connections.Config{Values: map[string]string{"path": path}}); err == nil {
			t.Errorf("%s: Validate accepted it", path)
		}
	}
	for _, path := range []string{"app.db", filepath.Join(root, "app.db")} {
		if _, err := p.Resolve(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
	if _, err := p.Resolve("missing.db"); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("missing file: %v", err)
	}
	disabled := New(Options{})
	if disabled.Status().State != connections.StateUnsupported {
		t.Error("without BALEIA_SQLITE_ROOT the provider must be unsupported")
	}
	if _, err := disabled.Validate(connections.Config{Values: map[string]string{"path": "app.db"}}); err == nil {
		t.Error("Validate accepted a path while SQLite is disabled")
	}
}

func TestMetadataReader_sqlite(t *testing.T) {
	t.Parallel()
	root, _ := fixture(t)
	p := New(Options{Root: root})
	ctx := context.Background()
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"path": "app.db"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	r, err := p.OpenMetadata(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	nss, err := r.ListNamespaces(ctx, Catalog, nil)
	if err != nil || len(nss) != 1 || len(nss[0]) != 0 {
		t.Fatalf("namespaces = %v, %v", nss, err)
	}
	tables, _, err := r.ListTables(ctx, Catalog, connections.Namespace{}, "")
	if err != nil || len(tables) != 2 || tables[0].Name != "big_orders" || tables[0].Type != "VIEW" || tables[1].Type != "TABLE" {
		t.Fatalf("tables = %+v, %v", tables, err)
	}
	meta, err := r.LoadTable(ctx, tables[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Columns) != 3 || meta.Columns[0].Nullable || meta.Columns[1].NativeType != "NUMERIC(12,2)" || !meta.Columns[2].Nullable {
		t.Errorf("columns = %+v", meta.Columns)
	}
	conntest.CheckWalk(t, r, Catalog, tables...)

	if _, err := r.LoadTable(ctx, connections.TableRef{Catalog: Catalog, Name: "nope"}); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("missing table: %v", err)
	}
}

// TestReadOnlyUnlessBothAllow: writes need the connection and the server.
func TestReadOnlyUnlessBothAllow(t *testing.T) {
	t.Parallel()
	root, _ := fixture(t)
	ctx := context.Background()
	write := func(p *Provider, rw string) error {
		db, err := p.open(connections.Config{Values: map[string]string{"path": "app.db", "read_write": rw}})
		if err != nil {
			return err
		}
		defer db.Close()
		_, err = db.ExecContext(ctx, `INSERT INTO orders (id) VALUES (1)`)
		return err
	}
	if err := write(New(Options{Root: root}), "true"); err == nil {
		t.Error("wrote without BALEIA_SQLITE_ALLOW_WRITE")
	}
	if err := write(New(Options{Root: root, AllowWrite: true}), "false"); err == nil {
		t.Error("wrote without read_write on the connection")
	}
	if err := write(New(Options{Root: root, AllowWrite: true}), "true"); err != nil {
		t.Errorf("both allow writes: %v", err)
	}
}
