---
sidebar_label: Oracle Database
description: "Connect Oracle Database to Baleia. Oracle Database, queried directly or exposed through Trino. See its settings and what Baleia does with it."
---

# Connect Oracle Database to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Oracle Database, queried directly or exposed through Trino.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Runs SQL on it directly from the [workbench](../guides/workbench.md), on its own connection (engine kind `oracle`).
- Sets up and keeps the matching Trino catalog (type `oracle`), so Trino can query it.

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `1521` |  |
| `service_name` | text | yes |  | e.g. FREEPDB1 |
| `cancel_with_alter_system` | bool |  | `false` | Requires the ALTER SYSTEM permission and SELECT on v$session and v$sql; lets a running statement be cancelled from another process |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
