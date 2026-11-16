package bigateway

import (
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
)

func TestReadOnly(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	allowed := []string{
		"SELECT 1",
		"  select * from lake.sales.orders",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"SHOW CATALOGS",
		"SHOW CREATE TABLE lake.sales.orders",
		"DESCRIBE lake.sales.orders",
		"DESC lake.sales.orders",
		"TABLE lake.sales.orders",
		"VALUES (1), (2)",
		"-- a comment\nSELECT 1",
		"/* a comment */ SELECT 1",
		"SELECT 1;",
	}
	for _, sql := range allowed {
		if err := ReadOnly(d, sql); err != nil {
			t.Errorf("ReadOnly(%q) = %v, want nil", sql, err)
		}
	}
	rejected := []string{
		"",
		"   ",
		"-- only a comment",
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET x=1",
		"DELETE FROM t",
		"MERGE INTO t USING s ON 1=1 WHEN MATCHED THEN DELETE",
		"CREATE TABLE t (x int)",
		"DROP TABLE t",
		"ALTER TABLE t ADD COLUMN y int",
		"TRUNCATE TABLE t",
		"SET SESSION x=1",
		"USE lake.sales",
		"EXPLAIN SELECT 1",
		"EXPLAIN ANALYZE SELECT 1",
		"CALL foo()",
		"PREPARE p FROM SELECT 1",
		"EXECUTE p",
		"SELECT 1; DROP TABLE t",
	}
	for _, sql := range rejected {
		if err := ReadOnly(d, sql); !errors.Is(err, ErrReadOnly) {
			t.Errorf("ReadOnly(%q) = %v, want ErrReadOnly", sql, err)
		}
	}
}

// TestReadOnlySetOperations: a set-operation root is query-shaped and stays
// read-only, whatever the parser's root kind.
func TestReadOnlySetOperations(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.Trino)
	for _, sql := range []string{
		"SELECT 1 UNION ALL SELECT 2",
		"SELECT 1 INTERSECT SELECT 2",
		"SELECT 1 EXCEPT SELECT 2",
	} {
		if err := ReadOnly(d, sql); err != nil {
			t.Errorf("ReadOnly(%q) = %v, want nil", sql, err)
		}
	}
}

// TestReadOnlyExplainPostgres: under PostgreSQL EXPLAIN parses as a Describe,
// but EXPLAIN ANALYZE executes the explained statement, so it is rejected.
func TestReadOnlyExplainPostgres(t *testing.T) {
	t.Parallel()
	d := dialect.NewRegistry().MustGet(dialect.PostgreSQL)
	for _, sql := range []string{
		"EXPLAIN SELECT 1",
		"EXPLAIN ANALYZE SELECT 1",
		"EXPLAIN ANALYZE DELETE FROM t",
	} {
		if err := ReadOnly(d, sql); !errors.Is(err, ErrReadOnly) {
			t.Errorf("ReadOnly(%q) = %v, want ErrReadOnly", sql, err)
		}
	}
	if err := ReadOnly(d, "DESCRIBE t"); err != nil {
		t.Errorf("ReadOnly(DESCRIBE t) = %v, want nil", err)
	}
}
