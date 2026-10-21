# 0012. Semantic layer roadmap (no dbt)

- Status: Accepted (owner decision D9)
- Date: 2026-10-14
- Issues: #9, #14, #33, #91, #115, #116

## Context

Users need documented tables and reusable metrics. A full semantic layer (models, joins, a compiler
per dialect) is large, and adopting dbt's project format would tie Baleia to dbt's tooling and
release cycle.

## Decision

- **v1 (#33):** table and column documentation, tags, owners, certification, and **simple metrics**:
  one aggregate expression over one table, compiled per dialect (#14).
- **v2 (#91, deferred):** a semantic layer at Lightdash's level: models, dimensions, metrics, joins,
  calculated fields, and a compiler per dialect.
- **No dbt in any version:** no dbt integration, no `schema.yml` parser, no dbt-compatible project
  format.
- v1 metrics migrate into v2 models through a documented migration.

## Consequences

- Dialects must expose SQL-generation primitives from v1 (#14).
- Teams with dbt projects keep them; Baleia does not read them.

## Current compliance

`internal/dialect` has the generation primitives for every v1 dialect (`Aggregate`, `TimeTrunc`,
`Cast`, `SafeDivide`, `Literal`, `Limit`) with golden tests (#14). `internal/semantic` renders
simple metrics per dialect, and the API validates them with a zero-row probe in a read-only engine
session (#33). Docs, tags and owners live in `semantic_docs`, keyed by the table's natural key so
they survive a metadata resync. Spaces (#40) and versions and bundles (#34) are in place. The v2 semantic layer is in
place too: models, joins and the per-dialect compiler (#91), with the Explore UI and the model
editor on top (#121).

## Alternatives considered

- **dbt integration.** Rejected by the owner.
- **Full semantic layer in v1.** Rejected: too large before engines and content exist.
