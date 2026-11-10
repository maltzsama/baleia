---
sidebar_label: Project Nessie
description: "Connect Project Nessie to Baleia. Project Nessie catalog manager, exposed through Trino. See its settings and what Baleia does with it."
---

# Connect Project Nessie to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Project Nessie catalog manager, exposed through Trino.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Sets up and keeps the matching Trino catalog (type `iceberg`), so Trino can query it.

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | Server root, e.g. http://nessie:19120 (without /iceberg) |
| `warehouse` | text |  |  | Default: the server's default warehouse |
| `token` | secret |  |  |  |
| `storage.endpoint` | text |  |  | Only with an S3 access key, e.g. https://<namespace>.compat.objectstorage.<region>.oraclecloud.com; empty uses the AWS default for the region |
| `storage.region` | text |  |  | Only with an S3 access key, e.g. us-east-1 |
| `storage.path_style` | bool |  | `false` | Only with an S3 access key; required by most S3-compatible stores |
| `storage.access_key` | text |  |  | Set it when the object store has no STS: the engine then uses these credentials instead of vended ones |
| `storage.secret_key` | secret |  |  |  |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
