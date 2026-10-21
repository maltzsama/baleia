# 0013. Saved content model

- Status: Accepted
- Date: 2026-10-14
- Issues: #9, #34, #40

## Context

Saved queries exist today; metrics, models, charts and dashboards will follow. If each kind invents
its own ownership, versioning and export, every feature (spaces, history, trash, moving content
between instances) is built once per kind.

## Decision

- Saved content is **one family**: saved queries, metrics and later models, charts and dashboards.
- Every item belongs to a **space** (#40), has a portable **slug**, and is **versioned
  append-only** (#34).
- Content moves between instances through deterministic **bundles** with upsert-by-slug semantics.
  Each item reports `CREATE`, `UPDATE`, `NO_CHANGES` or `ERROR`; items omitted from a bundle are
  never deleted.
- New kinds register in `internal/content` without schema changes to spaces, versions or bundles.

## Consequences

- Spaces, history, trash and import/export work for every kind once.
- Slugs must be unique per space and stable across renames.

## Current compliance

Spaces, slugs and `space_id` ownership are in place (#40); saved queries and semantic metrics are
content items with portable slugs. Content versions, trash/restore and the `baleia.bundle`
export/import (content-as-code) are in place (#34); semantic models are a content kind with versions, trash and bundles (#91), and the Explore UI
and model editor read them through the kind registry (#121).

## Alternatives considered

- **Per-kind tables and endpoints for history and export.** Rejected: repeated work and drift.
- **Bundles that delete omitted items.** Rejected: a partial bundle would destroy content.
