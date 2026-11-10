---
sidebar_label: PostgreSQL
description: "Connect PostgreSQL to Baleia. PostgreSQL relational database, queried directly or exposed through Trino. See its settings and what Baleia does with it."
---

# Connect PostgreSQL to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

PostgreSQL relational database, queried directly or exposed through Trino.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Runs SQL on it directly from the [workbench](../guides/workbench.md), on its own connection (engine kind `postgresql`).
- Sets up and keeps the matching Trino catalog (type `postgresql`), so Trino can query it.

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `5432` |  |
| `database` | text |  | `postgres` | Database to connect to; every database the user may connect to is listed |
| `sslmode` | select |  | `prefer` | one of `disable`, `allow`, `prefer`, `require`, `verify-ca`, `verify-full` |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
