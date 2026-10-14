package activities

import (
	"context"
	"errors"
	"testing"

	"go.temporal.io/sdk/temporal"
)

func TestSplitSQL(t *testing.T) {
	a := &Activities{}
	res, err := a.splitSQL(context.Background(), SplitSQLParams{SQL: "SELECT 1; SELECT 'a;b'"})
	if err != nil || len(res.Statements) != 2 {
		t.Fatalf("%+v, %v", res, err)
	}
	for _, sql := range []string{"SELECT 1; DROP CATALOG x", "SELECT 'open"} {
		_, err := a.splitSQL(context.Background(), SplitSQLParams{SQL: sql})
		var appErr *temporal.ApplicationError
		if !errors.As(err, &appErr) || !appErr.NonRetryable() || appErr.Type() != "editor_rejected" {
			t.Errorf("%q: want a non-retryable editor_rejected error, got %v", sql, err)
		}
	}
}
