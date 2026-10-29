---
sidebar_position: 4
---

# Content, spaces and retrievers

Saved work lives in **Content**: saved queries, metrics and semantic models, organized in spaces ([concepts](../concepts.md#spaces-and-content)). Retrievers, which turn a query into a table on a schedule, live here too.

## Spaces

A workspace has a default space and can have more (**Content → Spaces**). Each space has:

- **Visibility:** the whole workspace, or **restricted** to its members.
- **Roles:** members are viewers or editors; the space sets the role new members get.
- **A slug:** portable, used to match content between instances.

Folders organize saved queries inside a space. The default space cannot be deleted. Deleting another space deletes the content in it.

## Versions and trash

Every save adds a version to a saved query, a metric or a model. Open an item's history to compare versions and restore an older one; restoring adds a new version, so nothing is lost.

Deleted items go to **Content → Trash**, where they can be restored.

## Move content between instances

A space exports to a **bundle**, a JSON document (`baleia.bundle`, version 1) with its folders, saved queries, metrics and models. Exporting the same content twice gives the same bytes, so bundles can live in Git and be reviewed like code.

- **Export:** from the space's page; viewers can export.
- **Import:** from the space's page, or **Import space bundle** in **Content → Spaces**; editors can import. A bundle is at most 10 MiB.
  - **Dry run** first: each item reports `CREATE`, `UPDATE`, `NO_CHANGES` or `ERROR` before anything is written.
  - **Apply** to write.
- **Matching:** import matches items by kind and slug, so a re-import updates instead of duplicating.
- **Nothing is deleted:** items that are in the space but not in the bundle are left unchanged.
- **Validation:** metrics are validated when the import is applied.

## Retrievers

A **retriever** materializes a SQL query into an Iceberg table on a schedule: each refresh runs `CREATE OR REPLACE TABLE … AS <your query>` on the destination engine.

- **Destination:** an Iceberg catalog bound to a federated engine (Trino) that the workspace has a **write** grant on, plus a schema and a table name. **Content → Retrievers** appears once such a catalog exists.
- **Definition:** a single `SELECT`. **Validate on the destination** checks it on the destination engine before saving.
- **Schedule:** a cron expression, at most every 5 minutes. Refreshes are full: the table is rebuilt each time.
- **Refresh now** runs one at once. The **refresh history** lists each run with its outcome.
- **Deleting** a retriever stops its schedule and removes its history; the destination table is not dropped.

A retriever whose destination is no longer valid (the grant was revoked, the binding is gone) stops refreshing and shows what to fix. Its failures also appear in the attention bell ([administration](administration.md#attention)).
