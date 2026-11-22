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

| Filter       | Example               | Keeps                                                    |
| ------------ | --------------------- | -------------------------------------------------------- |
| `type:`      | `type:column`         | `table`, `column` or `all` results                       |
| `tag:`       | `tag:pii`             | items with the tag; `-tag:pii` excludes it               |
| `certified:` | `certified:yes`       | certified (`yes`) or uncertified (`no`) tables           |
| `namespace:` | `namespace:sales.raw` | items under that schema; quote a name that contains dots |

Anything that is not a filter is free text: `orders type:column tag:pii` finds PII columns matching _orders_.

## Lineage

A table's detail shows its lineage in two directions:

- **Used by:** the saved queries, metrics and semantic models that read the table.
- **Depends on:** for a saved query or a model, the tables it reads. It shows in the workbench sidebar and in the model editor.

A table in either list opens it: in the workbench inspector its detail appears below the list, and elsewhere (the model editor) the table opens in the data explorer.

Lineage comes from the statements themselves, so it stays current as content changes. When a metadata sync finds that a model or metric points at a table or column that no longer exists, the attention bell reports it ([administration](administration.md#attention)).

## Kafka topics

An Apache Kafka connection ([its settings](../connections/kafka.md)) is read through Trino's Kafka connector. Each of its catalogs lists the topics it exposes, under one schema (`default` unless you name another), and each topic is a table.

- **Columns.** Baleia ships no table description files, so a topic's columns are the connector's internal ones: `_key` and `_message` (the bytes as text), `_timestamp`, `_partition_id`, `_partition_offset`, `_headers` and the segment columns. Read JSON messages with Trino's JSON functions, for example `json_extract_scalar(_message, '$.id')`.
- **Reads.** Baleia never produces messages or commits offsets, and ships no table description files: without one for a topic the connector has no encoder, so an `INSERT` into it fails. Baleia does not block writes itself ([ADR 0016](../adr/0016-access-layers-and-workspace-roles.md)): if an operator places a description file with an encoder for a topic in the Trino nodes' table description directory (`etc/kafka` by default), Trino can write to that topic, and the Kafka cluster's own ACLs decide.
- **Live data.** A topic changes as messages arrive and segments expire, so two reads of the same topic in one query (a self join) can see different rows.
- **Topics.** Names must be lowercase letters, digits, `_` or `-`, without dots: Trino folds identifiers to lowercase and reads a dot as a schema qualifier. Changing a catalog's topics re-creates its Trino catalog.

Operational prerequisites:

- Kafka 3.3 or later with KRaft, and Trino 484.
- Every Trino coordinator and worker must reach every broker, not only the bootstrap ones you list. The connection test proves that the API reaches the brokers and that Trino loads the catalog, but listing the catalog's schemas does not contact the brokers: the first query on a topic is what proves the Trino nodes reach them.
- Security is `PLAINTEXT` or `SSL` with the JVM's default trust store. A private CA, client certificates and SASL need files on every Trino node and are not supported, nor is a Schema Registry.

## Open in a BI tool

On a Trino target, a table's detail offers **Download Tableau connection** and **Download Power BI connection**. The action opens a guided dialog: it checks the gateway and workspace readiness, lets you pick or create a workspace-bound BI key, and downloads a credential-free data-source file (`.tds` or `.pbids`) that points at the deployment's BI gateway ([ADR 0017](../adr/0017-bi-gateway.md)) with the target's catalog and schema. A missing prerequisite is named in the dialog with a link to the screen that fixes it, instead of hiding the action.

The gateway must be enabled by the operator ([Install](../install.md#bi-gateway)) and BI must be on for the workspace. In the BI tool, sign in with the key owner's e-mail as the user and the key as the password; the secret is shown once when the key is created, and the key is revoked from the workspace BI access. The gateway keeps the session read-only and applies the workspace's catalog scope; only Trino targets are served, so a table on a direct engine has no entry.

## Document

Each table and column can carry documentation: a Markdown description, tags, an owner and a **certified** flag. Edit it from the table detail. The documentation is shown in Data, in the workbench's inspector, and to the assistant and agents.

**Data → Docs** searches the documentation by text, tag, owner or certification.

## Maintain Iceberg tables

Iceberg tables have per-table maintenance parameters for compaction, manifest rewrites, snapshot expiry and orphan cleanup. They are set on the **Maintenance** page, opened from **Monitoring → Table health** with **Configure**. [Monitoring and maintenance](monitoring.md#table-maintenance) explains them.
