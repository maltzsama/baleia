---
sidebar_label: Trino
description: "Connect Trino to Baleia. Trino federated query engine. See its settings and what Baleia does with it."
---

# Connect Trino to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Trino federated query engine.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Runs SQL as a federated engine, so one query can join across the sources registered with it.

## Configuration

Register it from **Engines** with **Register engine**, which creates the connection and the engine together. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | e.g. http://trino:8080 |
| `service_user` | text |  | `baleia` | Identity for metadata reads and service statements |
| `admin_user` | text |  | `baleia-admin` | Identity that cancels queries; never runs user SQL |
| `username` | text |  |  | Password authentication (HTTPS only); must be allowed to impersonate the identities Baleia sends |
| `password` | secret |  |  |  |
| `ca_cert` | textarea |  |  | PEM bundle trusted for the coordinator's certificate, besides the system roots |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
