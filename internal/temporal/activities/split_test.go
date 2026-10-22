package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
	"go.temporal.io/sdk/temporal"
)

func TestSplitSQL(t *testing.T) {
	a := &Activities{dialects: dialect.NewRegistry()}
	res, err := a.splitSQL(context.Background(), SplitSQLParams{SQL: "SELECT 1; SELECT 'a;b'", Dialect: "trino"})
	if err != nil || len(res.Statements) != 2 {
		t.Fatalf("%+v, %v", res, err)
	}
	for _, sql := range []string{"SELECT 1; DROP CATALOG x", "SELECT 'open"} {
		_, err := a.splitSQL(context.Background(), SplitSQLParams{SQL: sql, Dialect: "trino"})
		var appErr *temporal.ApplicationError
		if !errors.As(err, &appErr) || !appErr.NonRetryable() || appErr.Type() != "editor_rejected" {
			t.Errorf("%q: want a non-retryable editor_rejected error, got %v", sql, err)
		}
	}
}

// TestSplitSQL_Dialect: the script is split with the target engine's dialect.
func TestSplitSQL_Dialect(t *testing.T) {
	a := &Activities{dialects: dialect.NewRegistry()}
	res, err := a.splitSQL(context.Background(), SplitSQLParams{SQL: "SELECT 1\nGO\nSELECT 2", Dialect: "tsql"})
	if err != nil || len(res.Statements) != 2 {
		t.Fatalf("tsql GO: %+v, %v", res, err)
	}
	if _, err := a.splitSQL(context.Background(), SplitSQLParams{SQL: "SELECT 1\nGO\nDROP CATALOG x", Dialect: "tsql"}); err == nil {
		t.Error("catalog DDL after GO accepted")
	}
	if _, err := a.splitSQL(context.Background(), SplitSQLParams{SQL: "SELECT 1", Dialect: "cobol"}); err == nil {
		t.Error("unknown dialect accepted")
	}
}
