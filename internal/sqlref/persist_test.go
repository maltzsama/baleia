package sqlref

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// seedStatement stores a finished execution with one statement through the
// GORM store, as production does.
func seedStatement(t *testing.T, statement string) (*repo.Repo, *gorm.DB, db.UUID, db.UUID) {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	gdb := pool
	q := repo.New(gdb)

	uid := uuid.New()
	userID := db.UUID{Bytes: uid, Valid: true}
	password := "x"
	if err := gdb.Create(&model.User{
		ID: model.UUID(uid.String()), Name: "u", Email: "u@x", PasswordHash: &password,
	}).Error; err != nil {
		t.Fatal(err)
	}
	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "w", Type: "personal", PolarisCatalogName: "p", OwnerUserID: userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	exec, err := q.CreateQueryExecution(ctx, db.CreateQueryExecutionParams{
		WorkspaceID: ws.ID, UserID: userID, Status: "finished", Sql: "SELECT 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := q.CreateQueryStatement(ctx, db.CreateQueryStatementParams{
		ExecutionID: exec.ID, Position: 1, Sql: statement, Status: "finished",
	})
	if err != nil {
		t.Fatal(err)
	}
	return q, gdb, ws.ID, stmt.ID
}

// TestPersistIsIdempotent: re-running Persist replaces the statement's rows
// instead of duplicating them (#538).
func TestPersistIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const statement = `SELECT * FROM orders o JOIN customers c ON o.customer_id = c.id`
	q, gdb, wsID, stmtID := seedStatement(t, statement)

	for i := 0; i < 2; i++ {
		if err := Persist(ctx, q, stmtID, wsID, db.UUID{}, "sales", statement); err != nil {
			t.Fatalf("Persist %d: %v", i, err)
		}
	}
	var refs, joins int64
	if err := gdb.Model(&model.StatementReference{}).Where("statement_id = ?", stmtID).Count(&refs).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.StatementJoin{}).Where("statement_id = ?", stmtID).Count(&joins).Error; err != nil {
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
	ctx := context.Background()
	q, gdb, wsID, stmtID := seedStatement(t, "SELECT 1")

	if err := Persist(ctx, q, stmtID, wsID, db.UUID{}, "sales", "SELECT * FROM orders"); err != nil {
		t.Fatal(err)
	}
	if err := Persist(ctx, q, stmtID, wsID, db.UUID{}, "sales", "SELECT FROM ((("); err != nil {
		t.Fatal(err)
	}
	var refs int64
	if err := gdb.Model(&model.StatementReference{}).Where("statement_id = ?", stmtID).Count(&refs).Error; err != nil {
		t.Fatal(err)
	}
	if refs != 0 {
		t.Fatalf("refs=%d, want 0", refs)
	}
}
