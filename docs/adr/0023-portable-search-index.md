# 0023. Search reads a portable term index rebuilt per scope

- Status: Accepted
- Date: 2026-11-10
- Issues: #798, #942

## Context

In-product search (metadata, docs and content) used to load every visible row
of a workspace on each request, then match and score in Go. That was portable
(ADR 0020: no `pg_trgm`, no `tsvector`) but each keystroke of the sidebar or
the palette read every table, column, doc, saved query, metric and model the
workspace can see. A lakehouse catalog holds tens of thousands of columns.

The matching itself (`internal/search`: case and accent folding, words of
`snake_case` and `camelCase` names, singular forms, tiers and a trigram fuzzy
fallback) is settled by #941 and measured by the golden set. What is missing is
a way to find the few rows worth scoring without reading all of them, on
PostgreSQL, MySQL and MariaDB alike.

The rows come from many write paths: the metadata sync, doc upserts, content
creates, renames, moves between folders and spaces, certification, the trash.
New write paths keep appearing.

## Decision

1. **A term index in two GORM tables.** `search_scopes` holds one row per
   indexed scope: a catalog's metadata (`metadata:<catalog>`), a workspace's
   docs (`docs:<workspace>`) and a workspace's content
   (`content:<workspace>`), with the fingerprint of the source state it was
   built from. `search_terms` holds `(scope, term, kind, ref)`: every folded
   word of a row's searchable texts, and its singular forms, pointing at the
   row. Lookups are `term = ?` and `term LIKE 'prefix%'` on the primary key
   `(scope_key, term, kind, ref_id)`. On PostgreSQL the term column is a custom
   GORM type (`model.Term`, collation `C`), because a database collated
   `en_US` cannot serve `LIKE 'prefix%'` from a btree index (300,000 terms: a
   sequential scan of 17 ms against an index-only scan of 1 ms); MySQL and
   MariaDB serve it from the primary key as is. Deleting a catalog or a
   workspace cascades to its scopes and terms. No handwritten SQL is involved
   (ADR 0020): the index is queried and built with GORM's API only.
2. **The index is rebuilt per scope when its source changes, not maintained
   by each writer.** Before a search, each scope's fingerprint is computed
   from cheap aggregates of its source, read through GORM's `Count` and
   ordered `Pluck` (metadata: live table count, latest `last_changed_at` and
   latest `removed_at`; docs and content: row counts and latest `updated_at`).
   Checking a fresh scope is two plain reads and takes no lock, so concurrent
   searches do not queue on it. A scope whose fingerprint differs is rebuilt
   once per process (the searches of that scope wait on the rebuild without
   holding a database connection, and the rebuild is detached from the
   request that started it) and, across replicas, under a row lock on its
   `search_scopes` row that the others find fresh once they get it. The
   metadata sync rebuilds its catalog's scope as soon as a pass finishes, so
   searches rarely pay for a rebuild. No write path has to remember the index.
3. **The index only proposes candidates; it never decides what a user sees.**
   The candidate rows are read from their live tables with the same
   predicates as before (catalogs granted to the workspace, live tables, the
   spaces the user can see) and scored on their live values. A stale index can
   miss or misorder a hit until the next rebuild; it cannot leak one.
4. **Fuzzy matching runs on the vocabulary, not on the rows.** When the exact
   and prefix lookups find fewer candidates than the limit, the terms of the
   scopes that share a query word's first two letters are compared by trigram
   similarity in Go, and the close terms add their rows as candidates.

## Consequences

- A search reads a bounded set of rows: the candidates of its terms, capped
  at 5,000 per lookup. On a catalog of 20,000 tables and 100,000 columns
  (PostgreSQL) a search that used to take about 1 s (every table and column
  loaded) takes 0.2 to 0.4 s, and 1 s when the word matches every table.
- Rebuilding a scope still reads the whole scope, once per change: about 12 s
  for that catalog. The metadata sync pays it when a pass finishes; the first
  search of a catalog that no pass has indexed yet (right after an upgrade)
  pays it inside the request.
- Matching a word inside another word (`rder` in `orders`) is no longer
  possible: lookups are by whole word or word prefix. Words of `snake_case`
  and `camelCase` names are separate terms, so the common cases remain.
- A misspelling of a word's first two letters is not found by the fuzzy
  fallback.
- Folders are few and are matched live, not indexed.
- Every new searchable kind adds its texts to a scope builder and its source
  to that scope's fingerprint.

## Current compliance

Metadata, docs and content search read the index (#942). The federated endpoint
and the glossary are #943.

## Alternatives considered

- **`pg_trgm` or `tsvector` indexes.** PostgreSQL only; rejected by ADR 0020.
- **Each write path updates the index in its transaction.** Exact, but about
  fifteen write paths today and more to come; one forgotten path leaves the
  index silently stale, and cascading deletes need their own handling.
- **An external search engine.** Another stateful component to deploy and
  keep consistent, for a corpus that fits in the metadata store.
- **Keep scanning every row, but cache it.** Moves the memory to every
  replica and still reads everything after each change.
