# 0020. The metadata store runs on Bun, portably (PostgreSQL, MySQL, MariaDB)

- Status: Accepted
- Date: 2026-11-07
- Issues: #758, #754

## Context

Baleia's own store (users, workspaces, saved content, models, connections,
catalogs, audit, snapshots) is PostgreSQL-only: 80 goose migrations in
`db/migrations/*.sql`, sqlc-generated queries (`db/queries` → `internal/db/*.sql.go`)
and `pgx`/`pgxpool`. The assistant keeps a second sqlc package under
`internal/db/assistant`. An installer who runs MySQL or MariaDB cannot install
Baleia at all, even though the query-execution path (Trino and the direct
engines) does not depend on the store's SGBD.

The API already depends on `db.Querier` and per-domain `Store` interfaces (for
example `spaces.Store`), so the code has a seam to swap the data layer one
domain at a time.

The query set leans on PostgreSQL idioms that must be handled explicitly by any
portable data layer:

- `ON CONFLICT ... DO UPDATE` in 12 query files — the content-as-code upsert
  (FD16).
- `RETURNING` in 24 query files.
- `pg_trgm` (`%`, `similarity()`, `gin_trgm_ops`), array containment (`@>`),
  `jsonb` and `cardinality()` in the metadata search and related read models.
- `::uuid` / `::text[]` / `::timestamptz` casts and `sqlc.narg` / `sqlc.arg`
  placeholders across 26+ files.
- `LISTEN`/`NOTIFY` in monitoring — being replaced by the shared bus (#755), so
  it does not constrain this decision.

## Decision

- **Bun** (`github.com/uptrace/bun`) is the single data layer for Baleia's own
  store. Every domain moves to it: identity & access, workspaces & content,
  connections/engines/catalogs, semantic & lineage, monitoring & ops, and
  audit/agents/retrievers, including the assistant's tables. There is no second,
  PostgreSQL-only data path in the store.
- **Nothing survives.** `sqlc.yaml`, `internal/db/*.sql.go` (including
  `internal/db/assistant`), goose and `pgx` as the metadata store are removed.
  Connections go through `database/sql` with Bun's drivers. `pgx` remains only
  where it is not the store's business (none currently).
- **Supported backends: PostgreSQL, MySQL and MariaDB.** The installer picks the
  backend; Baleia runs on any of the three with the same code. Dialect
  differences are resolved by Bun or, where they cannot be, in the application.
- **Migrations** move from goose to `uptrace/bun/migrate`: Go migrations that use
  Bun's query builder emit dialect-specific DDL, so no separate migration tool is
  added.
- **PostgreSQL-specific patterns are rewritten portably**, not kept:
  - upserts via `OnConflict(...).DoUpdate()`;
  - `RETURNING` through Bun with the `LastInsertId` fallback the MySQL/MariaDB
    drivers require;
  - trigram search is replaced by a portable implementation, owned by the search
    epic (#798);
  - casts and `sqlc.narg`/`sqlc.arg` placeholders become Bun's query builder.
- **Migration order** (each domain behind its existing `Store` interface, with CI
  running PostgreSQL and MySQL/MariaDB for the migrated domain):
  1. identity & access (#769);
  2. workspaces & content (#770);
  3. connections/engines/catalogs (#771);
  4. semantic & lineage (#772);
  5. monitoring & ops (#773);
  6. audit/agents/retrievers (#774).

## Consequences

- **Easier:** one data layer and one set of models; installers on MySQL/MariaDB;
  dialect-aware migrations in the repository's own language instead of a separate
  tool; `database/sql` composes with the existing pool, tracing and transaction
  helpers.
- **Harder:** the trigram search must be reimplemented portably and loses its
  PostgreSQL index until #798 lands (meantime it is prefix/`ILIKE`); every
  `RETURNING`-dependent insert needs a per-backend test because MySQL/MariaDB do
  not support `RETURNING` uniformly; the `pgx.Tx`-based `WithTx` becomes a
  `database/sql` transaction; the team must learn Bun; the diff touches every
  handler group.
- **Must now be true:** no store SQL goes through `database/sql` outside Bun;
  the per-domain `Store` interfaces stay the seam; CI runs a MySQL/MariaDB job;
  the clean-up (#761) only deletes sqlc/goose after the last domain has migrated.

## Current compliance

Not implemented yet: the store is still goose + sqlc + pgx. This ADR is
implemented by #759 (the Bun migrator) and #769–#774 (the domains), followed by
#761 (remove sqlc/goose and update the Makefile/DoD) and #762 (document the
supported backends for installers). Until the last domain lands, sqlc and goose
stay in place for the domains that have not migrated.

## Alternatives considered

- **GORM.** Rejected: its native migration story is `AutoMigrate`, which keeps no
  versioned history, so versioned migrations need a second tool
  (golang-migrate/atlas); its `*gorm.DB` transaction model is a heavier shift from
  the current `pgx.Tx` wiring; and it is less directly composable with
  `database/sql` and the OpenTelemetry tracing already in place. Its larger
  community and maturity are real advantages, but they do not outweigh the fit for
  portable, dialect-aware migrations.
- **Prisma or ent.** Rejected: both bring a heavier schema-DSL/codegen or graph
  model than the "portable and light" requirement calls for.
- **Keep `pgx`/sqlc for the PostgreSQL-only paths.** Rejected by the owner: one
  data path for the store, and no PostgreSQL-only survivor.
- **MongoDB.** Out of scope: a document store would need a separate repository
  pattern and does not fit the relational store.
