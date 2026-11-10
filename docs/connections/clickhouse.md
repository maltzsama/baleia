---
sidebar_label: ClickHouse
description: "Connect ClickHouse to Baleia. ClickHouse analytical database; direct access is supported and Trino access is planned. See its settings and what Baleia does with it."
---

# Connect ClickHouse to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

ClickHouse analytical database; direct access is supported and Trino access is planned.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Runs SQL on it directly from the [workbench](../guides/workbench.md), on its own connection (engine kind `clickhouse`).

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `8123` | 8123 for HTTP, 8443 for HTTPS |
| `secure` | bool |  | `false` |  |
| `database` | text |  | `default` |  |
| `username` | text |  | `default` |  |
| `password` | secret |  |  |  |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
