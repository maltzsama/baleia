---
sidebar_label: Apache Polaris
description: "Connect Apache Polaris to Baleia. Apache Polaris Iceberg REST catalog, exposed through Trino. See its settings and what Baleia does with it."
---

# Connect Apache Polaris to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Apache Polaris Iceberg REST catalog, exposed through Trino.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Sets up and keeps the matching Trino catalog (type `iceberg`), so Trino can query it.

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | Server root, e.g. http://polaris:8181 (without /api/catalog) |
| `realm` | text |  | `POLARIS` |  |
| `client_id` | text | yes |  |  |
| `client_secret` | secret | yes |  |  |
| `user_identity` | bool |  | `false` | The catalog manager authorizes each Baleia user with the access token of the identity provider they signed in with; it must trust that provider. Users signed in without one, and background work, act as this connection's own credentials. |
| `storage.endpoint` | text |  |  | Only with an S3 access key, e.g. https://<namespace>.compat.objectstorage.<region>.oraclecloud.com; empty uses the AWS default for the region |
| `storage.region` | text |  |  | Only with an S3 access key, e.g. us-east-1 |
| `storage.path_style` | bool |  | `false` | Only with an S3 access key; required by most S3-compatible stores |
| `storage.access_key` | text |  |  | Set it when the object store has no STS: the engine then uses these credentials instead of vended ones |
| `storage.secret_key` | secret |  |  |  |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
