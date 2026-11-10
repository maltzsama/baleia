---
sidebar_label: AWS Glue Data Catalog
description: "Connect AWS Glue Data Catalog to Baleia. AWS Glue Data Catalog holding Iceberg, Hive and Delta Lake tables, exposed through Trino. See its settings and what Baleia does with it."
---

# Connect AWS Glue Data Catalog to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

AWS Glue Data Catalog holding Iceberg, Hive and Delta Lake tables, exposed through Trino.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Sets up and keeps the matching Trino catalog (type `iceberg`, `hive`, `delta_lake`), so Trino can query it.

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `region` | text | yes |  | AWS region of the Data Catalog, e.g. us-east-1 |
| `catalog_id` | text |  |  | Cross-account Data Catalog ID; empty uses the caller's account |
| `endpoint` | text |  |  | Override for tests or regional endpoints, e.g. http://moto:5000; empty uses the AWS default |
| `access_key` | text |  |  | Static credentials; when empty the operator must allow ambient credentials (a role ARN alone still needs base credentials) |
| `secret_key` | secret |  |  |  |
| `role_arn` | text |  |  | Assume this role (with the keys, or the ambient chain) for Glue and S3 |
| `external_id` | text |  |  | External ID of the assumed role's trust policy |
| `storage.type` | select |  | `s3` | one of `s3`. Object storage the table data resides on (S3-compatible in v1) |
| `storage.endpoint` | text |  |  | empty uses the AWS default for the region |
| `storage.region` | text |  |  |  |
| `storage.path_style` | bool |  | `false` | Required by most S3-compatible stores (RustFS, MinIO) |
| `storage.access_key` | text |  |  | Only when the table data is not on AWS S3 with the Glue credentials |
| `storage.secret_key` | secret |  |  |  |

## Next steps

- [Quick start](../quick-start.md)
- [Every connection provider](../connections.md)
- [Concepts](../concepts.md)
