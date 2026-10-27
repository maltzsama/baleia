package flightsql

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/flight"
	"github.com/apache/arrow-go/v18/arrow/flight/flightsql"
	"github.com/apache/arrow-go/v18/arrow/flight/flightsql/example"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/galileostudio/baleia/internal/connections"
)

type validator struct{}

func (validator) Validate(user, pass string) (string, error) {
	if user == "alice" && pass == "s3cret" {
		return "tok-alice", nil
	}
	return "", errors.New("bad credentials")
}

func (validator) IsValid(token string) (any, error) {
	if token == "tok-alice" || token == "static-token" {
		return "alice", nil
	}
	return nil, errors.New("bad token")
}

// server starts Arrow's SQLite Flight SQL example (tables foreignTable and
// intTable) on loopback, optionally behind basic/bearer auth.
func server(t *testing.T, auth bool) (host, port string) {
	t.Helper()
	db, err := example.CreateDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE VIEW positive AS SELECT * FROM intTable WHERE value > 0`); err != nil {
		t.Fatal(err)
	}
	srv, err := example.NewSQLiteFlightSQLServer(db)
	if err != nil {
		t.Fatal(err)
	}
	var mw []flight.ServerMiddleware
	if auth {
		mw = append(mw, flight.CreateServerBasicAuthMiddleware(validator{}))
	}
	s := flight.NewServerWithMiddleware(mw)
	s.RegisterFlightService(flightsql.NewFlightServer(srv))
	if err := s.Init("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	t.Cleanup(s.Shutdown)
	host, port, _ = net.SplitHostPort(s.Addr().String())
	return host, port
}

func provider(t *testing.T) *Provider {
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	return New(d)
}

func TestMetadataReader_flightsql(t *testing.T) {
	t.Parallel()
	host, port := server(t, false)
	p := provider(t)
	ctx := context.Background()
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"host": host, "port": port, "tls": "false"}})
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
	cats, err := r.ListCatalogs(ctx)
	if err != nil || len(cats) != 1 {
		t.Fatalf("catalogs = %v, %v", cats, err)
	}
	catalog := cats[0].Name
	nss, err := r.ListNamespaces(ctx, catalog, nil)
	if err != nil || len(nss) != 1 {
		t.Fatalf("namespaces = %v, %v", nss, err)
	}
	tables, next, err := r.ListTables(ctx, catalog, nss[0], "")
	if err != nil || next != "" {
		t.Fatal(err)
	}
	var names []string
	for _, tb := range tables {
		names = append(names, tb.Name+":"+tb.Type)
	}
	if got := strings.Join(names, ","); got != "foreignTable:TABLE,intTable:TABLE,positive:VIEW,sqlite_sequence:TABLE" {
		t.Fatalf("tables = %s", got)
	}
	meta, err := r.LoadTable(ctx, tables[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Columns) != 4 || meta.Columns[0].Name != "id" || meta.Columns[1].Name != "keyName" || meta.Columns[3].Position != 4 {
		t.Errorf("columns = %+v", meta.Columns)
	}
	// LIKE wildcards in a name never match another table.
	if _, err := r.LoadTable(ctx, connections.TableRef{Catalog: catalog, Namespace: nss[0], Name: "int%"}); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("wildcard name: %v", err)
	}
}

func TestAuth(t *testing.T) {
	t.Parallel()
	host, port := server(t, true)
	p := provider(t)
	ctx := context.Background()
	try := func(values, secrets map[string]string) error {
		values["host"], values["port"], values["tls"] = host, port, "false"
		cfg, err := p.Validate(connections.Config{Values: values, Secrets: secrets})
		if err != nil {
			t.Fatal(err)
		}
		return p.Ping(ctx, cfg)
	}
	if err := try(map[string]string{"auth": "basic", "username": "alice"}, map[string]string{"password": "s3cret"}); err != nil {
		t.Errorf("basic: %v", err)
	}
	err := try(map[string]string{"auth": "basic", "username": "alice"}, map[string]string{"password": "wrong"})
	if err == nil || strings.Contains(err.Error(), "wrong") {
		t.Errorf("wrong password: %v", err)
	}
	if err := try(map[string]string{"auth": "token"}, map[string]string{"token": "static-token"}); err != nil {
		t.Errorf("token: %v", err)
	}
	if err := try(map[string]string{"auth": "none"}, nil); err == nil {
		t.Error("no credentials accepted")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	p := provider(t)
	for _, c := range []connections.Config{
		{Values: map[string]string{"host": "h", "port": "1", "auth": "basic"}},
		{Values: map[string]string{"host": "h", "port": "1", "auth": "token"}},
		{Values: map[string]string{"host": "h/x", "port": "1"}},
		{Values: map[string]string{"host": "h", "port": "0"}},
	} {
		if _, err := p.Validate(c); !errors.Is(err, connections.ErrValidation) {
			t.Errorf("%v accepted: %v", c.Values, err)
		}
	}
	cfg, err := p.Validate(connections.Config{Values: map[string]string{"host": "h", "port": "1", "username": "u"}, Secrets: map[string]string{"token": "t"}})
	if err != nil || cfg.Values["username"] != "" || cfg.Secrets["token"] != "" || cfg.Values["tls"] != "true" {
		t.Errorf("auth none keeps credentials or TLS is off: %+v, %v", cfg, err)
	}
}

// TestColumnTypes: string-like and binary-like columns are read whatever
// their width.
func TestColumnTypes(t *testing.T) {
	t.Parallel()
	mem := memory.NewGoAllocator()
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "a", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "b", Type: arrow.BinaryTypes.LargeString},
		{Name: "c", Type: arrow.BinaryTypes.StringView},
		{Name: "d", Type: arrow.BinaryTypes.LargeBinary},
	}, nil)
	bld := array.NewRecordBuilder(mem, schema)
	defer bld.Release()
	bld.Field(0).(*array.StringBuilder).AppendNull()
	bld.Field(1).(*array.LargeStringBuilder).Append("large")
	bld.Field(2).(*array.StringViewBuilder).Append("view")
	bld.Field(3).(*array.BinaryBuilder).Append([]byte{1, 2})
	rec := bld.NewRecordBatch()
	defer rec.Release()
	if got := []string{column(rec, "a")[0], column(rec, "b")[0], column(rec, "c")[0], column(rec, "missing")[0]}; got[0] != "" || got[1] != "large" || got[2] != "view" || got[3] != "" {
		t.Errorf("columns = %q", got)
	}
	if b, ok := rec.Column(3).(binaryColumn); !ok || b.Value(0)[1] != 2 {
		t.Error("large_binary is not read")
	}
}
