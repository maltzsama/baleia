// Package bootstrap applies deploy-time state that must not live in a committed
// migration, such as secrets that come from the environment.
package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// execer is the subset of a database handle this package needs.
type execer interface {
	Exec(ctx context.Context, query string, args ...any) error
}

// storeExecer adapts the GORM store to execer.
type storeExecer struct{ db *gorm.DB }

func (e storeExecer) Exec(ctx context.Context, query string, args ...any) error {
	return e.db.WithContext(ctx).Exec(query, args...).Error
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
func EnsureTrinoRole(ctx context.Context, db *gorm.DB) error {
	return ensureTrinoRole(ctx, storeExecer{db: db})
}

func ensureTrinoRole(ctx context.Context, db execer) error {
	for _, stmt := range trinoRoleStatements {
		if err := db.Exec(ctx, stmt); err != nil {
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
func SetTrinoRolePassword(ctx context.Context, db *gorm.DB, password string) error {
	return setTrinoRolePassword(ctx, storeExecer{db: db}, password)
}

func setTrinoRolePassword(ctx context.Context, db execer, password string) error {
	if password == "" {
		return nil
	}
	return db.Exec(ctx, "ALTER ROLE baleia_trino WITH LOGIN PASSWORD "+quoteLiteral(password))
}

// quoteLiteral renders s as a single-quoted SQL string literal, doubling any
// embedded single quotes.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
