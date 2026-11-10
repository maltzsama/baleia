---
title: "Data catalog and explorer for Iceberg and your databases"
description: "Browse every Iceberg catalog and database in one place from stored metadata: typo-tolerant search, documentation, tags, owners and certified tables."
---

# Data catalog and explorer for Iceberg and your databases

The Baleia data explorer browses every catalog your workspace was granted, from Baleia's stored metadata: it does not query your engines while you browse. Register Polaris, Nessie, Lakekeeper, Iceberg REST, Unity, AWS Glue or Hive Metastore, and the databases you connect directly, and find everything in one tree.

## Browse and share

- **Catalog, schema, table.** Pick a table to see its kind, qualified name (with a copy button), when its metadata was last synced, its columns and its properties.
- **Preview and insert.** Run a 100-row preview in the workbench, or put the qualified name in the editor.
- **Links that reproduce the view.** The catalog, schema and table are in the URL, so a link opens the same table for a teammate.
- **Popular tables.** The landing page lists the tables your workspace read most in the last 30 days, from its query history.

## Search that tolerates typos

The search box finds tables and columns by name, even with a typo. Narrow it with filters typed as `key:value`: `type:column`, `tag:pii` (or `-tag:pii`), `certified:yes` and `namespace:sales.raw`. Anything else is free text, so `orders type:column tag:pii` finds PII columns matching *orders*.

## Document your data

Each table and column can carry a Markdown description, tags, an owner and a **certified** flag. The documentation shows in the explorer and in the workbench's inspector, and is available to the built-in assistant and to agents. A separate search finds documentation by text, tag, owner or certification.

## Keep Iceberg tables healthy

For Iceberg tables, Baleia also shows table health and runs per-table maintenance (compaction, manifest rewrites, snapshot expiry and orphan cleanup) from the same workspace.

## Try it

- [Read the data explorer guide](/docs/guides/data)
- [See every connection provider](/docs/connections)
- [Open the live demo](https://demo.baleia.galileostd.io)
