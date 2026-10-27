package postgresql

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// requirePostgres returns BALEIA_TEST_POSTGRES_URL, skipping when it is unset.
func requirePostgres(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("BALEIA_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("BALEIA_TEST_POSTGRES_URL not set")
	}
	return raw
}

// testEngine builds an engine against BALEIA_TEST_POSTGRES_URL, skipping when
// it is unset.
func testEngine(t *testing.T) queryengine.Engine {
	t.Helper()
	raw := requirePostgres(t)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	d, err := connections.NewDialer(connections.DialerOptions{AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(dialect.NewRegistry().MustGet(dialect.PostgreSQL))(context.Background(), queryengine.EngineRow{ID: "t", Kind: Kind}, dsn(raw, u), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

// TestReadOnlyBlocksWrites: a read-only session refuses DML, DDL and temp-table
// DDL. The connection's user may write; the wrapping read-only transaction
// stops it.
func TestReadOnlyBlocksWrites(t *testing.T) {
	t.Parallel()
	setup(t, requirePostgres(t))
	e := testEngine(t)
	s, err := e.OpenSession(context.Background(), queryengine.SessionSpec{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, sql := range []string{
		"INSERT INTO public.baleia_conformance_tx (n) VALUES (1)",
		"CREATE TABLE public.baleia_readonly (a integer)",
		"CREATE TEMP TABLE baleia_readonly_temp (a integer)",
	} {
		err := s.Execute(context.Background(), sql, queryengine.Callbacks{}, &sink{})
		if got := queryengine.CodeOf(err); got != queryengine.CodePermission {
			t.Errorf("%q: code %q (%v), want permission_denied", sql, got, err)
		}
	}
}

// TestSingleStatementProtocol: the extended protocol rejects a multi-statement
// string, so a splitter mistake cannot run a second statement.
func TestSingleStatementProtocol(t *testing.T) {
	t.Parallel()
	e := testEngine(t)
	s, err := e.OpenSession(context.Background(), queryengine.SessionSpec{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Execute(context.Background(), "SELECT 1; SELECT 2", queryengine.Callbacks{}, &sink{}); err == nil {
		t.Fatal("a multi-statement string was accepted")
	}
}

// sink discards every record.
type sink struct{}

func (*sink) Begin(*arrow.Schema) error       { return nil }
func (*sink) Write(r arrow.RecordBatch) error { r.Release(); return nil }
func (*sink) End(queryengine.Stats) error     { return nil }
