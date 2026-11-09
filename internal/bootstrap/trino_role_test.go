package bootstrap

import (
	"context"
	"strings"
	"testing"
)

type fakeExecer struct {
	gotSQL string
	calls  int
}

func (f *fakeExecer) Exec(_ context.Context, query string, _ ...any) error {
	f.gotSQL = query
	f.calls++
	return nil
}

func TestSetTrinoRolePasswordQuotesLiteral(t *testing.T) {
	t.Parallel()
	f := &fakeExecer{}
	if err := setTrinoRolePassword(context.Background(), f, "p'wd"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(f.gotSQL, "ALTER ROLE baleia_trino WITH LOGIN PASSWORD 'p''wd'") {
		t.Fatalf("password not safely quoted: %s", f.gotSQL)
	}
}

func TestSetTrinoRolePasswordNoopWhenEmpty(t *testing.T) {
	t.Parallel()
	f := &fakeExecer{}
	if err := setTrinoRolePassword(context.Background(), f, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.calls != 0 {
		t.Fatalf("expected no SQL for an empty password, ran %d statement(s)", f.calls)
	}
}
