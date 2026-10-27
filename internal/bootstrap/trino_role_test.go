package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type fakeExecer struct {
	gotSQL string
	calls  int
}

func (f *fakeExecer) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.gotSQL = sql
	f.calls++
	return pgconn.CommandTag{}, nil
}

func TestSetTrinoRolePasswordQuotesLiteral(t *testing.T) {
	t.Parallel()
	f := &fakeExecer{}
	if err := SetTrinoRolePassword(context.Background(), f, "p'wd"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(f.gotSQL, "ALTER ROLE baleia_trino WITH LOGIN PASSWORD 'p''wd'") {
		t.Fatalf("password not safely quoted: %s", f.gotSQL)
	}
}

func TestSetTrinoRolePasswordNoopWhenEmpty(t *testing.T) {
	t.Parallel()
	f := &fakeExecer{}
	if err := SetTrinoRolePassword(context.Background(), f, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.calls != 0 {
		t.Fatalf("expected no SQL for an empty password, ran %d statement(s)", f.calls)
	}
}
