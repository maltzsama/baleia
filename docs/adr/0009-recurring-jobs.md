# 0009. Recurring jobs run on Temporal Schedules

- Status: Accepted
- Date: 2026-10-14
- Issues: #9, #84, #85

## Context

Recurring work was registered as workflows that were never started, or as long-running loops with
sleeps. Loops grow history without bound, are hard to pause or inspect, and retries could re-run
statements with side effects.

## Decision

- Every recurring job runs on a **Temporal Schedule**, created idempotently at worker boot
  (`internal/temporal/schedules.Ensure`).
- Scheduled workflows are **single-pass**: they do one unit of work and return. No infinite-loop
  workflows.
- Overlapping runs are skipped (`SCHEDULE_OVERLAP_POLICY_SKIP`).
- **Statements are never auto-retried.** Activities that execute user or job SQL use
  `MaximumAttempts: 1` and heartbeats; cancellation kills the engine-side query (#84).

## Consequences

- Schedules can be paused, triggered and inspected in Temporal.
- A failed statement is visible as failed, never silently re-run.
- Non-statement activities (bookkeeping, uploads) may retry.

## Current compliance

Compliant since #84 and #85.

## Alternatives considered

- **Cron inside the API process.** Rejected: duplicates per replica and has no visibility.
- **Looping workflows with `workflow.Sleep`.** Rejected: unbounded history, no pause/inspect.
