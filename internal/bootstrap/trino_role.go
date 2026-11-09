// Package bootstrap applies deploy-time state that must not live in a committed
// migration, such as secrets that come from the environment.
package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is the subset of a database handle this package needs.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// trinoRoleStatements recreate the baleia_trino role and its grants that the
// removed goose migrations owned. They belong to the Trino plugin's execution
// path, not to the GORM store schema (ADR 0020 keeps the execution path out of
// the store), so they are deploy-time bootstrap here.
var trinoRoleStatements = []string{
	`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'baleia_trino') THEN CREATE ROLE baleia_trino LOGIN; END IF; END $$`,
	`GRANT USAGE ON SCHEMA public TO baleia_trino`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON trino_catalog_registry TO baleia_trino`,
	`GRANT SELECT ON trino_clusters TO baleia_trino`,
	`GRANT SELECT ON resource_groups TO baleia_trino`,
	`GRANT SELECT ON selectors TO baleia_trino`,
	`GRANT SELECT ON resource_groups_global_properties TO baleia_trino`,
}

// EnsureTrinoRole creates the baleia_trino login role if it is missing and
// grants it the registry and resource-group reads the plugin needs. It is
// idempotent.
func EnsureTrinoRole(ctx context.Context, db Execer) error {
	for _, stmt := range trinoRoleStatements {
		if _, err := db.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("ensure baleia_trino role: %w", err)
		}
	}
	return nil
}

// EnsureTrinoRoleSQL is EnsureTrinoRole over database/sql, for the migration
// command and tests.
func EnsureTrinoRoleSQL(ctx context.Context, db *sql.DB) error {
	for _, stmt := range trinoRoleStatements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ensure baleia_trino role: %w", err)
		}
	}
	return nil
}

// SetTrinoRolePassword sets the baleia_trino login password from password, which
// callers read from BALEIA_TRINO_DB_PASSWORD — the same env var the
// baleia-trino-catalog-store plugin reads — so the role and the Trino connection
// cannot diverge. An empty password is a no-op, leaving the role untouched.
//
// The value is escaped here (quoteLiteral) because Postgres does not accept a
// bind parameter in ALTER ROLE ... PASSWORD. Operators who enable full statement
// logging should be aware the generated DDL carries the value.
func SetTrinoRolePassword(ctx context.Context, db Execer, password string) error {
	if password == "" {
		return nil
	}
	_, err := db.Exec(ctx, "ALTER ROLE baleia_trino WITH LOGIN PASSWORD "+quoteLiteral(password))
	return err
}

// quoteLiteral renders s as a single-quoted SQL string literal, doubling any
// embedded single quotes.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
