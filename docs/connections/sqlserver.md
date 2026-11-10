---
sidebar_label: SQL Server
description: "Connect SQL Server to Baleia. Microsoft SQL Server relational database, queried directly or exposed through Trino. See its settings and what Baleia does with it."
---

# Connect SQL Server to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Microsoft SQL Server relational database, queried directly or exposed through Trino.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Runs SQL on it directly from the [workbench](../guides/workbench.md), on its own connection (engine kind `sqlserver`).
- Sets up and keeps the matching Trino catalog (type `sqlserver`), so Trino can query it.

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `1433` |  |
| `database` | text |  | `master` | Database to connect to; every database the login may access is listed |
| `encrypt` | select |  | `true` | one of `true`, `false`, `disable`, `strict` |
| `trust_server_certificate` | bool |  | `false` |  |
| `cancel_with_kill` | bool |  | `false` | Requires the ALTER ANY CONNECTION permission; lets a running statement be cancelled from another process |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
