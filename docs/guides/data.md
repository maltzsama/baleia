---
sidebar_position: 2
---

# Data explorer

**Data** browses every catalog your workspace was granted, from Baleia's stored metadata: it does not query the engines while you browse.

## Browse

The tree on the left goes catalog → schema → table. Pick a table to open its detail:

- **Header:** the table's kind, its qualified name for the selected engine binding (with a copy button), and when its metadata was last synced.
- **Preview:** runs a 100-row preview in the workbench. **Insert name** puts the qualified name in the editor.
- **About:** the table's documentation.
- **Columns and properties:** from the last metadata sync.

A table bound into several engines lets you switch the binding the names are shown for. Links to a table keep the catalog, schema and table in the URL (`/data?catalog=…&ns=…&table=…`), so they can be shared.

Metadata is refreshed on a schedule (`BALEIA_METADATA_SYNC_INTERVAL`); an admin can sync a catalog at once from its page in **Connections → Catalogs**.

## Document

Each table and column can carry documentation: a Markdown description, tags, an owner and a **certified** flag. Edit it from the table detail. The documentation is shown in Data, in the workbench's inspector, and to the assistant and agents.

**Data → Docs** searches the documentation by text, tag, owner or certification.

## Maintain Iceberg tables

Tables of Iceberg catalogs have a **Maintenance** section in their detail: per-table parameters for compaction, manifest rewrites, snapshot expiry and orphan cleanup. [Monitoring and maintenance](monitoring.md#table-maintenance) explains them.
