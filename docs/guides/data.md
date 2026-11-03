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

**Popular in this workspace**, on the Data landing page, lists the tables the workspace read most in the last 30 days, from its query history.

## Search

The search box finds tables and columns by name, tolerating typos. Narrow it with filters, typed as `key:value` and shown as chips you can remove:

| Filter | Example | Keeps |
|---|---|---|
| `type:` | `type:column` | `table`, `column` or `all` results |
| `tag:` | `tag:pii` | items with the tag; `-tag:pii` excludes it |
| `certified:` | `certified:yes` | certified (`yes`) or uncertified (`no`) tables |
| `namespace:` | `namespace:sales.raw` | items under that schema; quote a name that contains dots |

Anything that is not a filter is free text: `orders type:column tag:pii` finds PII columns matching *orders*.

## Lineage

A table's detail shows its lineage in two directions:

- **Used by:** the saved queries, metrics and semantic models that read the table.
- **Depends on:** for a saved query or a model, the tables it reads. It shows in the workbench sidebar and in the model editor.

Lineage comes from the statements themselves, so it stays current as content changes. When a metadata sync finds that a model or metric points at a table or column that no longer exists, the attention bell reports it ([administration](administration.md#attention)).

## Open in a BI tool

On a Trino, PostgreSQL, MySQL or SQL Server engine, a table's detail offers **Open in Tableau** and **Open in Power BI**. Each downloads a data-source file (`.tds` or `.pbids`) that opens the table in that tool. The file carries no credentials: you sign in from the BI tool, which connects to the engine directly. The engine needs an **External URL** first: the address BI clients use to reach it, set in the engine's settings (**Connections → Engines**).

The engine must authenticate the BI tool's user itself. The Trino bundled with Baleia has no authentication ([Architecture](../architecture.md#trino-security-model)), so never give it an External URL or expose it to BI clients: anyone reaching it could claim any user. Use an engine that authenticates its clients.

## Document

Each table and column can carry documentation: a Markdown description, tags, an owner and a **certified** flag. Edit it from the table detail. The documentation is shown in Data, in the workbench's inspector, and to the assistant and agents.

**Data → Docs** searches the documentation by text, tag, owner or certification.

## Maintain Iceberg tables

Iceberg tables have per-table maintenance parameters for compaction, manifest rewrites, snapshot expiry and orphan cleanup. They are set on the **Maintenance** page, opened from **Monitoring → Table health** with **Configure**. [Monitoring and maintenance](monitoring.md#table-maintenance) explains them.
