---
sidebar_label: SQLite
description: "Connect SQLite to Baleia. SQLite database, queried directly. See its settings and what Baleia does with it."
---

# Connect SQLite to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

SQLite database, queried directly.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Runs SQL on it directly from the [workbench](../guides/workbench.md), on its own connection (engine kind `sqlite`).

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `path` | file_path | yes |  | Path inside BALEIA_SQLITE_ROOT (relative to it, or absolute) |
| `read_write` | bool |  | `false` | Takes effect only when the server sets BALEIA_SQLITE_ALLOW_WRITE=true |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
