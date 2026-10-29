---
sidebar_position: 4
---

# Monitoring and maintenance

**Monitoring** shows, per engine, the cluster load, the queries running on it and the health of Iceberg tables. It covers federated engines such as Trino.

## Cluster

Workers, running, queued and blocked queries, and memory, sampled by a poller and drawn over time.

## Queries

The queries on the engine, filterable by state and text, with their detail: SQL, user, timings and statistics. A query can be cancelled from here. That cannot be undone: it is stopped on the engine, and its user gets an error instead of the results.

## Table health

**Table health** lists the tables of source Iceberg catalogs. For each one it shows:
- **Size and files:** snapshots, data and delete files, size and commits per hour.
- **Freshness:** the oldest snapshot.
- **Maintenance:** what is due, its mode and the last maintenance.

A health pass refreshes these numbers on a schedule; **Analyze now** runs one for a catalog at once. Below the table, the maintenance runs can be filtered by trigger (automatic, manual, dry run) and operation.

## Table maintenance

Iceberg tables need upkeep: small files pile up, snapshots accumulate, and failed writes leave orphan files. Baleia maintains each table with its own **parameters**, because a batch table and a streaming (CDC) table need very different care. Set them in **Data**, in the table's **Maintenance** section.

### Mode

| Mode | What rounds do |
|---|---|
| Managed | Run the operations that are due on this table |
| Off | Skip the table; **Run now** still works |
| Maintained elsewhere | Never run anything: another system maintains the table, and Baleia only reports its health |

The **writer commit interval** tells Baleia how often the table's writer commits. Operations that delete files keep a margin above it, so they never race a write in progress.

### Operations

| Operation | What it does | Triggers |
|---|---|---|
| Optimize | Rewrites small files and folds in delete files | share of small files; number of delete files (Iceberg v2) |
| Optimize manifests | Rewrites the manifest files | number of manifests |
| Expire snapshots | Drops old snapshots; time travel to them stops working | number of snapshots; age of the oldest |
| Remove orphan files | Deletes files no snapshot references | none: it runs when its maximum interval passes |

Each operation also has:
- **Schedule:** a minimum interval (do not run more often), an optional maximum interval (run at least this often, even without a trigger), an optional time window and a timeout.
- **Conflict behavior:** what to do on a commit conflict, either retry with backoff (30 s, 2 min, 5 min) or give up and report.
- **Its own parameters:**
  - Optimize: target file size, a partition filter, files to skip by age, and a sort order.
  - Expire snapshots: retention and the snapshots to keep.
  - Remove orphan files: the age of the files.

New parameters start from what the table already declares in its Iceberg properties.

### Rounds

After each health pass, a round runs **at most one operation per table**:
1. Operations past their maximum interval go first.
2. Then come the triggered ones, in priority order: optimize, optimize manifests, expire snapshots, remove orphan files.

The table's **plan** shows what a round would do now and why: what fired, what is blocked and by what. **Dry run** records that plan without running it, and **Run now** runs the next due operation or the one you pick. The catalog's **Maintenance** tab (**Connections → Catalogs**) lists every table with parameters and its next step. The number of runs at once across catalogs is capped by `BALEIA_MAINTENANCE_MAX_CONCURRENT_RUNS`.

### Retention floor

Trino refuses retentions shorter than its catalog's minimum, which is 7 days by default. On catalogs Baleia manages, it sets that minimum to `BALEIA_ICEBERG_MAINTENANCE_MIN_RETENTION` (1 hour by default). The floor that applies is always the one the engine has loaded: until a catalog is reconciled after a change, its old floor applies. Adopted and native catalogs keep Trino's 7 days.
