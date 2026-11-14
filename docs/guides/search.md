---
description: "Search Baleia for tables, columns, docs, saved queries, metrics and models in one ranked list, from the command palette, the sidebar, the API and agents, and teach it your team's synonyms."
---

# Search

One search finds what you can see in a workspace: **tables**, **columns**,
**docs**, **saved queries**, **metrics**, **models** and **folders**. It returns
one list, best match first, with how many matches each kind has.

You use it from:

- the **command palette** of the [workbench](workbench.md) (⌘/Ctrl + K): a table or column is inserted at the cursor, a metric inserts its
  SQL, a saved query opens in a tab, and anything else opens its page;
- the **search box of the sidebar**, for tables and columns;
- `GET /workspaces/{id}/search` in the API (`q`, `kind`, `catalog_id`,
  `space_id`, `limit`);
- `GET /workspaces/{id}/metadata/search`, for tables and columns only. With
  `match=prefix` it returns just the names that start with `q` and forgives no
  typo, which is what the editor's autocomplete asks for;
- the **`search` tool** of the [MCP server](../mcp.md), for agents.

Each surface also keeps its own endpoint (`/metadata/search`,
`/semantic/docs/search`, `/content/search`) for callers that need that
surface's filters. The sidebar still uses the metadata search when you filter by
tag, certification or namespace.

## What matches

You type words, not exact names:

- **Case and accents do not matter**: `REGIAO` finds `Região`.
- **Names are split into words**: `lifetime value` finds
  `customerLifetimeValue` and `customer_lifetime_value`.
- **Plurals and singulars match**: `sessao` finds `sessões_web`,
  `categories` finds `product_category`.
- **The start of a word matches**: `ord` finds `orders`.
- **Small typos are forgiven**: `custmer` finds `customers`.
- **Comments and descriptions count**: a word of a table's or column's comment,
  of a doc's description or tags, of a metric's or model's description or of the
  fields it mentions finds it, as does a word of a saved query's SQL.

Better matches rank first: the exact name, then names that start with what you
typed, then names that contain all its words, then partial and misspelled
ones. A match in a name beats the same match in a comment. Among saved content,
certified, your own and recently changed items rank a little higher; among
tables, the most-read ones.

It does **not** match a fragment inside a word (`rder` in `orders`), and a typo
in the first two letters of a word is not forgiven. Search reads the stored
metadata of your catalogs, never the live engine, so a table appears after the
next metadata sync.

You only find what you may see: tables of catalogs granted to the workspace, and
content of spaces you can open.

## The glossary

Teams call the same thing by different names. **Models → Glossary**
holds sets of words that mean the same in the workspace, for example
`receita` = `faturamento`, `revenue`. Searching for one of them also looks for
the others, in the palette, the sidebar (for plain text) and the `search` tool,
and the response lists the synonyms it added (`expanded_with`). What you typed
always ranks above what a synonym found.

Everyone in the workspace reads the glossary; editors and admins change it. An
entry has one term and up to 20 synonyms, compared without case or accents, and
a term has one entry per workspace.

## Operating it

Search keeps a term index in the Baleia database (tables `search_scopes` and
`search_terms`, [ADR 0023](../adr/0023-portable-search-index.md)). A catalog's
index is rebuilt when a metadata sync pass finishes; a workspace's docs and
content are rebuilt when they change. The first search of a catalog that no pass
has indexed yet, for example right after an upgrade, builds it inside the
request: about 12 seconds for 100,000 columns, once.

Each Baleia process keeps in memory the terms its typo-tolerant searches compare
against, by catalog or workspace and by the first two letters of the word, and
reads them again only when the index of that catalog or workspace was rebuilt.

`baleia_search_requests_total` counts searches by surface and by whether they
found anything; the zero-result rate is in the
[metrics catalog](../operations.md#metrics-catalog).

## For contributors

The golden set in `internal/api/testdata/search_golden.json` holds the queries
people type and the results they expect. `TestSearchGolden` runs them through the
real handlers, reports recall@10 and the zero-result rate and fails when either
is worse than `search_golden_baseline.json`. Change the matcher, run
`BALEIA_SEARCH_GOLDEN_UPDATE=1 go test ./internal/api -run TestSearchGolden`,
and commit the new baseline with the change.
