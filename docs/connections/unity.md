---
sidebar_label: Unity Catalog
description: "Connect Unity Catalog to Baleia. Unity Catalog, exposed through Trino over its Iceberg REST endpoint. Baleia reads only tables that expose Iceberg metadata: Iceberg tables and Delta tables with Iceberg compatibility (UniForm) enabled. On Databricks, enable it on each Delta table ('delta.universalFormat.enabledFormats' = 'iceberg') and set iceberg_path to /api/2.1/unity-catalog/iceberg-rest. A table without it, such as a plain Delta table, is not readable through Baleia: the endpoint answers it with 400, and the metadata sync skips it and still syncs the rest of the catalog. See its settings and what Baleia does with it."
---

# Connect Unity Catalog to Baleia

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Unity Catalog, exposed through Trino over its Iceberg REST endpoint. Baleia reads only tables that expose Iceberg metadata: Iceberg tables and Delta tables with Iceberg compatibility (UniForm) enabled. On Databricks, enable it on each Delta table ('delta.universalFormat.enabledFormats' = 'iceberg') and set iceberg_path to /api/2.1/unity-catalog/iceberg-rest. A table without it, such as a plain Delta table, is not readable through Baleia: the endpoint answers it with 400, and the metadata sync skips it and still syncs the rest of the catalog.

## What Baleia does with it

- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.
- Sets up and keeps the matching Trino catalog (type `iceberg`), so Trino can query it.

## Configuration

Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | Server root, e.g. http://unity:8080 or a Databricks workspace URL |
| `token` | secret |  |  | OSS token or a Databricks personal access token |
| `iceberg_path` | text |  | `/api/2.1/unity-catalog/iceberg` | Override for Databricks: /api/2.1/unity-catalog/iceberg-rest |
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
