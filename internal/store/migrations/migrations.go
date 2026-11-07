// Package migrations holds Baleia's store schema as Bun Go migrations
// (docs/adr/0020). Each file registers one version with the same numeric id as
// the migration it replaces, and emits the DDL for the dialect Bun is running
// on.
package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
	"github.com/uptrace/bun/migrate"
)

// Migrations is the ordered set Bun applies and rolls back.
var Migrations = migrate.NewMigrations()

var ifExistsRe = regexp.MustCompile(`(?i)\bIF\s+(?:NOT\s+)?EXISTS\s+`)

// dialectDDL runs the statement for the dialect Bun is connected to.
func dialectDDL(ctx context.Context, db *bun.DB, postgres, mysql string) error {
	switch db.Dialect().Name() {
	case dialect.PG:
		if strings.TrimSpace(postgres) == "" {
			return nil
		}
		_, err := db.ExecContext(ctx, postgres)
		return err
	case dialect.MySQL:
		return mysqlExec(ctx, db, mysql)
	default:
		return fmt.Errorf("migrations: unsupported dialect %s", db.Dialect().Name())
	}
}

// mysqlExec runs the MySQL DDL one statement at a time and makes the
// IF [NOT] EXISTS forms idempotent through information_schema, so the same
// migrations run on MySQL 8 (which rejects those clauses) and MariaDB.
func mysqlExec(ctx context.Context, db *bun.DB, ddl string) error {
	// Go raw strings cannot hold a backtick; the MySQL DDL writes {{bt}} where
	// an identifier needs quoting.
	ddl = strings.ReplaceAll(ddl, "{{bt}}", "`")
	for _, stmt := range splitStatements(ddl) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := mysqlStatement(ctx, db, stmt); err != nil {
			return fmt.Errorf("%s: %w", firstLine(stmt), err)
		}
	}
	return nil
}

func mysqlStatement(ctx context.Context, db *bun.DB, stmt string) error {
	upper := strings.ToUpper(stmt)
	if strings.HasPrefix(upper, "ALTER TABLE ") {
		table, actions := splitAlter(stmt)
		if table != "" && actions != "" {
			for _, action := range splitTopLevel(actions) {
				if err := mysqlAction(ctx, db, table, action); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if strings.HasPrefix(upper, "CREATE INDEX ") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX ") {
		return mysqlCreateIndex(ctx, db, stmt)
	}
	if strings.HasPrefix(upper, "DROP INDEX ") {
		return mysqlDropIndex(ctx, db, stmt)
	}
	_, err := db.ExecContext(ctx, stmt)
	return err
}

func mysqlAction(ctx context.Context, db *bun.DB, table, action string) error {
	a := strings.TrimSpace(action)
	upper := strings.ToUpper(a)
	switch {
	case strings.HasPrefix(upper, "ADD COLUMN "):
		rest := strings.TrimSpace(a[len("ADD COLUMN "):])
		rest = strings.TrimSpace(ifExistsRe.ReplaceAllString(rest, ""))
		col := firstIdent(rest)
		exists, err := mysqlHasColumn(ctx, db, table, col)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		_, err = db.ExecContext(ctx, "ALTER TABLE "+quoteIdent(table)+" ADD COLUMN "+rest)
		return err
	case strings.HasPrefix(upper, "DROP COLUMN "):
		rest := strings.TrimSpace(a[len("DROP COLUMN "):])
		rest = strings.TrimSpace(ifExistsRe.ReplaceAllString(rest, ""))
		col := firstIdent(rest)
		exists, err := mysqlHasColumn(ctx, db, table, col)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if err := mysqlDropColumnFKs(ctx, db, table, col); err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, "ALTER TABLE "+quoteIdent(table)+" DROP COLUMN "+quoteIdent(col))
		return err
	case strings.HasPrefix(upper, "ADD CONSTRAINT "):
		rest := strings.TrimSpace(a[len("ADD CONSTRAINT "):])
		name := firstIdent(rest)
		exists, err := mysqlHasConstraint(ctx, db, table, name)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		_, err = db.ExecContext(ctx, "ALTER TABLE "+quoteIdent(table)+" "+a)
		return err
	case strings.HasPrefix(upper, "DROP CONSTRAINT "):
		rest := strings.TrimSpace(a[len("DROP CONSTRAINT "):])
		rest = strings.TrimSpace(ifExistsRe.ReplaceAllString(rest, ""))
		name := firstIdent(rest)
		exists, err := mysqlHasConstraint(ctx, db, table, name)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		return mysqlDropConstraint(ctx, db, table, name)
	default:
		_, err := db.ExecContext(ctx, "ALTER TABLE "+quoteIdent(table)+" "+a)
		return err
	}
}

func mysqlCreateIndex(ctx context.Context, db *bun.DB, stmt string) error {
	rest := stmt
	upper := strings.ToUpper(stmt)
	if strings.HasPrefix(upper, "CREATE UNIQUE INDEX ") {
		rest = stmt[len("CREATE UNIQUE INDEX "):]
	} else {
		rest = stmt[len("CREATE INDEX "):]
	}
	rest = strings.TrimSpace(ifExistsRe.ReplaceAllString(rest, ""))
	name := firstIdent(rest)
	after := strings.TrimSpace(rest[len(name):])
	table := ""
	if strings.HasPrefix(strings.ToUpper(after), "ON ") {
		table = firstIdent(strings.TrimSpace(after[3:]))
	}
	exists, err := mysqlHasIndex(ctx, db, table, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.ExecContext(ctx, ifExistsRe.ReplaceAllString(stmt, ""))
	return err
}

func mysqlDropIndex(ctx context.Context, db *bun.DB, stmt string) error {
	rest := strings.TrimSpace(ifExistsRe.ReplaceAllString(stmt[len("DROP INDEX "):], ""))
	name := firstIdent(rest)
	table := ""
	if i := strings.Index(strings.ToUpper(rest), " ON "); i >= 0 {
		table = firstIdent(strings.TrimSpace(rest[i+4:]))
	}
	if table == "" {
		err := db.QueryRowContext(ctx,
			`SELECT table_name FROM information_schema.statistics WHERE table_schema = DATABASE() AND index_name = ?`,
			name).Scan(&table)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	cols, err := mysqlIndexColumns(ctx, db, table, name)
	if err != nil {
		return err
	}
	for _, col := range cols {
		if err := mysqlDropColumnFKs(ctx, db, table, col); err != nil {
			return err
		}
	}
	_, err = db.ExecContext(ctx, "ALTER TABLE "+quoteIdent(table)+" DROP INDEX "+quoteIdent(name))
	return err
}

func mysqlHasColumn(ctx context.Context, db *bun.DB, table, column string) (bool, error) {
	return mysqlExists(ctx, db,
		`SELECT count(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`,
		table, column)
}

func mysqlHasConstraint(ctx context.Context, db *bun.DB, table, name string) (bool, error) {
	return mysqlExists(ctx, db,
		`SELECT count(*) FROM information_schema.table_constraints WHERE constraint_schema = DATABASE() AND table_name = ? AND constraint_name = ?`,
		table, name)
}

func mysqlHasIndex(ctx context.Context, db *bun.DB, table, name string) (bool, error) {
	return mysqlExists(ctx, db,
		`SELECT count(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`,
		table, name)
}

// mysqlDropColumnFKs drops the foreign keys that use a column (on this table)
// and the foreign keys of other tables that reference it, before the column or
// its index is dropped.
func mysqlDropColumnFKs(ctx context.Context, db *bun.DB, table, column string) error {
	type fk struct{ table, name string }
	var fks []fk
	queries := []struct {
		query string
		args  []any
	}{
		{`SELECT table_name, constraint_name FROM information_schema.key_column_usage WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ? AND referenced_table_name IS NOT NULL`, []any{table, column}},
		{`SELECT table_name, constraint_name FROM information_schema.key_column_usage WHERE table_schema = DATABASE() AND referenced_table_name = ? AND referenced_column_name = ?`, []any{table, column}},
	}
	for _, q := range queries {
		rows, err := db.QueryContext(ctx, q.query, q.args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var f fk
			if err := rows.Scan(&f.table, &f.name); err != nil {
				_ = rows.Close()
				return err
			}
			fks = append(fks, f)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
	}
	for _, f := range fks {
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+quoteIdent(f.table)+" DROP FOREIGN KEY "+quoteIdent(f.name)); err != nil {
			return err
		}
	}
	return nil
}

func mysqlIndexColumns(ctx context.Context, db *bun.DB, table, name string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT column_name FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ? ORDER BY seq_in_index`,
		table, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

// mysqlDropConstraint drops a unique/check/foreign constraint, first dropping
// the foreign keys that depend on its index.
func mysqlDropConstraint(ctx context.Context, db *bun.DB, table, name string) error {
	var ctype string
	err := db.QueryRowContext(ctx,
		`SELECT constraint_type FROM information_schema.table_constraints WHERE constraint_schema = DATABASE() AND table_name = ? AND constraint_name = ?`,
		table, name).Scan(&ctype)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.EqualFold(ctype, "FOREIGN KEY") {
		_, err = db.ExecContext(ctx, "ALTER TABLE "+quoteIdent(table)+" DROP FOREIGN KEY "+quoteIdent(name))
		return err
	}
	cols, err := mysqlIndexColumns(ctx, db, table, name)
	if err != nil {
		return err
	}
	for _, col := range cols {
		if err := mysqlDropColumnFKs(ctx, db, table, col); err != nil {
			return err
		}
	}
	_, err = db.ExecContext(ctx, "ALTER TABLE "+quoteIdent(table)+" DROP CONSTRAINT "+quoteIdent(name))
	return err
}

func mysqlExists(ctx context.Context, db *bun.DB, query string, args ...any) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// splitAlter splits `ALTER TABLE <table> <actions>` into its table and the
// action list.
func splitAlter(stmt string) (string, string) {
	rest := strings.TrimSpace(stmt[len("ALTER TABLE "):])
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "ONLY "))
	if strings.HasPrefix(rest, "`") {
		if end := strings.Index(rest[1:], "`"); end >= 0 {
			return rest[1 : end+1], strings.TrimSpace(rest[end+2:])
		}
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", ""
	}
	table := fields[0]
	return table, strings.TrimSpace(rest[len(table):])
}

// splitStatements splits on `;` outside quotes.
func splitStatements(sql string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote {
				if i+1 < len(sql) && sql[i+1] == quote {
					cur.WriteByte(sql[i+1])
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
			cur.WriteByte(c)
		case ';':
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// splitTopLevel splits on commas outside parentheses and quotes.
func splitTopLevel(s string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote {
				if i+1 < len(s) && s[i+1] == quote {
					cur.WriteByte(s[i+1])
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
			cur.WriteByte(c)
		case '(':
			depth++
			cur.WriteByte(c)
		case ')':
			depth--
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(cur.String()))
				cur.Reset()
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func firstIdent(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "`") {
		if end := strings.Index(s[1:], "`"); end >= 0 {
			return s[1 : end+1]
		}
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "`")
}

func quoteIdent(name string) string {
	return "`" + strings.Trim(name, "`") + "`"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
