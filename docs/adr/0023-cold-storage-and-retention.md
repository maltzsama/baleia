# 0023. Cold storage and retention for the audit trail and query history

- Status: Accepted
- Date: 2026-11-10
- Issues: #948
- Supersedes: the documented "400 days" audit convention (never enforced) and `migrate -cmd=audit-prune` as the retention mechanism.
- Amends: [0020](0020-gorm-store.md): table partitioning is re-introduced behind a `PartitionManager` seam.
- Related: `audit-partitions`, `audit-export` (Temporal schedules); `internal/storage` S3 plumbing (`ResultKey`).

## Context

Two append-only datasets grow without bound in the operational PostgreSQL database, which is the most expensive place to keep them:

1. **`audit_events`** — write-once, read-rarely, secrets redacted at write time. What is actually deployed today is weaker than the old design documents describe, because [0020](0020-gorm-store.md) moved the store to GORM:
   - It is a **single plain table**, not monthly-partitioned. `EnsureAuditPartitions` is a no-op (`internal/store/repo/querier_compat.go`), so the `audit-partitions` schedule provisions nothing.
   - Append-only is enforced **by convention** (the recorder only inserts), not by a trigger: 0020 removed triggers.
   - `audit.Prune` looks for `audit_events_YYYYMM` tables that the GORM schema never creates, so `migrate -cmd=audit-prune` drops nothing. Retention is therefore **unimplemented**, not merely manual.
   - An optional OpenSearch `_bulk` export (`audit-export`) exists and reads the plain table, cursor-keyed in `audit_export_cursor`. It never modifies `audit_events`.

2. **`query_executions` / `query_statements`** — full SQL per statement plus status/duration/error metadata, written on every query. **No retention, prune or export.** This is the larger growth risk. Neither table is partitioned.

The value of both datasets decays over time; the tail belongs in object storage, not in the hot database.

## Decision

1. **Retention is configuration, enforced on a schedule.** `BALEIA_AUDIT_RETENTION` (default `400d`) and `BALEIA_QUERY_HISTORY_RETENTION` (default `180d`) replace conventions and manual commands. Each is enforced by a daily Temporal schedule (`audit-retention`, `query-history-retention`). The current month and later are never pruned.

2. **Query history follows the same path as the audit trail: partition → offload → prune.** It is *offloaded*, not merely expired. SQL text carries lineage value and must survive the hot window in cold storage.

3. **Partitioning is native range by month, behind an engine-neutral `PartitionManager` seam, on every backend that provides it.** This amends 0020's "no table partitions": partitioning returns, but no service or repository authors partition DDL. The seam is not shaped like one engine's DDL — `Provision` creates-or-extends in a single call because several engines (MySQL, TiDB) require partitions at creation time. A registry dispatches per GORM dialector: PostgreSQL (implemented and tested first), GaussDB (PostgreSQL-compatible, same adapter), MySQL/MariaDB and TiDB (native range; the query-history foreign keys are dropped there because InnoDB forbids them on partitioned tables), SQL Server (partition function + partition scheme) and Oracle (native partitioning, which requires the paid Partitioning option). **SQLite has no native partitioning and is emulated** by the SQLite adapter: the table stays a plain table, monthly partitions are tracked in a per-table sidecar registry, and `DropPartition` is a bounded `DELETE` rather than a metadata drop. The emulation is explicit — `Capabilities().NativeRange` is false and `Capabilities().Emulated` is true, so the retention layer never mistakes it for native partitioning. A backend whose adapter has not shipped returns a clear not-implemented error rather than pretending parity. Partition drop is a metadata operation where the engine supports it, and a bounded `DELETE` where it does not.

4. **Partition keys use columns that exist.** `audit_events` partitions by `occurred_at`; `query_executions` partitions by `started_at` (the table has `started_at`, not `created_at`); `query_statements` partitions by a denormalized `execution_started_at` so its foreign key stays inside the parent's month. PostgreSQL requires the partition key in every primary/unique key, so the keys include it — and because a foreign key must reference a whole unique key, **every** table that references `query_executions` or `query_statements` carries the parent's month and switches to a composite foreign key (see the schema below).

5. **Offload cursors are keyed by `(stream, sink)`.** `audit_export_cursor` gains a `stream` column (`audit`, `query_history`). This lets one sink serve both streams and multiple sinks serve one stream without further schema change.

6. **Prune is gated on the cold sink cursor and the per-month manifest.** A partition is droppable only if (a) the cold-sink cursor for that stream has advanced past the partition's upper bound, and (b) the per-month manifest for that partition exists and matches the expected object count/checksum. The OpenSearch export is **not** a gate — it is a search/forensics path, not a retention path.

7. **The cold sink is S3-compatible and first-class.** The audit export `Sink` interface is generalized so the existing `_bulk` HTTP sink and the S3 sink are configured independently. S3 objects are gzip NDJSON, one object set per month under a configured prefix, with deterministic names (idempotent, re-runnable) and a per-month manifest recording keys, counts and checksums. The cursor advances only after the manifest is written.

8. **Prune is fail-closed.** If the cold sink is unconfigured, unreachable, or its cursor has not advanced past a candidate partition, that partition is skipped and an alert fires. It is never dropped. An unconfigured deployment grows (with alerts) rather than losing data.

9. **Format: NDJSON gzip for v1.** It matches the OpenSearch document shape, requires no schema evolution, and is readable with `zcat | jq`. Parquet (compact, queryable via Trino/Athena) is deferred until a re-queryability requirement is demonstrated. Objects are organized so a future Parquet sink can coexist.

10. **A per-month manifest is sufficient.** No per-row pointers and no global inventory table — both would approach the size of the data and defeat the purpose. Operators discover what left the DB from the manifest keys, which are deterministic from `(stream, prefix, month)`.

11. **Query-history offload keeps execution + statements as one unit.** The manifest records the `(query_executions, query_statements)` pair for the month. Statements are never offloaded without their parent execution, and a partition is not droppable until the pair is complete.

12. **SQL in cold storage is stored raw** under a private, encrypted (SSE), least-privilege bucket. Redaction of SQL literals at offload is explicitly deferred (see Consequences); in-DB SQL remains governed by database access controls.

13. **The manual prune command is retained as an emergency escape hatch**, but it now respects the same gate and gains `--dry-run` and a per-run cap.

## PartitionManager

The physical mechanism is isolated behind a small seam; the retention decision does not live there.

```go
type Manager interface {
    Provision(ctx context.Context, model any, spec Spec, from, until time.Time) error
    ListPartitions(ctx context.Context, table string) ([]Partition, error)
    DropPartition(ctx context.Context, table string, from, until time.Time) error
    Capabilities() Capabilities
}
```

- `Spec` names the table, the partition column, the strategy (`range`) and the interval (`monthly`).
- `Provision` is create-or-extend: it creates the partitioned table if absent, converts an existing plain table in place, and creates the partitions covering `[from, until)`. It is a single call because MySQL/TiDB cannot create a partitioned table without defining its partitions.
- `New(db *gorm.DB)` resolves the adapter from `db.Dialector.Name()` through a registry. `Capabilities()`/`capabilitiesFor` is the single source of truth for what a backend can do, so the retention layer does not assume one physical model.
- The PostgreSQL adapter uses `gorm:table_options` to create the parent (`PARTITION BY RANGE (...)`) and dialect DDL to create/drop partitions. That DDL is confined to the adapter, which is the carve-out recorded in [0020](0020-gorm-store.md).
- The manager executes physical operations when authorized; it does **not** know about S3, manifests or offload cursors. The worker/schedule owns that judgment.

| Backend | Native range | FK on partitioned | Adapter |
|---|---|---|---|
| PostgreSQL | yes | yes | implemented, DB-tested |
| GaussDB | yes | yes | PostgreSQL-compatible, same adapter |
| MySQL / MariaDB | yes | **no** (InnoDB) | implemented |
| TiDB | yes | version-dependent | pending |
| SQL Server | yes (scheme model) | yes | pending |
| Oracle | yes (Partitioning option) | yes | pending |
| SQLite | **no** | n/a | emulated (plain table + registry, drop = `DELETE`) |

## Schema

**`audit_events`** — re-created as `RANGE (occurred_at)` by month. Because the partition key must be part of every primary/unique key, the primary key becomes `(id, occurred_at)`. `id` stays a single global sequence, so the `(occurred_at, id)` export cursor and keyset pagination keep working.

**`query_executions`** — `RANGE (started_at)` by month. Primary key `(id, started_at)`.

**`query_statements`** — `RANGE (execution_started_at)` by month. The parent's `started_at` is denormalized as `execution_started_at`. Because PostgreSQL requires the partition key in every unique key, a foreign key must reference the whole composite key, so every table that references a partitioned table carries the parent's month. Four foreign keys change shape, not one:

```text
query_executions        PRIMARY KEY (id, started_at)
query_statements        FOREIGN KEY (execution_id, execution_started_at)
                        REFERENCES query_executions (id, started_at)
trino_query_snapshots   FOREIGN KEY (execution_id, execution_started_at)
                        REFERENCES query_executions (id, started_at)

query_statements        PRIMARY KEY (id, execution_started_at)
statement_references    FOREIGN KEY (statement_id, execution_started_at)
                        REFERENCES query_statements (id, execution_started_at)
statement_joins         FOREIGN KEY (statement_id, execution_started_at)
                        REFERENCES query_statements (id, execution_started_at)
```

`trino_query_snapshots`, `statement_references` and `statement_joins` each gain a denormalized `execution_started_at` column, mirroring `query_statements`. None of the three is itself partitioned; only their foreign key changes shape.

- Partition provisioning mirrors the audit approach: a schedule creates partitions ahead of time; nothing creates them retroactively after a drop (late inserts into a dropped month fail loudly, which is the desired behavior).
- MySQL/MariaDB InnoDB and TiDB forbid foreign keys on partitioned tables. Both query-history tables are partitioned on those engines too; the four foreign keys above are **dropped**, and referential integrity is enforced by the application, which writes a statement only with its execution and offloads/prunes the pair as one unit. PostgreSQL keeps the composite foreign keys. This is an engine limitation, not a GORM one.

## Query history offload design

- `audit_export_cursor` → `(stream, sink) → cursor`, with `stream ∈ {audit, query_history}` and `sink ∈ {opensearch, s3}`.
- The `query_history` stream reads executions and their statements in `started_at` order, writes the month's NDJSON objects to the cold sink, and finalizes the month's manifest. Only then does the cursor advance past the month boundary.
- The `Sink` interface, deterministic naming, manifest format, gzip NDJSON encoding and cursor-only-advance protocol are shared between the two streams; only the query and row shape differ.

## Retention semantics

- **Cutoff:** a partition for month `M` is a candidate once `now ≥ end_of_month(M) + retention + grace`, with `BALEIA_*_COLD_GRACE` defaulting to `7d`. The grace period absorbs late-arriving events and clock skew.
- **Gate:** a candidate is dropped only if the cold-sink cursor for the stream is `≥ end_of_month(M)` **and** the manifest for `M` exists and validates. Otherwise: skip, emit metric, alert.
- **Late data:** rows inserted into a month older than the retention window but not yet pruned are offloaded by the next sink run before that partition becomes droppable. Rows arriving *after* a partition is dropped are rejected by the missing partition. This is an accepted limitation and is documented in the runbook.
- **Shortening retention:** lowering the configured window may make several months droppable at once. Each run drops at most one partition per stream (`BALEIA_*_PRUNE_MAX_PARTITIONS`, default `1`) and emits a warning when the backlog exceeds that, so a config change degrades into a controlled drain rather than a single destructive burst.
- **First run on an existing deployment:** the initial offload covers years of data. The schedules run with a configurable throttle and the manual command supports `--dry-run` reporting exactly what would be offloaded and dropped before the schedule is enabled.

## Configuration

| Setting | Default | Meaning |
|---|---|---|
| `BALEIA_AUDIT_RETENTION` | `400d` | Hot window for `audit_events`; enforced daily |
| `BALEIA_AUDIT_COLD_BUCKET` / `_PREFIX` | (required) | S3-compatible cold destination for audit; prune is skipped without it |
| `BALEIA_AUDIT_EXPORT_URL` | (off) | OpenSearch export (unchanged; not a gate) |
| `BALEIA_QUERY_HISTORY_RETENTION` | `180d` | Hot window for query history; enforced daily |
| `BALEIA_QUERY_HISTORY_COLD_BUCKET` / `_PREFIX` | (required) | S3-compatible cold destination for query history |
| `BALEIA_AUDIT_COLD_GRACE` / `BALEIA_QUERY_HISTORY_COLD_GRACE` | `7d` | Safety margin before a partition becomes droppable |
| `BALEIA_*_PRUNE_MAX_PARTITIONS` | `1` | Per-run drop cap per stream |

## Consequences

**Positive**

- The database stops growing unboundedly under normal operation, with the growth risk removed from operator memory and moved into configuration and alerts.
- Retention is uniform across both logs: same gate, same manifest, same idempotency guarantees, same failure mode (skip + alert, never silent loss).
- The existing OpenSearch export is preserved and becomes additive rather than load-bearing.
- Cold data is discoverable from deterministic keys and monthly manifests without a per-row index.

**Negative**

- Two more Temporal schedules and two more configuration surfaces to operate.
- Re-partitioning `audit_events`, `query_executions` and `query_statements` is a non-trivial migration on existing tables (rewrite or partition-swap), and it is a hard prerequisite for retention.
- `(stream, sink)` cursor keying changes the schema of `audit_export_cursor`.
- Cold storage adds S3 cost and a second copy of SQL text that must be governed.
- Partitioning is not uniform across backends: engines differ in DDL shape, in whether foreign keys survive (`MySQL`/`TiDB`), and in licensing (`Oracle`). SQLite has no native partitioning, so it is **emulated** (plain table, a sidecar registry, and a bounded `DELETE` for `DropPartition`) rather than pretending native parity. The capability matrix makes these differences explicit.

**Risks**

- **SQL text in object storage is a compliance surface.** v1 stores it raw behind a private, SSE-encrypted, least-privilege bucket. Reliable SQL-literal redaction is hard and can silently corrupt lineage, so it is deferred; if compliance requires it, a redaction pass at offload is the follow-up and is tracked as an open action, not an open question.
- **Fail-closed means an unconfigured deployment still grows.** This is deliberate: the alternative (prune without offload) loses data. Alerts on cold-sink health and on oldest-unpruned-partition age make the condition visible.
- **Late-arriving rows past a drop boundary are lost**, bounded by the grace period.
- **The initial backfill is large**; without the throttle and `--dry-run` it is a one-off operational hazard.

## Current compliance

The `PartitionManager` seam exists in `internal/persistence/partitioning`: the interface, `Spec`/`Capabilities` (with `Emulated`), and three adapters with tests — PostgreSQL, MySQL/MariaDB (native range, foreign keys dropped) and SQLite (emulated). TiDB rides the MySQL adapter through the MySQL protocol. SQL Server and Oracle have GORM dialectors (`gorm.io/driver/sqlserver`; Godror-based `gorm-oracle` or pure-Go `cengsin/oracle`), but their adapters are not yet shipped: the store wires only PostgreSQL and MySQL, and the store test harness (`internal/testdb`) covers only those two backends. Nothing else in this decision is implemented yet: `audit_events` is still a single plain table, `EnsureAuditPartitions` and `audit.Prune` are effectively no-ops, and query history has no lifecycle management. #948 tracks the rollout.

## Rollout

1. Ship the `PartitionManager` seam (`internal/persistence/partitioning`): interface, spec, PostgreSQL adapter, unit and DB-backed tests. Additive, no schema change.
2. Re-partition `audit_events` by `occurred_at` and `query_executions` / `query_statements` by `started_at` (off-peak, swap-based).
3. Ship the `(stream, sink)` cursor migration and the generalized `Sink` interface; no behavior change (OpenSearch export only).
4. Ship the S3 sink and the manifest writer; enable it for audit with prune still manual and gated. Run `--dry-run` on production to size the initial offload.
5. Enable `audit-retention` with a throttle; validate that dropped months have complete manifests and that the OpenSearch path is unaffected.
6. Ship the `query_history` stream and enable `query-history-retention`.

Each step is independently revertible: disabling a schedule stops pruning, and the manual command remains available.

## Monitoring and alerts

- Cursor lag per `(stream, sink)`.
- Age of the oldest partition not yet pruned, per stream.
- Prune skips (counted, with reason: gate not met, manifest invalid, sink unreachable).
- Cold-sink error rate and manifest validation failures.
- Database growth rate per table, and hot-window size per stream.
- Alert thresholds: cursor lag > 2 days, oldest unpruned partition > retention + grace + 7d, any manifest validation failure.

## Restore / rehydration

Cold data is recovered from the manifest for a month: `zcat` the month's NDJSON objects and load them back into a scratch database (or query them in place with a Trino/Athena-compatible reader once Parquet exists). Restore is manual and out-of-band; the ADR commits to a documented runbook and to the manifest containing everything needed to reconstruct a month (keys, counts, checksums, stream, month), not to an automated rehydration tool.

## Cold retention and lifecycle

The hot window is this ADR's concern; the cold window is a bucket lifecycle policy owned by operations. Objects are written under a prefix that makes a lifecycle rule (`Glacier`/expiry) expressible per stream. Legal hold / compliance retention for `audit` is set by policy, not by this ADR, and is expected to exceed the hot window substantially. Query history has no legal-hold requirement in v1.

## Alternatives considered

- **Batch archive every N months.** Rejected: up to N months with no offload copy, and it regresses the continuous export.
- **Keep retention manual.** Rejected: it is the source of the unbounded-growth risk.
- **A single generic `cold_log` + `retention_log_period`.** Rejected: audit and query history differ in shape, sensitivity and value; one knob conflates distinct concerns.
- **Expire query history without offload.** Rejected: full SQL has lineage value, and a plain `DELETE` on a large append-only table is the slowest possible way to reclaim space.
- **Gate prune on the OpenSearch cursor.** Rejected: OpenSearch is an optional search path, not a durable retention copy; gating on it would make an optional feature a hard dependency of reclamation.
- **Plain monthly tables controlled by the application.** Rejected by the owner in favour of native `RANGE` behind a `PartitionManager` where the backend provides it: native partitioning keeps routing and partition pruning in the database and avoids hand-routing every write and unioning every range query. On SQLite, which has no native partitioning, the `PartitionManager` adapter **emulates** the contract (a sidecar registry plus a bounded `DELETE`) so the retention layer stays uniform; the emulation is signalled by `Capabilities().Emulated`, not hidden.
- **`gorm.io/sharding`.** Rejected: it is hash sharding by a high-cardinality key, has no time boundary, requires the shard key in every query, and cannot drop a month — it solves scale-out, not retention.

## Follow-ups (not blocking)

- Parquet sink, if re-queryability of cold data is required.
- SQL-literal redaction at offload, if compliance requires it.
- Automated rehydration tooling.
- Global cold inventory, if manifest browsing proves insufficient in practice.
- A MySQL/MariaDB partitioning adapter or a portable plain-table fallback, if those backends must retain partition-based retention.

## References

- [0020](0020-gorm-store.md): GORM store; amended here for table partitioning.
- `audit-partitions`, `audit-export` Temporal schedules.
- `audit.Prune`, `migrate -cmd=audit-prune`.
- `internal/storage` S3 plumbing (`ResultKey`).
