# SQL dialect knowledge: where it lives and where it should live

Status: audit note / discussion artifact. Not a decision (see `docs/adr/` for
decisions). Written 2026-11-07, after adopting `sqlingo` in the editor (#756),
then revised after inspecting the committed function catalog.

## Why this document exists

Baleia carries a large amount of per-dialect SQL knowledge — keyword lists,
function catalogs, lexical rules, quoting, literal rendering, statement
splitting. This document inventories what exists today, and asks a sharper
question than "where should the files go":

> **Which SQL-related responsibilities should Baleia own, and which should be
> delegated to an existing SQL library or recovered from the engine at runtime?**

The goal is not to reduce line count or relocate files. It is to establish a
domain boundary: Baleia should be a *consumer* of SQL knowledge, not the place
where that knowledge is accumulated (the same principle as ADR 0001 — a control
plane, never a data plane — applied to SQL).

## Three responsibilities, not two

An earlier draft split "syntax vs metadata". A cleaner cut is three layers:

| Responsibility | Examples | Where it belongs |
|---|---|---|
| **SQL language knowledge** | grammar, keywords, function *names*, token types, quoting, literals, lexer, statement splitting, formatting | a SQL library (`sqlingo` / `sqlglot-go`) and a language mode (`@codemirror/lang-sql`) |
| **Engine capabilities** | function *availability*, signatures, descriptions, version-specific behavior, feature limits | introspected per engine; represented uniformly, not re-encoded |
| **Engine introspection** | schemas, tables, columns, installed functions, server version | Baleia / its connectors |

The middle layer is the crux: `array_agg` exists in the PostgreSQL *language*,
but its signature and availability vary by version. Baleia must not encode that
difference; it should *query* it through a uniform interface and cache the
result — exactly what it already does for tables and columns.

## Current inventory

### Frontend (`web/`)

- `web/src/lib/workbench/sqlingo.ts` (~147 lines): a lazy wrapper over `sqlingo`
  (TypeScript SQLGlot port). Maps Baleia dialect names to sqlingo dialects, and
  exposes `tokenize` / `statementSpans` / `formatSQL`.
- `web/src/lib/workbench/sqlContext.ts` (~330 lines): editor "glue" mapping
  tokens to what fits at the cursor (`statement`/`table`/`column`/`keyword`).
- `web/src/lib/workbench/sqlCompletion.ts`: metadata-driven completion sources.
  Function/keyword *options* come from the API (below), not from sqlingo.

Tokenization, splitting and formatting are already delegated to `sqlingo`.

### Backend (`internal/dialect/`)

Hand-written Go, ~1,459 non-test lines: `dialects.go` (394) per-dialect
quoting/types/literals/metadata queries, `base.go` (389) SQL generation, `lexer.go`
(293) the statement splitter, `dialect.go` (250), `functions.go` (79),
`lists.go` (54).

### Backend data (`internal/dialect/lists/`)

Committed, generated data, consumed **only** to serve the editor's autocomplete
(grep-verified; no backend logic reads it):

```
lists/*.txt            → embed (lists.go) → base.go Keywords()/Functions()
                                              ↓
                              handlers_dialects.go → GET /dialects/{name}
lists/*.functions.json → embed (functions.go) → FunctionReferences()
                                              ↓
                              handlers_dialects.go → GET /dialects/{name}/functions
                                              ↓
                              web sqlCompletion.ts (keywordOptions + functionOptions)
```

## The finding that reshapes the question

The `functions.json` files look huge (~33k lines), but that is pretty-printed
JSON — ~7 lines per entry. The real content, per dialect:

| dialect | entries | with `signature` | with `description` | `kind` |
|---|---|---|---|---|
| postgresql | 1,852 | **1,852** | **1,851** | aggregate, function, window |
| clickhouse | 1,548 | 0 | 0 | function |
| oracle | 1,279 | 0 | 0 | function |
| trino | 442 | 0 | 0 | function |
| mysql | 392 | 0 | 0 | function |
| mariadb | 268 | 0 | 0 | function |
| sqlite | 164 | 0 | 0 | function |

So the "rich catalog" is really:

- **~4,100 name-only entries** (every engine except PostgreSQL) — the generation
  queries return only names (`SHOW FUNCTIONS`, `v$sqlfn_metadata`,
  `system.functions`, `mysql.help_topic`, `pragma_function_list`). These names
  are **redundant with sqlingo / sqlglot-go's per-dialect function catalog**.
- **1,852 PostgreSQL entries** with real signatures and descriptions — the only
  engine that exposes them, via `pg_proc` + `obj_description()`.

The `kind` classification (aggregate / window / function) is also already
present in SQLGlot's catalog, not Baleia-specific.

**Conclusion: there is no precious catalog to relocate.** The committed snapshot
is the anti-pattern, not the data inside it. Function *names* and *kind* belong
to the SQL library; signatures/descriptions belong to runtime introspection
(like columns). Neither needs a committed, versioned blob — and therefore
neither needs a new independent library to own it.

## Per-bucket paths

### 1. Keywords → `@codemirror/lang-sql`

Contribute the missing dialects (Trino, ClickHouse, StarRocks, Doris, Oracle,
Dremio) as `SQLDialectSpec`s, complete keywords from `keywordCompletionSource`
on the client, delete `*.keywords.txt` and the `keywords` API field. Removes
~7k lines; no loss. Note some engines (Trino, SQLite, SQL Server) expose no
keyword catalog at all, so any committed list there is already a manual fallback.

### 2. Function names + kind → sqlingo / sqlglot-go

Complete function names (and aggregate/window classification) from the SQL
library's catalog. Delete `*.functions.txt` and every name-only `*.functions.json`
(clickhouse, oracle, trino, mysql, mariadb, sqlite). Removes ~30k lines; no loss.

### 3. PostgreSQL signature/description → runtime introspection

Introspect `pg_proc` + `obj_description()` (or the equivalent) per connection,
like tables/columns already are, and serve it through the metadata layer. This
drops the last committed snapshot and makes the catalog version-accurate instead
of pinned to "PostgreSQL 18.6".

If richer descriptions turn out not to be worth the introspection cost, the
fallback is to drop them and keep only names (sqlingo already offers static
signatures), which is a product decision — but the default should be
introspection, since Baleia already does this for every other metadata kind.

### 4. Backend Go logic → `sqlglot-go` (#625)

`dialects.go` + `base.go` + `lexer.go` hand-encode quoting, literals, and
statement splitting. `sqlglot-go` replaces those; the `information_schema`
metadata queries are Baleia-specific and stay. Already deferred (#625).

## What is removed vs. what is lost

| Bucket | Home | Removes | Lost |
|---|---|---|---|
| keywords | `@codemirror/lang-sql` | `*.keywords.txt` + API field | nothing |
| function names + kind | `sqlingo` / `sqlglot-go` | `*.functions.txt` + name-only JSONs | nothing |
| PostgreSQL signature/description | runtime introspection | the committed `*.functions.json` | nothing (if introspected) |
| Go logic | `sqlglot-go` (#625) | `dialects.go`/`base.go`/`lexer.go` | deferred; metadata queries stay |

Net: Baleia stops owning any committed per-dialect catalog. SQL knowledge is
delegated to the SQL libraries; engine capabilities are introspected at runtime.

## What Baleia keeps

- Connecting to engines and executing introspection queries.
- The metadata cache and its invalidation.
- Associating metadata with connections/workspaces.
- Resolving the completion context (editor glue).
- Exposing everything through the API.

These are the control plane's actual job; none of them is "knowing SQL".

## Open questions

1. Is the PostgreSQL function *description* worth introspecting at runtime, or
   do we drop it and rely on sqlingo's static signatures?
2. Do we contribute the missing dialects to `@codemirror/lang-sql` upstream, or
   carry a thin local dialect map? (Upstream contribution is preferred.)
3. For engines with no keyword catalog (Trino, SQLite, SQL Server), is the
   CodeMirror generic keyword set acceptable, or is a minimal hand-maintained
   list still justified?
