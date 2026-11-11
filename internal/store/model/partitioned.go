package model

// Partitioned is a table that is range-partitioned by month on one of its
// columns (ADR 0024).
type Partitioned struct {
	// Model is the table's model; its key carries the partition column.
	Model any
	Table string
	// Column is the timestamp the table is partitioned by.
	Column string
}

// PartitionedTables lists the partitioned tables, parents before children:
// query_statements is partitioned by the month it denormalizes from its
// execution, so it follows query_executions.
func PartitionedTables() []Partitioned {
	return []Partitioned{
		{Model: &QueryExecution{}, Table: "query_executions", Column: "started_at"},
		{Model: &QueryStatement{}, Table: "query_statements", Column: "execution_started_at"},
		{Model: &AuditEvent{}, Table: "audit_events", Column: "occurred_at"},
	}
}
