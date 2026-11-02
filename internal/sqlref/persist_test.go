package sqlref

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestPersistIsIdempotent: re-running Persist replaces the statement's rows
// instead of duplicating them (#538).
func TestPersistIsIdempotent(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)

	var userID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (name, email, password_hash) VALUES ('u','u@x','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var wsID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w','personal','p',$1) RETURNING id`, userID).Scan(&wsID); err != nil {
		t.Fatal(err)
	}
	var execID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO query_executions (workspace_id, user_id, status, sql) VALUES ($1,$2,'finished','SELECT 1') RETURNING id`, wsID, userID).Scan(&execID); err != nil {
		t.Fatal(err)
	}
	const statement = `SELECT * FROM orders o JOIN customers c ON o.customer_id = c.id`
	var stmtID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO query_statements (execution_id, position, sql, status) VALUES ($1,1,$2,'finished') RETURNING id`, execID, statement).Scan(&stmtID); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := Persist(ctx, q, stmtID, wsID, pgtype.UUID{}, "sales", statement); err != nil {
			t.Fatalf("Persist %d: %v", i, err)
		}
	}
	var refs, joins int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM statement_references WHERE statement_id=$1`, stmtID).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM statement_joins WHERE statement_id=$1`, stmtID).Scan(&joins); err != nil {
		t.Fatal(err)
	}
	if refs != 2 || joins != 1 {
		t.Fatalf("refs=%d joins=%d, want 2 and 1", refs, joins)
	}
}

// TestPersistUnparsableWritesNothing: a statement the extractor cannot read
// stores no rows, and an old row is cleared.
func TestPersistUnparsableWritesNothing(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)

	var userID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (name, email, password_hash) VALUES ('u','u@x','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var wsID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w','personal','p',$1) RETURNING id`, userID).Scan(&wsID); err != nil {
		t.Fatal(err)
	}
	var execID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO query_executions (workspace_id, user_id, status, sql) VALUES ($1,$2,'finished','SELECT 1') RETURNING id`, wsID, userID).Scan(&execID); err != nil {
		t.Fatal(err)
	}
	var stmtID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO query_statements (execution_id, position, sql, status) VALUES ($1,1,'SELECT 1','finished') RETURNING id`, execID).Scan(&stmtID); err != nil {
		t.Fatal(err)
	}

	if err := Persist(ctx, q, stmtID, wsID, pgtype.UUID{}, "sales", "SELECT * FROM orders"); err != nil {
		t.Fatal(err)
	}
	if err := Persist(ctx, q, stmtID, wsID, pgtype.UUID{}, "sales", "SELECT FROM ((("); err != nil {
		t.Fatal(err)
	}
	var refs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM statement_references WHERE statement_id=$1`, stmtID).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if refs != 0 {
		t.Fatalf("refs=%d, want 0", refs)
	}
}
