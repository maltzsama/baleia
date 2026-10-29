---
sidebar_position: 4
---

# Concepts

Baleia keeps four ideas apart: where credentials point, what runs SQL, where data lives, and how that data becomes visible to an engine ([ADR 0002](adr/0002-connections-engines-catalogs-bindings.md)). Access and saved work sit on top of them.

## Connections

A **connection** is a provider kind with an endpoint and its credentials. It can point at a catalog manager (Polaris, Nessie, Lakekeeper, Iceberg REST, Unity, AWS Glue, Hive Metastore), a database (PostgreSQL, MySQL/MariaDB, SQL Server, Oracle, ClickHouse, SQLite, Arrow Flight SQL) or a query engine (Trino).

Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same network policy ([ADR 0011](adr/0011-direct-connections-security.md)). [Connections](connections.md) lists each kind and its fields.

## Engines

An **engine** runs SQL and is backed by one connection. There are two classes:

- **Federated** engines host catalogs that come from other connections. Trino is the federated engine.
- **Direct** engines run SQL on their own connection: PostgreSQL, MySQL/MariaDB, SQL Server, Oracle, ClickHouse, SQLite and Arrow Flight SQL.

## Catalogs

A **catalog** is a logical container of data: an Iceberg warehouse in a catalog manager, or a database. It belongs to a connection and does not depend on the engine that queries it. Baleia keeps a snapshot of each catalog's metadata (schemas, tables, columns), refreshed on a schedule, for browsing and autocomplete.

## Bindings

A **binding** makes a catalog visible inside an engine, under an engine-side name. It has one of three modes:

| Mode | Meaning |
|---|---|
| `managed` | Baleia created the catalog in the engine (for Trino, through the catalog-store plugin) and keeps it in sync |
| `adopted` | The catalog already existed in the engine; Baleia uses it but does not change it |
| `native` | A direct engine's own databases |

When you run a query you pick a **target**: a binding and, optionally, a schema.

## Workspaces

A **workspace** is where people run queries. It is **personal** (yours) or **shared** (owned by a team). Catalogs are **granted** to a workspace for reading or writing, and a workspace can set a default target. Members see only what their workspace was granted.

**Teams** group users. A team owns shared workspaces, and its members are owners or members of the team.

## Roles

Each user has a global role, **admin** or **member**. Admins manage connections, engines, users and the platform. Members work in the workspaces they belong to.

Finer access comes from where the user is:
- **Team:** owner or member.
- **Space:** viewer or editor.
- **Workspace:** the catalogs it was granted.

The interface shows only the actions the server allows, from each resource's allowed actions.

## Spaces and content

Saved work is **content**: saved queries, metrics and semantic models ([ADR 0013](adr/0013-content-model.md)).

- **Spaces:** each item lives in a space of a workspace. A space is visible to the whole workspace or restricted to its members, with viewer or editor roles.
- **Slugs and versions:** each item has a portable slug and an append-only version history. Deleted items go to the trash and can be restored.
- **Bundles:** content moves between Baleia instances as a bundle. Importing one creates or updates items by slug and never deletes what the bundle omits.

## Executions

Running SQL creates an **execution**. The worker runs the statements in one engine session, stops at the first error, and stores each statement's result as an Arrow file in object storage. The web app reads that file in pages. [Architecture](architecture.md#how-a-query-runs) walks through a run.
