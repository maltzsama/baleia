---
sidebar_label: Arrow Flight SQL
description: "Connect Arrow Flight SQL to Baleia. Arrow Flight SQL endpoint, queried directly. See its settings and what Baleia does with it."
---

# Connect Arrow Flight SQL to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Arrow Flight SQL endpoint, queried directly.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Runs SQL on it directly from the [workbench](../guides/workbench.md), on its own connection (engine kind `flightsql`).

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int | yes |  |  |
| `tls` | bool |  | `true` |  |
| `auth` | select |  | `none` | one of `none`, `basic`, `token` |
| `username` | text |  |  | Basic authentication |
| `password` | secret |  |  | Basic authentication |
| `token` | secret |  |  | Token authentication |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
