package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// Re-partitioning of the append-only tables (ADR 0024, issue #948). The
// transition is expressed with the ORM and the PartitionManager seam: the only
// SQL it needs (partition DDL, the key swap, the id sequence) lives in the
// adapter, and the backend is told apart by Capabilities, never by dialect name.

const (
	// partitionMonthsAhead is how many months past the current one the
	// migration provisions, the same horizon the audit-partitions schedule keeps
	// extending. Engines with a catch-all partition take later months in it;
	// PostgreSQL does not.
	partitionMonthsAhead = 3
	// copyBatch is the number of rows moved per round trip when a table is
	// converted by copy (PostgreSQL).
	copyBatch = 500
	// backfillBatch is the number of parents resolved per backfill round.
	backfillBatch = 500
)

// partitionedTable is one table that becomes monthly range-partitioned.
type partitionedTable struct {
	model any
	spec  partitioning.Spec
	// copyRows moves the rows of the set-aside legacy table into the
	// partitioned one, with their ids.
	copyRows func(db *gorm.DB, legacy string) error
	// idColumn names an auto-generated id that stays one global sequence.
	idColumn string
}

func rangeSpec(table, column string) partitioning.Spec {
	return partitioning.Spec{Table: table, Column: column, Strategy: partitioning.Range, Interval: partitioning.Monthly}
}

// partitionedTables lists the partitioned tables, parents before children,
// with how each is copied when its engine converts by copy.
func partitionedTables() []partitionedTable {
	copiers := map[string]func(*gorm.DB, string) error{
		"query_executions": copyRows[model.QueryExecution],
		"query_statements": copyRows[model.QueryStatement],
		"audit_events":     copyRows[model.AuditEvent],
	}
	var out []partitionedTable
	for _, p := range model.PartitionedTables() {
		t := partitionedTable{model: p.Model, spec: rangeSpec(p.Table, p.Column), copyRows: copiers[p.Table]}
		if p.Table == "audit_events" {
			t.idColumn = "id"
		}
		out = append(out, t)
	}
	return out
}

func isPartitioned(table string) bool {
	for _, t := range partitionedTables() {
		if t.spec.Table == table {
			return true
		}
	}
	return false
}

// Nullable shims, one per table that gains the parent's month. They let the
// column be added nullable to a table that already has rows; the model's NOT
// NULL is applied once the column is backfilled.
type (
	statementMonth struct {
		ExecutionStartedAt *time.Time `gorm:"column:execution_started_at"`
	}
	referenceMonth struct {
		ExecutionStartedAt *time.Time `gorm:"column:execution_started_at"`
	}
	joinMonth struct {
		ExecutionStartedAt *time.Time `gorm:"column:execution_started_at"`
	}
	snapshotMonth struct {
		ExecutionStartedAt *time.Time `gorm:"column:execution_started_at"`
	}
)

func (statementMonth) TableName() string { return "query_statements" }
func (referenceMonth) TableName() string { return "statement_references" }
func (joinMonth) TableName() string      { return "statement_joins" }
func (snapshotMonth) TableName() string  { return "trino_query_snapshots" }

// monthColumn is the denormalized execution_started_at of one child table.
type monthColumn struct {
	shim    any
	model   any
	table   string // the child table
	fk      string // column holding the parent's id
	parent  string // the parent table
	parentV string // the parent's month column
	notNull bool   // the model declares the column NOT NULL
}

func monthColumns() []monthColumn {
	return []monthColumn{
		{shim: &statementMonth{}, model: &model.QueryStatement{}, table: "query_statements", fk: "execution_id", parent: "query_executions", parentV: "started_at", notNull: true},
		{shim: &snapshotMonth{}, model: &model.TrinoQuerySnapshot{}, table: "trino_query_snapshots", fk: "execution_id", parent: "query_executions", parentV: "started_at"},
		{shim: &referenceMonth{}, model: &model.StatementReference{}, table: "statement_references", fk: "statement_id", parent: "query_statements", parentV: "execution_started_at", notNull: true},
		{shim: &joinMonth{}, model: &model.StatementJoin{}, table: "statement_joins", fk: "statement_id", parent: "query_statements", parentV: "execution_started_at", notNull: true},
	}
}

// repartitionQueryHistory moves audit_events, query_executions and
// query_statements to monthly range partitions keyed by the month, and the
// foreign keys that reference them to composite ones.
func repartitionQueryHistory(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		cols := monthColumns()
		for _, c := range cols {
			if !tx.Migrator().HasColumn(c.shim, "ExecutionStartedAt") {
				if err := tx.Migrator().AddColumn(c.shim, "ExecutionStartedAt"); err != nil {
					return fmt.Errorf("add execution_started_at: %w", err)
				}
			}
		}
		// Parents first: a reference reads the month its statement just got.
		for _, c := range cols {
			if err := backfillMonth(tx, c); err != nil {
				return err
			}
		}
		for _, c := range cols {
			if c.notNull {
				if err := requireNotNull(tx, c.model); err != nil {
					return err
				}
			}
		}
		if err := dropHistoryForeignKeys(tx); err != nil {
			return err
		}
		if err := partitionTables(tx); err != nil {
			return err
		}
		return createForeignKeys(tx, historyModels())
	})
}

// historyModels are the models whose foreign keys the transition touches.
func historyModels() []any {
	return []any{
		&model.QueryExecution{}, &model.QueryStatement{}, &model.AuditEvent{},
		&model.TrinoQuerySnapshot{}, &model.StatementReference{}, &model.StatementJoin{},
	}
}

// backfillMonth fills execution_started_at from the parent row, in batches of
// parents, in Go. It resumes where it stopped: only rows still NULL are read.
func backfillMonth(db *gorm.DB, c monthColumn) error {
	for {
		var ids []string
		// Table, not Model: updating a model's key column would leave the value
		// behind as a condition for the next statement built on the same model.
		if err := db.Table(c.table).
			Where("execution_started_at IS NULL AND "+c.fk+" IS NOT NULL").
			Distinct(c.fk).Limit(backfillBatch).Pluck(c.fk, &ids).Error; err != nil {
			return fmt.Errorf("backfill execution_started_at: read parents: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		months, err := parentMonths(db, c, ids)
		if err != nil {
			return err
		}
		if len(months) == 0 {
			return fmt.Errorf("backfill execution_started_at: %d rows reference parents that do not exist (first: %s)", len(ids), ids[0])
		}
		// Parents that share a month are updated together.
		byMonth := map[int64][]string{}
		at := map[int64]time.Time{}
		for id, m := range months {
			key := m.UnixNano()
			byMonth[key] = append(byMonth[key], id)
			at[key] = m
		}
		var updated int64
		for key, group := range byMonth {
			res := db.Table(c.table).
				Where(c.fk+" IN ? AND execution_started_at IS NULL", group).
				Update("execution_started_at", at[key])
			if res.Error != nil {
				return fmt.Errorf("backfill execution_started_at: %w", res.Error)
			}
			updated += res.RowsAffected
		}
		// A round that changes nothing would repeat forever.
		if updated == 0 {
			return fmt.Errorf("backfill execution_started_at on %T made no progress", c.table)
		}
	}
}

// parentMonths returns the parents' month by id.
func parentMonths(db *gorm.DB, c monthColumn, ids []string) (map[string]time.Time, error) {
	rows, err := db.Table(c.parent).Select("id", c.parentV).Where("id IN ?", ids).Rows()
	if err != nil {
		return nil, fmt.Errorf("backfill execution_started_at: read months: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]time.Time, len(ids))
	for rows.Next() {
		var id string
		var month time.Time
		if err := rows.Scan(&id, &month); err != nil {
			return nil, fmt.Errorf("backfill execution_started_at: scan: %w", err)
		}
		out[id] = month
	}
	return out, rows.Err()
}

// requireNotNull applies the model's NOT NULL to a column that was added
// nullable, unless the column already is.
func requireNotNull(db *gorm.DB, m any) error {
	types, err := db.Migrator().ColumnTypes(m)
	if err != nil {
		return fmt.Errorf("read columns: %w", err)
	}
	for _, ct := range types {
		if ct.Name() != "execution_started_at" {
			continue
		}
		if nullable, ok := ct.Nullable(); ok && !nullable {
			return nil
		}
	}
	if err := db.Migrator().AlterColumn(m, "ExecutionStartedAt"); err != nil {
		return fmt.Errorf("require execution_started_at: %w", err)
	}
	return nil
}

// historyForeignKeys returns every belongs-to relation that has a partitioned
// table on either side: the key a partitioned table references changes shape,
// and an engine that forbids foreign keys on partitioned tables drops them all.
func historyForeignKeys(db *gorm.DB) ([]foreignKey, error) {
	var out []foreignKey
	for _, m := range Models() {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(m); err != nil {
			return nil, err
		}
		for name, rel := range stmt.Schema.Relationships.Relations {
			if rel.Type != schema.BelongsTo {
				continue
			}
			if touchesPartitioned(stmt.Schema.Table, rel.FieldSchema.Table) {
				out = append(out, foreignKey{model: m, name: name})
			}
		}
	}
	return out, nil
}

type foreignKey struct {
	model any
	name  string
}

func touchesPartitioned(tables ...string) bool {
	for _, t := range tables {
		if isPartitioned(t) {
			return true
		}
	}
	return false
}

func dropHistoryForeignKeys(db *gorm.DB) error {
	fks, err := historyForeignKeys(db)
	if err != nil {
		return err
	}
	for _, fk := range fks {
		if !db.Migrator().HasConstraint(fk.model, fk.name) {
			continue
		}
		if err := db.Migrator().DropConstraint(fk.model, fk.name); err != nil {
			return fmt.Errorf("drop foreign key %s on %T: %w", fk.name, fk.model, err)
		}
	}
	return nil
}

// partitionTables provisions every partitioned table through the adapter. A
// backend with no adapter keeps its plain tables: the schema still changes
// shape, but nothing is partitioned.
func partitionTables(db *gorm.DB) error {
	if !partitioning.CapabilitiesFor(db).Implemented {
		return nil
	}
	pm, err := partitioning.New(db)
	if err != nil {
		if errors.Is(err, partitioning.ErrNotImplemented) || errors.Is(err, partitioning.ErrUnsupportedPartitioning) {
			return nil
		}
		return err
	}
	ctx := db.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()
	until := partitioning.Monthly.Start(now).AddDate(0, partitionMonthsAhead+1, 0)
	for _, t := range partitionedTables() {
		from, err := firstMonth(db, t, now)
		if err != nil {
			return err
		}
		if err := pm.Provision(ctx, t.model, t.spec, from, until); err != nil {
			return fmt.Errorf("partition %s: %w", t.spec.Table, err)
		}
		if err := finishConversion(ctx, db, pm, t); err != nil {
			return err
		}
	}
	return nil
}

// firstMonth is the first month a partition must cover: the oldest row's, or
// the current month on an empty table.
func firstMonth(db *gorm.DB, t partitionedTable, now time.Time) (time.Time, error) {
	var oldest []time.Time
	if err := db.Model(t.model).Order(t.spec.Column).Limit(1).Pluck(t.spec.Column, &oldest).Error; err != nil {
		return time.Time{}, fmt.Errorf("oldest row of %s: %w", t.spec.Table, err)
	}
	from := partitioning.Monthly.Start(now)
	if len(oldest) > 0 && oldest[0].Before(from) {
		from = partitioning.Monthly.Start(oldest[0])
	}
	return from, nil
}

// finishConversion completes a conversion that set the old table aside: the
// rows are copied through the ORM with their ids, the id sequence moves past
// them, and the old table goes.
func finishConversion(ctx context.Context, db *gorm.DB, pm partitioning.Manager, t partitionedTable) error {
	legacy := partitioning.LegacyTable(t.spec.Table)
	if !db.Migrator().HasTable(legacy) {
		return nil
	}
	if err := t.copyRows(db, legacy); err != nil {
		return fmt.Errorf("copy %s into %s: %w", legacy, t.spec.Table, err)
	}
	if syncer, ok := pm.(partitioning.SequenceSyncer); ok && t.idColumn != "" {
		var last []int64
		if err := db.Model(t.model).Order(t.idColumn+" DESC").Limit(1).Pluck(t.idColumn, &last).Error; err != nil {
			return fmt.Errorf("last %s of %s: %w", t.idColumn, t.spec.Table, err)
		}
		if len(last) > 0 {
			if err := syncer.SyncSequence(ctx, t.spec.Table, t.idColumn, last[0]); err != nil {
				return err
			}
		}
	}
	if err := db.Migrator().DropTable(legacy); err != nil {
		return fmt.Errorf("drop %s: %w", legacy, err)
	}
	return nil
}

// copyRows copies a legacy table into the table of T, preserving every column
// including the id. DO NOTHING makes a repeated copy harmless.
func copyRows[T any](db *gorm.DB, legacy string) error {
	var rows []T
	return db.Table(legacy).FindInBatches(&rows, copyBatch, func(_ *gorm.DB, _ int) error {
		return db.Session(&gorm.Session{NewDB: true}).
			Clauses(clause.OnConflict{DoNothing: true}).
			CreateInBatches(&rows, 100).Error
	}).Error
}
