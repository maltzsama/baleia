# 0014. Workload management: per-engine admission queues

- Status: Accepted
- Date: 2026-11-01
- Issues: #399, #525–#562

## Context

Every Run goes straight to its engine. Ten analysts at once means ten concurrent
queries, and a heavy report, a retriever refresh and a 50 ms table preview compete
as equals. On the direct engines (PostgreSQL, MySQL, SQL Server, Oracle,
ClickHouse, SQLite, Flight SQL) nothing protects the source database from Baleia's
own load. All workflows share one Temporal task queue, so Temporal never backs up
either.

Baleia is a control plane ([0001](0001-control-plane-only.md)) and is multi-engine
([0006](0006-multi-engine-scope.md)). It decides **when** a run enters an engine and
**how it is tagged** there; it never schedules CPU or memory inside an engine.

## Decision

- Work is classified into three **global classes**: `interactive`, `heavy` and
  `background`. Policy speaks in classes; **capacity is per engine**.
- A queue is one `(engine_id, class)` pair. A Trino run never waits for a slot on
  another engine.
- Admission is **one entity workflow per engine**, `admission-<engine_id>`, using
  the Temporal semaphore pattern. It owns the per-class queues, the running set,
  the leases and the optional engine ceiling (`engines.max_running_total`).
- `QueryExecutionWorkflow` requests a slot before executing and releases it on
  completion, failure, cancellation and the heartbeat-loss (`interrupted`) path.
  The slot is held for the whole execution because every statement shares one
  engine session ([#27](https://github.com/maltzsama/baleia/issues/27)).
- Classification happens in the API, in this order: origin, admin rules
  (`workload_rules`), opt-in cost routing, default, and an optional user downgrade.
  The chosen class and the reason are stored on the execution.
- A queued run has a `queued` status, a visible position (only its own), can be
  cancelled, and times out with a stable `queue_timeout` error. `queue_full` when
  `max_queued` is reached.
- Capacity `0` closes a class on an engine. A missing capacity row means a
  conservative default per engine kind.
- Within a class, slots are shared across workspaces by weight, and a queued run's
  effective priority rises with its wait so `background` cannot starve.
- **Phase 1 tags only**: the class reaches the engine where a native mechanism
  exists (Trino `baleia.class` client tag and `query_priority`, ClickHouse
  `priority`, SQL Server application name, Oracle module). Baleia manages Trino
  resource groups only later, from the same capacities and rules.
- The first release is behind `BALEIA_WORKLOAD_ENABLED` (default `false`). When it
  is off, executions start exactly as today and no run is ever `queued`.
- No preemption in v1: an admin cannot kill a running query to free a slot. Killing
  a query stays a separate, explicit action.

## Consequences

- The execution lifecycle gains `queued` and the WebSocket must treat it as
  non-terminal.
- The API owns classification; the per-engine workflow serializes admission, so
  multiple API replicas do not change the result.
- Admission adds a signal round-trip before every run. Previews and introspection
  must stay under the latency budget measured in #399; a fast path is a later
  decision, not a v1 requirement.
- A worker crash must release the slot through the existing heartbeat timeout;
  a lease without a release is reclaimed after `execution_timeout` plus a grace.
- Temporal becomes part of the query path, so its server version is pinned (#525).
- New metrics and alerts describe queue depth, running count and wait, labelled
  only by engine and class.

## Current compliance

Not implemented yet. The child issues under #399 implement it in order: the queued
state and capacities (#528, #535), classification (#536), the admission workflow
(#537, #541), the queue API and UI (#540, #543), metrics (#544) and the E2E (#545);
rules, fairness and settings follow in #529, #539, #546, #547.

## Managed Trino resource groups (A-5, #553)

Baleia can generate Trino's `resource-groups.json` from the same capacities and
rules it uses for its own admission, through Trino's `db` configuration
manager. The goal is that Trino's queue becomes the one that holds Baleia's
Trino runs, so the same run is never queued twice.

**Double queueing is avoided by an explicit handover, per engine:**

- While managed resource groups are off (today), Baleia's admission workflow is
  the queue for every engine, and Trino only sees the class tag.
- When an admin turns managed resource groups on for an engine, Baleia stops
  granting slots from its own per-class queue for that engine: the admission
  workflow forwards the run to Trino and lets Trino's resource group hold it.
  Baleia keeps only fairness across workspaces and the per-agent limit, which
  Trino cannot express.
- The switch is per engine and reversible without a restart. Turning it off
  makes Baleia's queue the authority again. A run never waits in both queues
  at once.

The generated groups mirror the API's capacities (`hardConcurrencyLimit`,
`maxQueued`, `softMemoryLimit`) and the rules' classes; selectors match
`baleia.class`. The configuration manager reloads without restarting Trino.

The breakdown is tracked as #598 (generate and validate the document), #599
(configure and reload), #600 (hand over admission) and #601 (flag, docs, E2E
and rollback).

## Alternatives considered

- **A Postgres queue** (`execution_queue`, `FOR UPDATE SKIP LOCKED`, leases with
  heartbeats). Simpler to query, but it re-implements the leases, locking and crash
  recovery Temporal already provides, and it duplicates the workflow lifecycle.
- **Temporal task-queue priority and fairness** (`temporal.Priority`). Less code,
  but it is marked experimental, depends on the server version and dynamic config,
  and slots would be per worker process rather than per engine cluster-wide. It is
  worth revisiting once it is stable.
- **One global semaphore** (the Dremio OSS shape). Rejected: it cannot express that
  every engine has its own capacity, and a slow engine would starve the others.
- **Limits inside each engine** (resource groups alone). Rejected as the only
  mechanism: several engines have none, and Baleia must remain a control plane.
