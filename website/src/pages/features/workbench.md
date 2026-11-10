---
title: "SQL workbench for Trino and your databases"
description: "A self-hosted SQL workbench for Trino, PostgreSQL, MySQL, SQL Server, Oracle and ClickHouse: autocomplete from your catalogs, scripts, history and exports."
---

# SQL workbench for Trino and your databases

The Baleia workbench is where you write and run SQL against Trino and the databases you connect directly: PostgreSQL, MySQL/MariaDB, SQL Server, Oracle, ClickHouse, SQLite and Arrow Flight SQL. It runs on your own infrastructure, next to your data.

## Write faster

- **Autocomplete from your catalogs.** Suggestions (statement keywords, tables, columns, clause keywords) come from the stored metadata of the tab's catalog, so they match what is actually there.
- **Join suggestions.** After `JOIN <table> ON`, autocomplete offers whole join conditions taken from joins your workspace has run before and from foreign keys.
- **Function reference, formatting and EXPLAIN.** Search the engine's functions and insert one at the cursor, format SQL for the engine's dialect, and read the engine's plan for the statement at the cursor.
- **Tabs that keep their target.** Each tab keeps its SQL and the engine it runs against.

## Run it and see where the time went

- **Statements and scripts.** Run the statement at the cursor, a selection, or the whole tab as a script: statements run in order in one engine session and stop at the first error.
- **Large results without a heavy browser.** Results are stored rather than held in the page, so you page through them, and export them as CSV, TSV, JSON Lines or Arrow.
- **Execution details.** See Baleia's queue, the engine's queue, planning and execution. On Trino it adds CPU, peak memory, spilled bytes, input and output, the tables read and the query profile.
- **Baleia never rewrites your query.** What a user may read or change is decided by the engine and the catalog manager.

## Save, share and keep the history

Saved queries live in team spaces and keep a version on every save, so you can compare or restore one; deleted queries go to a trash. If someone else saved the same query since you opened it, Baleia shows the conflict instead of overwriting their change. Your execution history is searchable by SQL and filterable by status, time and workload class.

## Try it

- [Read the workbench guide](/docs/guides/workbench)
- [Run Baleia locally in a few minutes](/docs/quick-start)
- [Open the live demo](https://demo.baleia.galileostd.io)
