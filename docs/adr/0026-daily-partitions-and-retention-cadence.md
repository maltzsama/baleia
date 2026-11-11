# 0026. Daily partitions, configurable retention and a weekly drop cadence

- Status: Proposed
- Date: 2026-11-11
- Issues: #958 (continues #948)
- Amends: [0024](0024-cold-storage-and-retention.md): the partition interval (monthly → daily), the
  retention defaults (400d / 180d → 45d), the enforcement cadence (daily → configurable, weekly by
  default), the offload unit (month → day) and the per-run drop cap. Everything else in 0024 stands:
  the `PartitionManager` seam, the partition keys and composite foreign keys, `(stream, sink)`
  cursors, the S3 cold sink, NDJSON gzip, and the fail-closed gate.

## Context

[0024](0024-cold-storage-and-retention.md) partitions `audit_events`, `query_executions` and
`query_statements` by month and drops a partition only after its month is offloaded. Store
migration 14 implements the monthly partitioning, and #955 implements the monthly offload
(`offload.MonthReader`, one object set and manifest per month) and the gated `retention.Retain`.
None of this has shipped in a release yet (the last one is v0.7.0), so this ADR changes migration 14
in place instead of adding a migration after it.

A monthly partition makes the retention window coarse. A partition can only be dropped once its
whole month is older than the window, so a 45-day window really keeps between 45 and 76 days of
data (plus grace). The database cannot hold the window it was configured with, and the
oldest-unpruned-partition alert has to tolerate a month of slack.

Two facts that bound the decision:

- **The partition width and the drop cadence are separate concerns.** The width sets how precisely
  the window can be held. The cadence sets how often DDL runs. 0024 coupled them implicitly (one
  month, daily runs that mostly did nothing).
- **Offload, retention and the per-run cap are written per month.** The manifest, the
  offload reader (`MonthReader`) and `BALEIA_PRUNE_MAX_PARTITIONS` (default `1`) all assume one
  partition is one month. A weekly run over daily partitions makes seven partitions droppable per week, so a cap of
  one would never catch up.

## Decision

1. **Partitions are daily.** `audit_events` (by `occurred_at`), `query_executions` (by
   `started_at`) and `query_statements` (by `execution_started_at`) are range-partitioned by UTC
   day. Partition names carry a `yyyymmdd` suffix: `audit_events_20261101`. The keys and the four
   composite foreign keys of 0024 do not change shape. They already carry the partition column; only
   the width of each range changes.

2. **The interval is a parameter of the seam, not a constant.** `partitioning.Interval` gains
   `Daily` next to `Monthly`, and the month-only helpers (`RangeMonths`, `MonthStart`,
   `NextMonth`) are generalized to helpers that take the interval. A third width (for example hourly)
   is then one more `Interval` value, not another refactor. `Provision`, `ListPartitions` and
   `DropPartition` keep their signatures (`from`, `until`). Every adapter, including the SQLite
   emulation, supports both intervals.

3. **Retention is configured per stream, with a 45-day default for both.** `audit` and
   `query_history` keep separate settings, as in 0024. Both now default to `45d`. A window shorter
   than one day is rejected at startup. Today's partition and later ones are never dropped.

4. **The drop cadence is configured per stream and defaults to weekly.** Each retention schedule
   (`audit-retention`, `query-history-retention`) runs on its configured cadence: `weekly` (the
   default, Sunday 03:00 UTC), `daily` (03:00 UTC) or a five-field cron expression. These are Temporal
   Schedules ([0009](0009-recurring-jobs.md)); `schedules.Spec` gains a calendar form next to
   `Every`, since a fixed interval cannot pin a weekday and an hour. The window is evaluated when a run starts. With the
   defaults, a run drops the days that crossed the window since the previous run, so the oldest day
   in the database is between 45 and 51 days old (plus grace). An operator who needs the exact window
   sets `daily`.

5. **The offload unit is a day.** `MonthReader` becomes a `PeriodReader` that reads one UTC day,
   and the offloader works one day at a time per stream. It writes
   that day's NDJSON gzip objects as `<prefix>/<stream>/<yyyy>/<mm>/<dd>/<object>-<yyyymmdd>-<hash>.ndjson.gz`, then
   that day's manifest (keys, counts, checksums, stream, day). Only after that does it advance the
   `(stream, sink)` cursor past the day. The cursor is already a timestamp, so no schema change is
   needed. The query-history pair rule of 0024 (an execution and its statements are one unit) applies
   per day. The per-month manifest of 0024 becomes a per-day manifest, about 365 per stream per year.

6. **The gate is unchanged in substance, and it applies per day.** A daily partition `D` is dropped
   only when all of these hold:
   1. `now ≥ end_of_day(D) + retention + grace`;
   2. the cold-sink cursor for the stream is `≥ end_of_day(D)`;
   3. the manifest for `D` exists and validates (object count and checksums).

   If (2) or (3) fails, the partition stays and the run records it as blocked, with a reason.
   `BALEIA_*_COLD_GRACE` keeps its meaning (late events, clock skew), and its default drops from
   `7d` to `1d` because the partition it protects is now a day wide.

7. **Retention runs are idempotent.** A run lists the existing partitions through the
   `PartitionManager` and drops only those that are present and pass the gate. A second run on the
   same day, or a run after a failed one, finds nothing more to drop and does not fail.

8. **The per-run cap is sized for the cadence.** `BALEIA_PRUNE_MAX_PARTITIONS` now counts days,
   and its default goes from `1` to `14`. That is two weeks of daily partitions: a weekly run keeps up
   and still drains a week of backlog. Shortening the window still turns into a controlled drain,
   as in 0024, and a backlog above the cap still emits a warning.

9. **Offload before drop stays the default, and opting out is explicit and loud.**
   `BALEIA_*_OFFLOAD_BEFORE_DROP` defaults to `true`, which is the fail-closed gate above. Setting
   it to `false` drops partitions on the age condition alone. It exists for deployments that keep no
   cold copy on purpose. When it is `false`, the worker logs a warning at startup and every dropped
   partition writes an audit event naming the stream, the day and the fact that no offload preceded
   it. With the default, an unconfigured cold sink still blocks every drop, as in 0024.

10. **The provisioning horizon is independent of retention.** Provisioning looks forward and
    retention looks back, so the horizon depends on how far the provisioning schedule can fall
    behind, not on the window. `audit-partitions` stays daily and provisions
    `BALEIA_PARTITION_DAYS_AHEAD` days past today (default `14`). That is enough for the schedule to
    miss a week of runs before an insert finds no partition. It replaces `partitionMonthsAhead = 3`.

11. **Migration 14 creates daily partitions; there is no monthly-to-daily migration.** Migration 14
    has not shipped, so it is edited in place: it converts each plain table exactly as it does today
    (set the plain table aside, create the partitioned table from the model, provision from the oldest
    row through the horizon, copy the rows through the ORM with their ids, sync the id sequence, drop
    the legacy table), but with `Daily` partitions and a horizon of 14 days, the default of
    `BALEIA_PARTITION_DAYS_AHEAD`. The conversion gains one step before the legacy table is dropped:
    it validates the copy per day (row count and a checksum over the ids against the legacy table).
    A failed validation aborts that table's conversion and keeps its rows. The migration asks
    `Capabilities()`, never the dialect name. A development database that already ran the monthly
    migration 14 is recreated.

    **The migration runs in stages, each its own transaction:** the preparation (the parent's
    timestamp column, its backfill, dropping the old foreign keys), one conversion per table, and one
    transaction per new foreign key. One partition per day of history multiplies the relations a
    transaction locks, and a single transaction over a year of history exhausted PostgreSQL's lock
    table (`max_locks_per_transaction`, 64 by default): 250 days converted and 300 failed. Staged,
    the same defaults convert about 800 days and fail at 1100. Each table converts atomically, so a
    failed run leaves whole tables converted or untouched, and the stages are idempotent, so a rerun
    continues. The runner still marks the migration dirty on failure, and the operator clears it. A
    database with more history than the lock table holds must raise `max_locks_per_transaction`
    before upgrading; `docs/operations.md` says so.

    No monthly cold objects or manifests exist in any release, so the daily offloader starts from the
    `(stream, sink)` cursor as it is.

## Configuration

Per stream (`AUDIT`, `QUERY_HISTORY`), replacing the 0024 defaults where they differ:

| Setting | Default | Meaning |
|---|---|---|
| `BALEIA_<STREAM>_RETENTION` | `45d` | Hot window; at least `1d` |
| `BALEIA_<STREAM>_RETENTION_CADENCE` | `weekly` | `daily`, `weekly` or a five-field cron (UTC) |
| `BALEIA_<STREAM>_OFFLOAD_BEFORE_DROP` | `true` | `false` drops without a cold copy (warned, audited) |
| `BALEIA_<STREAM>_COLD_GRACE` | `1d` | Margin past the window before a day is droppable |
| `BALEIA_PRUNE_MAX_PARTITIONS` | `14` | Days dropped per run, per stream |
| `BALEIA_PARTITION_DAYS_AHEAD` | `14` | Days past today that `audit-partitions` keeps provisioned |

The cold bucket and prefix settings of 0024 are unchanged.

## Consequences

**Positive**

- The window held is the configured one, give or take the cadence and a day of grace, instead of
  up to a month more.
- How often DDL runs is an operator's choice that does not affect the partition layout.
- A failed offload blocks one day, not a month. A recovery re-runs one day's objects.

**Negative**

- About 50 partitions per table inside a 45-day window, plus the provisioned horizon, instead of
  2–3. PostgreSQL planning time grows with the partition count, but stays small at this size.
  Queries that filter on the partition column prune to a few days.
- About 365 manifests per stream per year instead of 12. Listing the cold store by day is more
  verbose. The keys are still deterministic from `(stream, prefix, day)`.
- Migration 14 copies the three tables into their partitioned form on PostgreSQL. It is a copy of
  the hot data, which is bounded once retention is running, but on a deployment that has never
  pruned it is the full history.
- Daily partitions put a ceiling on the history migration 14 can convert with PostgreSQL's default
  lock table (about two years of dense history, see decision 11). Monthly partitions would have
  had none.
- A deployment upgrading from 0.7.0 holds unbounded history and gets a 45-day window. With a weekly
  cadence and a cap of 14 days per run, the net drain is 7 days per week, so reaching the window
  takes about as many weeks as the history has days beyond it divided by 7. The default is kept; the
  upgrade notes state the expected catch-up time.
- A 45-day audit default is far shorter than 0024's 400 days. Audit beyond 45 days lives only in
  cold storage, so the bucket's lifecycle and legal-hold policy (owned by operations, see 0024) is
  now what keeps the audit trail for compliance purposes.

**Risks**

- `OFFLOAD_BEFORE_DROP=false` reintroduces data loss by configuration. The warning and the audit
  event make it visible. The default never loses data.
- With a weekly cadence, a failed run delays reclamation by a week. The oldest-unpruned-partition
  alert of 0024 now fires at `retention + grace + cadence + 1d`. Neither the metric nor the alert exists yet:
  the rollout adds both.

## Current compliance

Implemented by the first pull request (#958): `Daily` in the `PartitionManager` seam and every
adapter (decisions 1 and 2), migration 14 creating daily partitions in validated stages with
`BALEIA_PARTITION_DAYS_AHEAD` (10 and 11), the daily offload (`PeriodReader`, day keys and
manifests, decision 5) and the gate that validates the day's manifest (6 and 7).

Not implemented yet, left to the second pull request: the configurable retention and its 45-day
default (3), the drop cadence and the calendar form of `schedules.Spec` (4), the per-run cap of 14
(8), the `OFFLOAD_BEFORE_DROP` opt-out (9), the `1d` grace default, and the oldest-unpruned-partition
metric and alert. Until then `audit-retention` and `query-history-retention` run every 24 hours
with the 0024 defaults (`400d`, `180d`, `7d` grace, a cap of `1`).

## Rollout

Two pull requests, in order, both before 0.8.0 is cut.

1. **Daily layout, end to end.** Generalize the interval in `internal/persistence/partitioning`
   (`Daily`, interval-aware range helpers, partition names parsed by suffix width) with DB-backed
   tests per adapter. Edit migration 14 to create daily partitions with per-day copy validation, and
   switch `audit-partitions` to `BALEIA_PARTITION_DAYS_AHEAD`. Switch the offload to days
   (`PeriodReader`, daily keys and manifests) and the retention gate to daily manifests that are
   validated, not only checked for existence. `migrate -cmd=audit-prune -before` takes a day.
   Update `docs/operations.md`.
2. **Cadence and configuration.** Add the calendar form to `schedules.Spec`, the cadence and
   opt-out settings, the new defaults and the cap, and move `audit-retention` and
   `query-history-retention` to the cadence. Add the oldest-unpruned-partition metric and alert.
   Update `docs/configuration.md`.

## Alternatives considered

- **Keep monthly partitions.** Rejected: the window held is 45–76 days for a 45-day setting, and
  every failed offload blocks a month.
- **Tie the drop cadence to the partition width** (daily partitions dropped daily, as 0024's daily
  schedule does). Rejected as a fixed rule: it runs DDL every day for a precision most deployments
  do not need. It remains available as `cadence=daily`.
- **Mixed widths: keep existing monthly partitions and add daily ones after a cutover.** PostgreSQL
  allows ranges of different widths. Rejected: with migration 14 unreleased there are no existing
  months to keep, and every adapter, the SQLite registry, the manifest and the gate would handle
  two widths for nothing.
- **Ship monthly partitions in 0.8.0 and move to daily in a later migration.** Rejected: it rewrites
  the three tables twice, needs a per-engine re-interval path (`REORGANIZE`, `SPLIT`, `SPLIT RANGE`)
  and leaves monthly objects and manifests in cold storage for good. Editing the unreleased
  migration 14 avoids all three.
- **Derive the provisioning horizon from the retention window** (`window + buffer`). Rejected:
  provisioning creates future partitions and retention drops past ones. Their only shared
  constraint is that the provisioning schedule must stay ahead of time, which the horizon in days
  expresses directly.
- **Remove the opt-out and always require offload.** Rejected: deployments that keep no cold copy
  on purpose would have no supported way to reclaim space and would fall back to manual drops,
  which bypass the gate entirely.

## References

- [0024](0024-cold-storage-and-retention.md): cold storage and retention, amended here.
- [0020](0020-gorm-store.md): GORM store and the partitioning carve-out.
- [0009](0009-recurring-jobs.md): Temporal Schedules.
- `internal/persistence/partitioning`, `internal/store/repartition.go` (migration 14).
