---
sidebar_label: Hive Metastore
description: "Connect Hive Metastore to Baleia. Hive Metastore (Thrift) holding Iceberg, Hive and Delta Lake tables, exposed through Trino. See its settings and what Baleia does with it."
---

# Connect Hive Metastore to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Hive Metastore (Thrift) holding Iceberg, Hive and Delta Lake tables, exposed through Trino.

## What Baleia does with it

- Sets up and keeps the matching Trino catalog (type `iceberg`, `hive`, `delta_lake`), so Trino can query it.

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `metastore_uri` | text | yes |  | Thrift URI of the Hive Metastore, e.g. thrift://hms:9083 |
| `storage.type` | select |  | `s3` | one of `s3`. Object storage the table data resides on (S3-compatible in v1) |
| `storage.endpoint` | text |  |  | e.g. http://rustfs:9000; empty uses the AWS default for the region |
| `storage.region` | text |  |  | e.g. us-east-1 |
| `storage.path_style` | bool |  | `false` | Required by most S3-compatible stores (RustFS, MinIO) |
| `storage.access_key` | text |  |  |  |
| `storage.secret_key` | secret |  |  |  |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
