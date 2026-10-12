// Package bootstrap applies deploy-time state that must not live in a committed
// migration, such as secrets that come from the environment.
package bootstrap

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is the subset of a database handle this package needs.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
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
