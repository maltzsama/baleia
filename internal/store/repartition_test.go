package store

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// newPostgresStore opens a throwaway PostgreSQL database with no schema.
func newPostgresStore(t *testing.T) *gorm.DB {
	t.Helper()
	adminURL := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	if b, err := BackendFromDSN(adminURL); err != nil || b != Postgres {
		t.Skip("PostgreSQL test database required")
	}
	ctx := context.Background()
	admin, err := Open(ctx, adminURL, Postgres, Options{MaxOpenConns: 1, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close(admin) })
	name := "baleia_repart_" + strings.ToLower(rand.Text()[:12])
	if err := admin.WithContext(ctx).Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)").Error })
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := Open(ctx, u.String(), Postgres, Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = Close(db) })
	return db
}

// toOldShape rewinds the query-history tables to the schema deployments have
// before migration 14: single-column keys, no execution_started_at, and the
// four single-column foreign keys.
func toOldShape(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := dropHistoryForeignKeys(db); err != nil {
		t.Fatal(err)
	}
	for _, c := range monthColumns() {
		if err := db.Migrator().DropColumn(c.shim, "ExecutionStartedAt"); err != nil {
			t.Fatalf("drop column: %v", err)
		}
	}
	for _, stmt := range []string{
		`ALTER TABLE audit_events DROP CONSTRAINT audit_events_pkey, ADD PRIMARY KEY (id)`,
		`ALTER TABLE query_executions DROP CONSTRAINT query_executions_pkey, ADD PRIMARY KEY (id)`,
		`ALTER TABLE query_statements ADD PRIMARY KEY (id)`,
		`ALTER TABLE query_statements ADD CONSTRAINT fk_query_statements_execution FOREIGN KEY (execution_id) REFERENCES query_executions (id) ON DELETE CASCADE`,
		`ALTER TABLE statement_references ADD CONSTRAINT fk_statement_references_statement FOREIGN KEY (statement_id) REFERENCES query_statements (id) ON DELETE CASCADE`,
		`ALTER TABLE statement_joins ADD CONSTRAINT fk_statement_joins_statement FOREIGN KEY (statement_id) REFERENCES query_statements (id) ON DELETE CASCADE`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// TestRepartitionQueryHistoryKeepsData upgrades a populated store from the
// pre-partition schema: rows and audit ids survive, the denormalized month is
// backfilled from the parent, the tables are partitioned and the composite
// foreign keys hold.
func TestRepartitionQueryHistoryKeepsData(t *testing.T) {
	t.Parallel()
	db := newPostgresStore(t)
	if err := AutoMigrate(db, Models()...); err != nil {
		t.Fatalf("schema: %v", err)
	}
	toOldShape(t, db)

	user := model.User{ID: "00000000-0000-4000-8000-000000000001", Name: "u", Email: "u@example.test", PasswordHash: new("x")}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ws := model.Workspace{ID: "00000000-0000-4000-8000-000000000002", Name: "w", Type: "personal", PolarisCatalogName: "w", OwnerUserID: &user.ID}
	if err := db.Create(&ws).Error; err != nil {
		t.Fatal(err)
	}
	months := []time.Time{
		time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 20, 11, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC),
	}
	execIDs := []string{
		"10000000-0000-4000-8000-000000000001",
		"10000000-0000-4000-8000-000000000002",
		"10000000-0000-4000-8000-000000000003",
	}
	stmtIDs := []string{
		"20000000-0000-4000-8000-000000000001",
		"20000000-0000-4000-8000-000000000002",
		"20000000-0000-4000-8000-000000000003",
	}
	for i, at := range months {
		mustInsert(t, db, "query_executions", map[string]any{
			"id": execIDs[i], "workspace_id": string(ws.ID), "user_id": string(user.ID),
			"status": "finished", "sql": "select 1", "started_at": at,
		})
		mustInsert(t, db, "query_statements", map[string]any{
			"id": stmtIDs[i], "execution_id": execIDs[i], "position": 1, "sql": "select 1", "status": "finished",
		})
		mustInsert(t, db, "statement_references", map[string]any{
			"statement_id": stmtIDs[i], "workspace_id": string(ws.ID), "namespace": "n", "table_name": "t", "alias": "a", "kind": "read",
		})
		mustInsert(t, db, "statement_joins", map[string]any{
			"statement_id": stmtIDs[i], "workspace_id": string(ws.ID), "left_alias": "a", "left_column": "x", "right_alias": "b", "right_column": "y",
		})
		mustInsert(t, db, "audit_events", map[string]any{
			"occurred_at": at, "actor_kind": "user", "action": "x", "resource_type": "r", "outcome": "allowed",
		})
	}

	if err := repartitionQueryHistory(db); err != nil {
		t.Fatalf("repartition: %v", err)
	}

	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, tbl := range partitionedTables() {
		parts, err := pm.ListPartitions(context.Background(), tbl.spec.Table)
		if err != nil || len(parts) < partitionDaysAhead+1 {
			t.Fatalf("%s partitions = %d, %v", tbl.spec.Table, len(parts), err)
		}
		if db.Migrator().HasTable(partitioning.LegacyTable(tbl.spec.Table)) {
			t.Fatalf("%s: legacy table should be gone", tbl.spec.Table)
		}
		// The partitions are one day wide, from the oldest row through the
		// horizon, and a row lives in the partition of its own day.
		for _, p := range parts {
			if p.Until.Sub(p.From) != 24*time.Hour {
				t.Fatalf("%s partition %s spans %v, want one day", tbl.spec.Table, p.Name, p.Until.Sub(p.From))
			}
		}
		if want := partitioning.PartitionName(tbl.spec.Table, partitioning.Daily, months[0]); parts[0].Name != want {
			t.Fatalf("%s first partition = %s, want %s", tbl.spec.Table, parts[0].Name, want)
		}
		var n int64
		if err := db.Table(tbl.spec.Table).Count(&n).Error; err != nil || n != 3 {
			t.Fatalf("%s rows = %d, %v; want 3", tbl.spec.Table, n, err)
		}
		day := partitioning.PartitionName(tbl.spec.Table, partitioning.Daily, months[1])
		var inDay int64
		if err := db.Table(day).Count(&inDay).Error; err != nil || inDay != 1 {
			t.Fatalf("%s rows = %d, %v; want the Feb 20 row alone in its day", day, inDay, err)
		}
	}

	// The month was backfilled from the parent execution.
	var statements []model.QueryStatement
	if err := db.Order("position, id").Find(&statements).Error; err != nil || len(statements) != 3 {
		t.Fatalf("statements = %d, %v", len(statements), err)
	}
	for i, st := range statements {
		if !st.ExecutionStartedAt.Equal(months[i]) {
			t.Errorf("statement %d execution_started_at = %v, want %v", i, st.ExecutionStartedAt, months[i])
		}
	}
	var refs []model.StatementReference
	if err := db.Find(&refs).Error; err != nil || len(refs) != 3 {
		t.Fatalf("references = %d, %v", len(refs), err)
	}
	for _, r := range refs {
		if r.ExecutionStartedAt.IsZero() {
			t.Errorf("reference %s has no execution_started_at", r.StatementID)
		}
	}

	// The audit id stays one global sequence: the next id follows the copied ones.
	next := model.AuditEvent{OccurredAt: months[2], ActorKind: "user", Action: "x", ResourceType: "r", Outcome: "allowed"}
	if err := db.Create(&next).Error; err != nil {
		t.Fatal(err)
	}
	if next.ID != 4 {
		t.Errorf("next audit id = %d, want 4", next.ID)
	}

	// The composite foreign key is enforced: a statement cannot name a month its
	// execution does not have.
	bad := model.QueryStatement{
		ID: "20000000-0000-4000-8000-0000000000ff", ExecutionID: model.UUID(execIDs[0]),
		ExecutionStartedAt: months[1], Position: 9, SQL: "x", Status: "finished", ResultSets: model.JSON("[]"),
	}
	if err := db.Create(&bad).Error; err == nil {
		t.Error("a statement with the wrong execution month should violate the composite foreign key")
	}
	// A second run is a no-op.
	if err := repartitionQueryHistory(db); err != nil {
		t.Fatalf("second repartition: %v", err)
	}
}

func mustInsert(t *testing.T, db *gorm.DB, table string, row map[string]any) {
	t.Helper()
	if err := db.Table(table).Create(row).Error; err != nil {
		t.Fatalf("insert into %s: %v", table, err)
	}
}

// legacyLeftover is the table an interrupted conversion leaves behind.
type legacyLeftover struct {
	ID int64 `gorm:"column:id;primaryKey"`
}

func (legacyLeftover) TableName() string { return "audit_events_legacy" }

// TestRepartitionResumesAfterAFailedTable: the stages commit separately, so a
// failure while converting one table leaves the tables before it converted and
// the rest untouched, and a rerun finishes the job without redoing or losing
// anything.
func TestRepartitionResumesAfterAFailedTable(t *testing.T) {
	t.Parallel()
	db := newPostgresStore(t)
	if err := AutoMigrate(db, Models()...); err != nil {
		t.Fatalf("schema: %v", err)
	}
	toOldShape(t, db)
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for range 2 {
		mustInsert(t, db, "audit_events", map[string]any{
			"occurred_at": at, "actor_kind": "user", "action": "x", "resource_type": "r", "outcome": "allowed",
		})
	}

	// A leftover legacy table makes the audit conversion refuse, as an
	// interrupted earlier attempt would. audit_events is converted last.
	if err := AutoMigrate(db, &legacyLeftover{}); err != nil {
		t.Fatal(err)
	}
	if err := repartitionQueryHistory(db); err == nil {
		t.Fatal("the audit conversion should refuse beside a legacy table")
	}
	pm, err := partitioning.New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, table := range []string{"query_executions", "query_statements"} {
		if parts, err := pm.ListPartitions(ctx, table); err != nil || len(parts) == 0 {
			t.Fatalf("%s should already be converted: %d partitions, %v", table, len(parts), err)
		}
	}
	if parts, _ := pm.ListPartitions(ctx, "audit_events"); len(parts) != 0 {
		t.Fatalf("audit_events must be untouched, has %d partitions", len(parts))
	}
	var n int64
	if err := db.Table("audit_events").Count(&n).Error; err != nil || n != 2 {
		t.Fatalf("audit_events rows = %d, %v; want 2", n, err)
	}

	// The operator removes the leftover and reruns.
	if err := db.Migrator().DropTable(&legacyLeftover{}); err != nil {
		t.Fatal(err)
	}
	if err := repartitionQueryHistory(db); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if parts, err := pm.ListPartitions(ctx, "audit_events"); err != nil || len(parts) == 0 {
		t.Fatalf("audit_events should now be converted: %d partitions, %v", len(parts), err)
	}
	if err := db.Table("audit_events").Count(&n).Error; err != nil || n != 2 {
		t.Fatalf("audit_events rows after the rerun = %d, %v; want 2", n, err)
	}
	if db.Migrator().HasTable(partitioning.LegacyTable("audit_events")) {
		t.Fatal("legacy table should be gone after the rerun")
	}
}
