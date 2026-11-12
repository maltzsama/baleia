---
description: "Every connection provider Baleia supports, from PostgreSQL to Iceberg catalogs like Polaris and Nessie: what each is used for and how to configure it."
---

# Connection providers

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Every connection kind Baleia knows, what it can be used for, and its configuration.
See [ADR 0002](adr/0002-connections-engines-catalogs-bindings.md) for the model,
[ADR 0011](adr/0011-direct-connections-security.md) for network safety and
[ADR 0021](adr/0021-unified-connection-model.md) for access modes and categories.

*Access modes* lists how the connection reaches its data; *Trino catalog types*
lists the internal catalog implementations it is exposed through. Neither is a
user-facing connector selection.

| Kind | Label | Category | Access modes | Roles | Engine kind | Trino catalog types | Status |
|---|---|---|---|---|---|---|---|
| `bigquery` | BigQuery | cloud_platform | trino | — | — | bigquery | planned |
| `blackhole` | Black Hole | internal_catalog | trino | — | — | blackhole | experimental |
| `cassandra` | Apache Cassandra | nosql | trino | — | — | cassandra | planned |
| `clickhouse` | ClickHouse | analytical | direct | metadata, direct_engine | clickhouse | — | supported |
| `delta` | Delta Lake | lakehouse | trino | — | — | delta_lake | planned |
| `doris` | Apache Doris | engine | — | federated_engine | doris | — | planned (planned after v1 (#21)) |
| `druid` | Apache Druid | analytics | trino | — | — | druid | planned |
| `duckdb` | DuckDB | analytical | trino | — | — | duckdb | planned |
| `elasticsearch` | Elasticsearch | search | trino | — | — | elasticsearch | planned |
| `exasol` | Exasol | relational | trino | — | — | exasol | planned |
| `faker` | Faker | synthetic | trino | — | — | faker | experimental |
| `flightsql` | Arrow Flight SQL | engine | direct | metadata, direct_engine | flightsql | — | supported |
| `glue` | AWS Glue Data Catalog | metastore | trino | metadata | — | iceberg, hive, delta_lake | supported |
| `gsheets` | Google Sheets | external_service | trino | — | — | gsheets | planned |
| `hive` | Hive Metastore | metastore | trino | — | — | iceberg, hive, delta_lake | supported |
| `hudi` | Apache Hudi | lakehouse | trino | — | — | hudi | planned |
| `iceberg` | Iceberg REST catalog | catalog_manager | trino | metadata | — | iceberg | supported |
| `ignite` | Apache Ignite | relational | trino | — | — | ignite | planned |
| `jmx` | JMX | internal_catalog | trino | — | — | jmx | experimental |
| `kafka` | Apache Kafka | streaming | trino | — | — | kafka | planned |
| `lakekeeper` | Lakekeeper | catalog_manager | trino | metadata | — | iceberg | supported |
| `loki` | Grafana Loki | observability | trino | — | — | loki | planned |
| `mariadb` | MariaDB | relational | direct, trino | metadata, direct_engine | mariadb | mariadb | supported |
| `memory` | Memory | internal_catalog | trino | — | — | memory | experimental |
| `mongodb` | MongoDB | nosql | trino | — | — | mongodb | planned |
| `mysql` | MySQL | relational | direct, trino | metadata, direct_engine | mysql | mysql | supported |
| `nessie` | Project Nessie | catalog_manager | trino | metadata | — | iceberg | supported |
| `opensearch` | OpenSearch | search | trino | — | — | opensearch | planned |
| `oracle` | Oracle Database | relational | direct, trino | metadata, direct_engine | oracle | oracle | supported |
| `pinot` | Apache Pinot | analytics | trino | — | — | pinot | planned |
| `polaris` | Apache Polaris | catalog_manager | trino | metadata | — | iceberg | supported |
| `postgresql` | PostgreSQL | relational | direct, trino | metadata, direct_engine | postgresql | postgresql | supported |
| `prometheus` | Prometheus | observability | trino | — | — | prometheus | planned |
| `redis` | Redis | nosql | trino | — | — | redis | planned |
| `redshift` | Amazon Redshift | cloud_platform | trino | — | — | redshift | planned |
| `singlestore` | SingleStore | relational | trino | — | — | singlestore | planned |
| `snowflake` | Snowflake | cloud_platform | trino | — | — | snowflake | planned |
| `sqlite` | SQLite | relational | direct | metadata, direct_engine | sqlite | — | unsupported (BALEIA_SQLITE_ROOT not set) |
| `sqlserver` | SQL Server | relational | direct, trino | metadata, direct_engine | sqlserver | sqlserver | supported |
| `starrocks` | StarRocks | engine | — | federated_engine | starrocks | — | planned (planned after v1 (#30)) |
| `system` | System | internal_catalog | — | — | — | — | not_applicable (reserved system catalog; not user-creatable) |
| `tpcds` | TPC-DS | synthetic | trino | — | — | tpcds | experimental |
| `tpch` | TPC-H | synthetic | trino | — | — | tpch | experimental |
| `trino` | Trino | engine | — | metadata, federated_engine | trino | — | supported |
| `unity` | Unity Catalog | catalog_manager | trino | metadata | — | iceberg | supported |

## `bigquery` — BigQuery

Google BigQuery data warehouse, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `blackhole` — Black Hole

Trino Black Hole catalog.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `cassandra` — Apache Cassandra

Apache Cassandra store, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `clickhouse` — ClickHouse

ClickHouse analytical database; direct access is supported and Trino access is planned.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `8123` | 8123 for HTTP, 8443 for HTTPS |
| `secure` | bool |  | `false` |  |
| `database` | text |  | `default` |  |
| `username` | text |  | `default` |  |
| `password` | secret |  |  |  |

## `delta` — Delta Lake

Delta Lake tables, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `doris` — Apache Doris

Apache Doris federated query engine.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `druid` — Apache Druid

Apache Druid analytics store, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `duckdb` — DuckDB

DuckDB analytical database, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `elasticsearch` — Elasticsearch

Elasticsearch search store, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `exasol` — Exasol

Exasol relational database, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `faker` — Faker

Synthetic Faker data, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `flightsql` — Arrow Flight SQL

Arrow Flight SQL endpoint, queried directly.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int | yes |  |  |
| `tls` | bool |  | `true` |  |
| `auth` | select |  | `none` | one of `none`, `basic`, `token` |
| `username` | text |  |  | Basic authentication |
| `password` | secret |  |  | Basic authentication |
| `token` | secret |  |  | Token authentication |

## `glue` — AWS Glue Data Catalog

AWS Glue Data Catalog holding Iceberg, Hive and Delta Lake tables, exposed through Trino. A Delta table is read only when Glue registers it with spark.sql.sources.provider=delta, as Spark, Databricks and the Glue Delta crawler do; one registered only with Athena's table_type=DELTA stays in the catalog greyed out with the reason, since Trino's Delta connector does not recognize it.

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

## `gsheets` — Google Sheets

Google Sheets spreadsheets, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `hive` — Hive Metastore

Hive Metastore (Thrift) holding Iceberg, Hive and Delta Lake tables, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `metastore_uri` | text | yes |  | Thrift URI of the Hive Metastore, e.g. thrift://hms:9083 |
| `storage.type` | select |  | `s3` | one of `s3`. Object storage the table data resides on (S3-compatible in v1) |
| `storage.endpoint` | text |  |  | e.g. http://rustfs:9000; empty uses the AWS default for the region |
| `storage.region` | text |  |  | e.g. us-east-1 |
| `storage.path_style` | bool |  | `false` | Required by most S3-compatible stores (RustFS, MinIO) |
| `storage.access_key` | text |  |  |  |
| `storage.secret_key` | secret |  |  |  |

## `hudi` — Apache Hudi

Apache Hudi tables, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `iceberg` — Iceberg REST catalog

Iceberg REST catalog, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | Base URI of the Iceberg REST API (the one /v1/config is under) |
| `warehouse` | text |  |  |  |
| `token` | secret |  |  |  |
| `client_id` | text |  |  |  |
| `client_secret` | secret |  |  |  |
| `oauth2_server_uri` | url |  |  | Default: <REST URI>/v1/oauth/tokens |
| `scope` | text |  |  |  |
| `user_identity` | bool |  | `false` | The catalog manager authorizes each Baleia user with the access token of the identity provider they signed in with; it must trust that provider. Users signed in without one, and background work, act as this connection's own credentials. |
| `storage.endpoint` | text |  |  | Only with an S3 access key, e.g. https://<namespace>.compat.objectstorage.<region>.oraclecloud.com; empty uses the AWS default for the region |
| `storage.region` | text |  |  | Only with an S3 access key, e.g. us-east-1 |
| `storage.path_style` | bool |  | `false` | Only with an S3 access key; required by most S3-compatible stores |
| `storage.access_key` | text |  |  | Set it when the object store has no STS: the engine then uses these credentials instead of vended ones |
| `storage.secret_key` | secret |  |  |  |

## `ignite` — Apache Ignite

Apache Ignite distributed database, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `jmx` — JMX

Trino JMX monitoring catalog.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `kafka` — Apache Kafka

Apache Kafka streaming store, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `lakekeeper` — Lakekeeper

Lakekeeper Iceberg REST catalog, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | Server root, e.g. http://lakekeeper:8181 (without /catalog) |
| `auth_mode` | select |  | `none` | one of `none`, `oauth2`. none is for servers with authorization off |
| `client_id` | text |  |  | Required with auth_mode=oauth2 |
| `client_secret` | secret |  |  | Required with auth_mode=oauth2 |
| `token_endpoint` | url |  |  | The IdP token endpoint (not the Lakekeeper URL); required with auth_mode=oauth2 |
| `scope` | text |  |  |  |
| `user_identity` | bool |  | `false` | The catalog manager authorizes each Baleia user with the access token of the identity provider they signed in with; it must trust that provider. Users signed in without one, and background work, act as this connection's own credentials. |
| `storage.endpoint` | text |  |  | Only with an S3 access key, e.g. https://<namespace>.compat.objectstorage.<region>.oraclecloud.com; empty uses the AWS default for the region |
| `storage.region` | text |  |  | Only with an S3 access key, e.g. us-east-1 |
| `storage.path_style` | bool |  | `false` | Only with an S3 access key; required by most S3-compatible stores |
| `storage.access_key` | text |  |  | Set it when the object store has no STS: the engine then uses these credentials instead of vended ones |
| `storage.secret_key` | secret |  |  |  |

## `loki` — Grafana Loki

Grafana Loki logs, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `mariadb` — MariaDB

MariaDB relational database, queried directly or exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `3306` |  |
| `database` | text |  |  |  |
| `tls` | select |  | `preferred` | one of `preferred`, `true`, `skip-verify`, `false` |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## `memory` — Memory

In-memory Trino catalog.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `mongodb` — MongoDB

MongoDB store, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `mysql` — MySQL

MySQL relational database, queried directly or exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `3306` |  |
| `database` | text |  |  |  |
| `tls` | select |  | `preferred` | one of `preferred`, `true`, `skip-verify`, `false` |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## `nessie` — Project Nessie

Project Nessie catalog manager, exposed through Trino.

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

## `opensearch` — OpenSearch

OpenSearch search store, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `oracle` — Oracle Database

Oracle Database, queried directly or exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `1521` |  |
| `service_name` | text | yes |  | e.g. FREEPDB1 |
| `cancel_with_alter_system` | bool |  | `false` | Requires the ALTER SYSTEM permission and SELECT on v$session and v$sql; lets a running statement be cancelled from another process |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## `pinot` — Apache Pinot

Apache Pinot analytics store, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `polaris` — Apache Polaris

Apache Polaris Iceberg REST catalog, exposed through Trino.

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

## `postgresql` — PostgreSQL

PostgreSQL relational database, queried directly or exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `5432` |  |
| `database` | text |  | `postgres` | Database to connect to; every database the user may connect to is listed |
| `sslmode` | select |  | `prefer` | one of `disable`, `allow`, `prefer`, `require`, `verify-ca`, `verify-full` |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## `prometheus` — Prometheus

Prometheus metrics, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `redis` — Redis

Redis key-value store, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `redshift` — Amazon Redshift

Amazon Redshift data warehouse, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `singlestore` — SingleStore

SingleStore relational database, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `snowflake` — Snowflake

Snowflake data warehouse, exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `sqlite` — SQLite

SQLite database, queried directly.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `path` | file_path | yes |  | Path inside BALEIA_SQLITE_ROOT (relative to it, or absolute) |
| `read_write` | bool |  | `false` | Takes effect only when the server sets BALEIA_SQLITE_ALLOW_WRITE=true |

## `sqlserver` — SQL Server

Microsoft SQL Server relational database, queried directly or exposed through Trino.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `1433` |  |
| `database` | text |  | `master` | Database to connect to; every database the login may access is listed |
| `encrypt` | select |  | `true` | one of `true`, `false`, `disable`, `strict` |
| `trust_server_certificate` | bool |  | `false` |  |
| `cancel_with_kill` | bool |  | `false` | Requires the ALTER ANY CONNECTION permission; lets a running statement be cancelled from another process |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## `starrocks` — StarRocks

StarRocks federated query engine.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `system` — System

Trino system catalog, reserved and not user-creatable.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `tpcds` — TPC-DS

TPC-DS synthetic benchmark data.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `tpch` — TPC-H

TPC-H synthetic benchmark data.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|

## `trino` — Trino

Trino federated query engine.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | e.g. http://trino:8080 |
| `service_user` | text |  | `baleia` | Identity for metadata reads and service statements |
| `admin_user` | text |  | `baleia-admin` | Identity that cancels queries; never runs user SQL |
| `username` | text |  |  | Password authentication (HTTPS only); must be allowed to impersonate the identities Baleia sends |
| `password` | secret |  |  |  |
| `ca_cert` | text |  |  | PEM bundle trusted for the coordinator's certificate, besides the system roots |

## `unity` — Unity Catalog

Unity Catalog, exposed through Trino over its Iceberg REST endpoint. Baleia reads the tables that endpoint can serve to an external engine: Iceberg tables and Delta tables with Iceberg compatibility (UniForm), as long as they are readable from outside Databricks. A table that is not stays in the catalog, greyed out and with the reason, and cannot be queried. On Databricks that is every managed table in the workspace's default storage (securable kind TABLE_DB_STORAGE), UniForm or not: Databricks does not give external engines credentials for it. Use a native Iceberg table (USING ICEBERG), or a Delta table that is external or lives in a catalog with its own managed storage, with Iceberg compatibility enabled ('delta.enableIcebergCompatV2' = 'true' and 'delta.universalFormat.enabledFormats' = 'iceberg'; see Databricks' UniForm documentation for the prerequisites). Set iceberg_path to /api/2.1/unity-catalog/iceberg-rest.

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
