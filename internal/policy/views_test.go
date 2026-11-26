package policy

import (
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
)

func TestInvokerViews(t *testing.T) {
	t.Parallel()
	trino := dialect.NewRegistry().MustGet(dialect.Trino)
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{
			name: "plain view",
			sql:  "CREATE VIEW v AS SELECT 1",
			want: "CREATE VIEW v SECURITY INVOKER AS SELECT 1",
		},
		{
			name: "or replace, qualified name, lowercase",
			sql:  "create or replace view iceberg.sales.v as select * from t",
			want: "create or replace view iceberg.sales.v SECURITY INVOKER as select * from t",
		},
		{
			name: "after the comment",
			sql:  "CREATE VIEW v COMMENT 'a view AS such' AS SELECT 1",
			want: "CREATE VIEW v COMMENT 'a view AS such' SECURITY INVOKER AS SELECT 1",
		},
		{
			name: "before the properties",
			sql:  "CREATE VIEW v WITH (security = 'x') AS WITH c AS (SELECT 1) SELECT * FROM c",
			want: "CREATE VIEW v SECURITY INVOKER WITH (security = 'x') AS WITH c AS (SELECT 1) SELECT * FROM c",
		},
		{
			name: "quoted names, a leading comment and no space before AS",
			sql:  "-- note\nCREATE VIEW \"My Cat\".s.\"v x\"AS SELECT 'ação'",
			want: "-- note\nCREATE VIEW \"My Cat\".s.\"v x\" SECURITY INVOKER AS SELECT 'ação'",
		},
		{
			name: "a view named security",
			sql:  "CREATE VIEW security AS SELECT 1",
			want: "CREATE VIEW security SECURITY INVOKER AS SELECT 1",
		},
		{
			name: "if not exists and a view named security",
			sql:  "CREATE VIEW IF NOT EXISTS s.security AS SELECT 1",
			want: "CREATE VIEW IF NOT EXISTS s.security SECURITY INVOKER AS SELECT 1",
		},
		{
			name: "a comment that says security",
			sql:  "CREATE VIEW v COMMENT 'security' AS SELECT 1",
			want: "CREATE VIEW v COMMENT 'security' SECURITY INVOKER AS SELECT 1",
		},
		{
			name: "explicit definer is kept",
			sql:  "CREATE VIEW v SECURITY DEFINER AS SELECT 1",
			want: "CREATE VIEW v SECURITY DEFINER AS SELECT 1",
		},
		{
			name: "explicit invoker is kept",
			sql:  "CREATE VIEW v security invoker AS SELECT 1",
			want: "CREATE VIEW v security invoker AS SELECT 1",
		},
		{
			name: "materialized view",
			sql:  "CREATE MATERIALIZED VIEW v AS SELECT 1",
			want: "CREATE MATERIALIZED VIEW v AS SELECT 1",
		},
		{
			name: "table as select",
			sql:  "CREATE TABLE t AS SELECT 1",
			want: "CREATE TABLE t AS SELECT 1",
		},
		{
			name: "query",
			sql:  "SELECT 1 AS view",
			want: "SELECT 1 AS view",
		},
		{
			name: "incomplete statement",
			sql:  "CREATE VIEW v",
			want: "CREATE VIEW v",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := InvokerViews(trino, tt.sql); got != tt.want {
				t.Errorf("InvokerViews(%q) = %q, want %q", tt.sql, got, tt.want)
			}
		})
	}
}

// TestInvokerViews_OtherDialects: a view's security mode is Trino's; other
// engines' views are left as written.
func TestInvokerViews_OtherDialects(t *testing.T) {
	t.Parallel()
	registry := dialect.NewRegistry()
	for _, name := range []string{dialect.PostgreSQL, dialect.MySQL, dialect.TSQL} {
		sql := "CREATE VIEW v AS SELECT 1"
		if got := InvokerViews(registry.MustGet(name), sql); got != sql {
			t.Errorf("%s: InvokerViews(%q) = %q, want it unchanged", name, sql, got)
		}
	}
}
