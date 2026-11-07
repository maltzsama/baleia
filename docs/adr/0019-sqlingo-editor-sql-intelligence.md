# 0019. sqlingo is the editor's SQL intelligence; the server keeps its own parser

- Status: Proposed
- Date: 2026-11-07
- Issues: #756

## Context

The workbench's autocomplete and statement context are hand-written TypeScript
(`web/src/lib/workbench/sqlContext.ts` lexes a small, union-of-dialects subset;
`web/src/lib/workbench/sqlCompletion.ts` fills it by prefix). It only knows a
small subset of each engine's grammar and drifts from the real grammars. The
statement under the cursor is split by a round-trip to
`POST /dialects/{name}/split` on every Run. Formatting uses `sql-formatter`,
which is a separate, non-AST formatter.

`sqlingo` is a TypeScript port of SQLGlot (parse + transpile + optimize, 32
dialects including Trino/Presto/StarRocks/Doris/Dremio, MIT, runs in the
browser). It gives the editor a real, dialect-aware tokenizer, parser and
formatter with no server round-trip.

## Decision

- The **editor** uses `sqlingo` for its SQL intelligence: statement context,
  autocomplete, formatting and the client-side split of the statement under the
  cursor. `sqlContext.ts`/`sqlCompletion.ts` are reduced to glue over sqlingo's
  tokenizer; the metadata-driven completion sources (tables, columns, joins,
  functions) stay, because they answer "what exists in this catalog", not "what
  does the grammar say here".
- **Statement splitting moves to the client.** The editor no longer calls
  `POST /dialects/{name}/split` to run the statement under the cursor. The
  endpoint and the Temporal `SplitSQL` activity stay: they are the execution
  path's authoritative split, shared by authorization, classification and
  lineage.
- The **backend keeps its own Go parser** (`internal/dialect`) for
  authorization, classification and lineage. `sqlingo` is the editor, never the
  server. A Go AST fork (the `sqlglot-go` idea of #625) is a separate, deferred
  concern and must not be referenced from the editor.
- The boundary is therefore: **client** parses for editor UX (completion
  context, formatting, choosing the statement to run); **server** parses for
  correctness and safety (splitting what actually executes, classifying it,
  building lineage). The two may differ on dialect edge cases; that is accepted
  because the server remains the authority for what runs.
- `sqlingo` is a single-maintainer, still-in-alpha port. We depend on it behind
  a thin, lazy-loaded wrapper and pin coverage tests for every dialect Baleia
  exposes, so a regression in the port or a future swap is contained in one
  module.

## Consequences

- The editor's completion and split are accurate per dialect without a
  round-trip, and formatting shares the same AST.
- A new runtime dependency (`sqlingo`, ~15 MB unpacked, plus its `luxon` peer)
  must stay out of the initial bundle: everything loads lazily and is cached per
  dialect.
- Unterminated input (string, comment, quoted identifier) is reported by
  sqlingo as a tokenization error: the editor completes nothing while one is
  open, and Run falls back to the whole script rather than half-running it.
- "Run current statement" may disagree with the server's split on a dialect edge
  case; a follow-up may reconcile the two, but the server path never changes to
  match the client.

## Current compliance

This ADR is proposed alongside the adoption in #756; until that PR lands the
editor still uses the hand-written lexer and the server split.

## Alternatives considered

- **Keep the hand-written lexer and server split.** Rejected: it keeps drifting
  from the engine grammars and needs a round-trip for every Run.
- **Use `sql-formatter` plus the hand-written context.** Rejected: two separate,
  non-AST tools instead of one dialect-aware parser.
- **Adopt the `sqlglot-go` fork on the backend now (#625).** Rejected: the fork
  is not mature enough to adopt; it stays out of scope for the editor.
