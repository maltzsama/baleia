---
slug: /
sidebar_position: 1
title: Introduction
---

# Baleia

Baleia is a self-hosted SQL workspace for your data platform. Write and run SQL against Trino and your databases, browse every catalog in one place, and share queries, metrics and models with your team.

![The Baleia SQL editor running a query on Trino](img/workbench.png)

Baleia is a **control plane**. It manages connections, catalogs and engines, runs your SQL on those engines and shows the results. The engines do the work: external clients keep talking to them directly, and joins across sources are the job of a federated engine such as Trino ([why](adr/0001-control-plane-only.md)).

## What you can do

- **Write and run SQL** in the [workbench](guides/workbench.md): tabs, autocomplete from your catalogs, formatting, EXPLAIN, scripts, and large results you can page through or export.
- **Query many engines from one place**: Trino, plus PostgreSQL, MySQL/MariaDB, SQL Server, Oracle, ClickHouse, SQLite and Arrow Flight SQL directly.
- **Connect your lakehouse**: register Polaris, Nessie, Lakekeeper, Iceberg REST, Unity, AWS Glue or Hive Metastore, and Baleia sets up the matching Trino catalog ([connections](connections.md)).
- **Find and document your data** in the [data explorer](guides/data.md).
- **Share and reuse** saved queries, metrics and semantic models in team [spaces](guides/content.md), with version history and export to Git-friendly bundles, and build questions in [Explore](guides/explore.md) without writing SQL.
- **Materialize queries** into Iceberg tables on a schedule with [retrievers](guides/content.md#retrievers).
- **Keep an eye on things**: the cluster, running queries, query history and Iceberg table health, with per-table [maintenance](guides/monitoring.md).
- **Control access** with workspaces, teams and roles, OIDC or LDAP sign-in, API keys and an audit trail ([administration](guides/administration.md)).
- **Bring in AI, if you want**: an optional [assistant](assistant.md) and an [MCP server](mcp.md) for your own agents.

The interface is available in English and Brazilian Portuguese.

## Where to start

| | |
|---|---|
| [Quick start](quick-start.md) | Run Baleia on your machine with Docker |
| [Install on Kubernetes](install.md) | Deploy a release with Helm |
| [Concepts](concepts.md) | Connections, engines, catalogs, bindings, workspaces and spaces |
| [Configuration](configuration.md) | Every setting |
| [Operations](operations.md) | Upgrades, metrics, alerts, the audit trail and runbooks |

Baleia is in beta and moving fast; expect changes between minor versions. Bugs and ideas are welcome in [issues](https://github.com/maltzsama/baleia/issues).
