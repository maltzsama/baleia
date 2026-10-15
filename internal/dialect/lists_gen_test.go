package dialect_test

// TestGenerateLists rewrites internal/dialect/lists/<dialect>.*.txt from a
// real server: BALEIA_UPDATE_LISTS=<dialect> with that dialect's
// BALEIA_TEST_*_URL set (scripts/gen-dialect-lists.sh does both). Lists are
// committed with the server version; they are never fetched at runtime.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/dialect"
)

// listSource says how to read one dialect's lists; an empty query means the
// server has no catalog of them.
type listSource struct {
	open             func(t *testing.T) runner
	version          string
	keywords, funcs  string
	upperKeywords    bool
	noKeywordsReason string
	noFuncsReason    string
}

var wordRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

func listSources() map[string]listSource {
	mysqlLike := func(envName string) func(t *testing.T) runner {
		return func(t *testing.T) runner { return openSQL(t, "mysql", mysqlDSN(t, env(t, envName))) }
	}
	return map[string]listSource{
		dialect.PostgreSQL: {
			open:     func(t *testing.T) runner { return openSQL(t, "pgx", env(t, "BALEIA_TEST_POSTGRES_URL")) },
			version:  `SELECT 'PostgreSQL ' || current_setting('server_version')`,
			keywords: `SELECT word FROM pg_get_keywords()`,
			funcs: `SELECT DISTINCT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = 'pg_catalog' AND p.prokind IN ('f', 'a', 'w') AND p.prorettype <> 'internal'::regtype
  AND p.proname !~ '^(_|pg_)' AND p.proname !~ '(in|out|recv|send|support|handler)$'`,
			upperKeywords: true,
		},
		dialect.MySQL: {
			open:     mysqlLike("BALEIA_TEST_MYSQL_URL"),
			version:  `SELECT CONCAT('MySQL ', VERSION())`,
			keywords: `SELECT WORD FROM information_schema.KEYWORDS`,
			funcs: `SELECT t.name FROM mysql.help_topic t JOIN mysql.help_category c ON c.help_category_id = t.help_category_id
WHERE c.name LIKE '%Functions%' OR c.name LIKE '%Operators%'`,
			upperKeywords: true,
		},
		dialect.MariaDB: {
			open:          mysqlLike("BALEIA_TEST_MARIADB_URL"),
			version:       `SELECT CONCAT('MariaDB ', VERSION())`,
			keywords:      `SELECT WORD FROM information_schema.KEYWORDS`,
			funcs:         `SELECT FUNCTION FROM information_schema.SQL_FUNCTIONS`,
			upperKeywords: true,
		},
		dialect.TSQL: {
			open: func(t *testing.T) runner {
				return openSQL(t, "sqlserver", env(t, "BALEIA_TEST_SQLSERVER_URL")+"?encrypt=disable")
			},
			version:          `SELECT 'SQL Server ' + CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(64))`,
			noKeywordsReason: "SQL Server has no catalog of keywords",
			noFuncsReason:    "SQL Server has no catalog of built-in functions",
		},
		dialect.Oracle: {
			open: func(t *testing.T) runner {
				return openOracle(t, env(t, "BALEIA_TEST_ORACLE_URL")) // needs SELECT on V$ views (SYSTEM)
			},
			version:       `SELECT 'Oracle ' || version_full FROM product_component_version WHERE ROWNUM = 1`,
			keywords:      `SELECT keyword FROM v$reserved_words`,
			funcs:         `SELECT DISTINCT name FROM v$sqlfn_metadata`,
			upperKeywords: true,
		},
		dialect.SQLite: {
			open: func(t *testing.T) runner {
				db, err := sql.Open("sqlite", ":memory:")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Close() })
				return sqlRunner(db)
			},
			version:          `SELECT 'SQLite ' || sqlite_version()`,
			funcs:            `SELECT DISTINCT name FROM pragma_function_list`,
			noKeywordsReason: "SQLite exposes keywords only through its C API",
		},
		dialect.ClickHouse: {
			open:          func(t *testing.T) runner { return clickhouseRunner(t, env(t, "BALEIA_TEST_CLICKHOUSE_URL")) },
			version:       `SELECT concat('ClickHouse ', version())`,
			keywords:      `SELECT keyword FROM system.keywords`,
			funcs:         `SELECT name FROM system.functions`,
			upperKeywords: true,
		},
		dialect.Trino: {
			open:             func(t *testing.T) runner { return trinoRunner(env(t, "BALEIA_TEST_TRINO_URL")) },
			version:          `SELECT 'Trino ' || node_version FROM system.runtime.nodes WHERE coordinator`,
			funcs:            `SHOW FUNCTIONS`,
			noKeywordsReason: "Trino has no catalog of keywords",
		},
	}
}

func TestGenerateLists(t *testing.T) {
	name := os.Getenv("BALEIA_UPDATE_LISTS")
	if name == "" {
		t.Skip("BALEIA_UPDATE_LISTS not set")
	}
	d, ok := dialect.Get(name)
	if !ok {
		t.Fatalf("unknown dialect %q", name)
	}
	if name == dialect.FlightSQL {
		writeList(t, name, "keywords", "no server: a Flight SQL server can be anything", nil)
		writeList(t, name, "functions", "no server: a Flight SQL server can be anything", nil)
		return
	}
	src, ok := listSources()[name]
	if !ok {
		t.Fatalf("no list source for %s", name)
	}
	run := src.open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rows, err := run(ctx, src.version)
	if err != nil || len(rows) == 0 {
		t.Fatalf("version: %v", err)
	}
	version := strings.TrimSpace(text(rows[0][0]))
	read := func(q string, upper bool) []string {
		rows, err := run(ctx, q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		var out []string
		for _, r := range rows {
			w := strings.TrimSpace(text(r[0]))
			if !wordRe.MatchString(w) {
				continue // operators, multi-word topics
			}
			if upper {
				w = strings.ToUpper(w)
			}
			out = append(out, w)
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	for _, l := range []struct {
		kind, query, none string
		upper             bool
	}{
		{"keywords", src.keywords, src.noKeywordsReason, src.upperKeywords},
		{"functions", src.funcs, src.noFuncsReason, false},
	} {
		if l.query == "" {
			writeList(t, name, l.kind, version+": "+l.none+"; the editor keeps "+d.CodeMirrorBase()+"'s", nil)
			continue
		}
		words := read(l.query, l.upper)
		if len(words) == 0 {
			t.Fatalf("%s: empty %s", name, l.kind)
		}
		writeList(t, name, l.kind, "from "+version, words)
	}
}

func writeList(t *testing.T, name, kind, origin string, words []string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s %s, %s.\n# Generated by scripts/gen-dialect-lists.sh %s; do not edit.\n", name, kind, origin, name)
	for _, w := range words {
		b.WriteString(w + "\n")
	}
	path := filepath.Join("lists", name+"."+kind+".txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d words", path, len(words))
}
