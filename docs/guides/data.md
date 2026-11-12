---
sidebar_position: 2
description: "Browse every catalog in the Baleia data explorer from stored metadata: search tables, see columns and lineage, without querying your SQL engines."
---

# Data explorer

**Data** browses every catalog your workspace was granted, from Baleia's stored metadata: it does not query the engines while you browse.

## Browse

The tree on the left goes catalog → schema → table. Pick a table to open its detail:

- **Header:** the table's kind, its qualified name for the selected engine binding (with a copy button), and when its metadata was last synced.
- **Preview:** runs a 100-row preview in the workbench. **Insert name** puts the qualified name in the editor.
- **About:** the table's documentation.
- **Columns and properties:** from the last metadata sync.

A table the source lists but Baleia cannot read stays in the tree, greyed out and not selectable. Hover its info icon to see why, for example `type: TABLE_DB_STORAGE`: a Databricks managed table in the default storage, which Databricks does not open to external engines, or `registered as: table_type=DELTA`: a Delta table in AWS Glue registered the Athena way, without the `spark.sql.sources.provider=delta` parameter Trino's Delta connector looks for. It has no columns and cannot be queried; see the [connection's page](../connections.md) for what each provider can read.

A table bound into several engines lets you switch the binding the names are shown for. Links to a table keep the catalog, schema and table in the URL (`/data?catalog=…&ns=…&table=…`), so they can be shared.

Metadata is refreshed on a schedule (`BALEIA_METADATA_SYNC_INTERVAL`); an admin can sync a catalog at once from its page in **Connections → Catalogs**.

**Popular in this workspace**, on the Data landing page, lists the tables the workspace read most in the last 30 days, from its query history.

## Search

The search box finds tables and columns by name or comment, tolerating typos, accents and plurals ([how search works](search.md)). Narrow it with filters, typed as `key:value` and shown as chips you can remove:

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

A table in either list opens it: in the workbench inspector its detail appears below the list, and elsewhere (the model editor) the table opens in the data explorer.

Lineage comes from the statements themselves, so it stays current as content changes. When a metadata sync finds that a model or metric points at a table or column that no longer exists, the attention bell reports it ([administration](administration.md#attention)).

## Open in a BI tool

On a Trino target, a table's detail offers **Open in Tableau** and **Open in Power BI**. Each downloads a data-source file (`.tds` or `.pbids`) that opens the table in that tool. The file points at the deployment's BI gateway ([ADR 0017](../adr/0017-bi-gateway.md)) with the target's catalog and schema, and carries no credentials.

The entry appears when the operator has enabled the BI gateway ([Install](../install.md#bi-gateway)) and BI is on for the workspace. Sign in from the BI tool with your e-mail as the user and a workspace-bound BI key as the password; issue one for the workspace (`POST /workspaces/{id}/bi-keys`, the secret is shown once) and revoke it when it is no longer needed. The gateway keeps the session read-only and applies the workspace's catalog scope; only Trino targets are served, so a table on a direct engine has no entry.

## Document

Each table and column can carry documentation: a Markdown description, tags, an owner and a **certified** flag. Edit it from the table detail. The documentation is shown in Data, in the workbench's inspector, and to the assistant and agents.

**Data → Docs** searches the documentation by text, tag, owner or certification.

## Maintain Iceberg tables

Iceberg tables have per-table maintenance parameters for compaction, manifest rewrites, snapshot expiry and orphan cleanup. They are set on the **Maintenance** page, opened from **Monitoring → Table health** with **Configure**. [Monitoring and maintenance](monitoring.md#table-maintenance) explains them.
