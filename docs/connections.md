# Connection providers

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Every connection kind Baleia knows, what it can be used for, and its configuration.
See [ADR 0002](adr/0002-connections-engines-catalogs-bindings.md) for the model and
[ADR 0011](adr/0011-direct-connections-security.md) for network safety.

*Bindable into* lists the federated engines whose binder can bind the kind, with the
connectors it may use (the first is the default; `internal/bindings`).

| Kind | Label | Family | Roles | Engine kind | Bindable into | Status |
|---|---|---|---|---|---|---|
| `clickhouse` | ClickHouse | database | metadata, direct_engine | clickhouse | — | supported |
| `doris` | Apache Doris | engine | federated_engine | doris | — | unsupported (planned after v1 (#21)) |
| `flightsql` | Arrow Flight SQL | engine | metadata, direct_engine | flightsql | — | supported |
| `hive` | Hive Metastore | metastore |  | — | trino (iceberg, hive, delta_lake) | supported |
| `iceberg` | Iceberg REST catalog | iceberg | metadata | — | trino (iceberg) | supported |
| `lakekeeper` | Lakekeeper | iceberg | metadata | — | trino (iceberg) | supported |
| `mariadb` | MariaDB | database | metadata, direct_engine | mariadb | trino (mariadb) | supported |
| `mysql` | MySQL | database | metadata, direct_engine | mysql | trino (mysql) | supported |
| `nessie` | Project Nessie | iceberg | metadata | — | trino (iceberg) | supported |
| `oracle` | Oracle Database | database | metadata, direct_engine | oracle | trino (oracle) | supported |
| `polaris` | Apache Polaris | iceberg | metadata | — | trino (iceberg) | supported |
| `postgresql` | PostgreSQL | database | metadata, direct_engine | postgresql | trino (postgresql) | supported |
| `sqlite` | SQLite | database | metadata, direct_engine | sqlite | — | unsupported (BALEIA_SQLITE_ROOT not set) |
| `sqlserver` | SQL Server | database | metadata, direct_engine | sqlserver | trino (sqlserver) | supported |
| `starrocks` | StarRocks | engine | federated_engine | starrocks | — | unsupported (planned after v1 (#30)) |
| `trino` | Trino | engine | metadata, federated_engine | trino | — | supported |
| `unity` | Unity Catalog | iceberg | metadata | — | trino (iceberg) | supported |

## `clickhouse` — ClickHouse

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `8123` | 8123 for HTTP, 8443 for HTTPS |
| `secure` | bool |  | `false` |  |
| `database` | text |  | `default` |  |
| `username` | text |  | `default` |  |
| `password` | secret |  |  |  |

## `doris` — Apache Doris

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `9030` |  |
| `username` | text |  |  |  |
| `password` | secret |  |  |  |

## `flightsql` — Arrow Flight SQL

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int | yes |  |  |
| `tls` | bool |  | `true` |  |
| `auth` | select |  | `none` | one of `none`, `basic`, `token` |
| `username` | text |  |  | Basic authentication |
| `password` | secret |  |  | Basic authentication |
| `token` | secret |  |  | Token authentication |

## `hive` — Hive Metastore

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `metastore_uri` | text | yes |  | Thrift URI of the Hive Metastore, e.g. thrift://hms:9083 |
| `storage.type` | select |  | `s3` | one of `s3`. Object storage the table data resides on (S3-compatible in v1) |
| `storage.endpoint` | text |  |  | e.g. http://rustfs:9000; empty uses the AWS default for the region |
| `storage.region` | text |  |  | e.g. us-east-1 |
| `storage.path_style` | bool |  | `false` | Required by most S3-compatible stores (RustFS, MinIO) |
| `storage.access_key` | text |  |  |  |
| `storage.secret_key` | secret |  |  |  |

## `iceberg` — Iceberg REST catalog

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | Base URI of the Iceberg REST API (the one /v1/config is under) |
| `warehouse` | text |  |  |  |
| `token` | secret |  |  |  |
| `client_id` | text |  |  |  |
| `client_secret` | secret |  |  |  |
| `oauth2_server_uri` | url |  |  | Default: <REST URI>/v1/oauth/tokens |
| `scope` | text |  |  |  |

## `lakekeeper` — Lakekeeper

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | Server root, e.g. http://lakekeeper:8181 (without /catalog) |
| `auth_mode` | select |  | `none` | one of `none`, `oauth2`. none is for servers with authorization off |
| `client_id` | text |  |  | Required with auth_mode=oauth2 |
| `client_secret` | secret |  |  | Required with auth_mode=oauth2 |
| `token_endpoint` | url |  |  | The IdP token endpoint (not the Lakekeeper URL); required with auth_mode=oauth2 |
| `scope` | text |  |  |  |

## `mariadb` — MariaDB

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `3306` |  |
| `database` | text |  |  |  |
| `tls` | select |  | `preferred` | one of `preferred`, `true`, `skip-verify`, `false` |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## `mysql` — MySQL

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `3306` |  |
| `database` | text |  |  |  |
| `tls` | select |  | `preferred` | one of `preferred`, `true`, `skip-verify`, `false` |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## `nessie` — Project Nessie

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | Server root, e.g. http://nessie:19120 (without /iceberg) |
| `warehouse` | text |  |  | Default: the server's default warehouse |
| `token` | secret |  |  |  |

## `oracle` — Oracle Database

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `1521` |  |
| `service_name` | text | yes |  | e.g. FREEPDB1 |
| `cancel_with_alter_system` | bool |  | `false` | Requires the ALTER SYSTEM permission; lets a running statement be cancelled from another process |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## `polaris` — Apache Polaris

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | Server root, e.g. http://polaris:8181 (without /api/catalog) |
| `realm` | text |  | `POLARIS` |  |
| `client_id` | text | yes |  |  |
| `client_secret` | secret | yes |  |  |

## `postgresql` — PostgreSQL

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `5432` |  |
| `database` | text |  | `postgres` | Database to connect to; every database the user may connect to is listed |
| `sslmode` | select |  | `prefer` | one of `disable`, `allow`, `prefer`, `require`, `verify-ca`, `verify-full` |
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |

## `sqlite` — SQLite

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `path` | file_path | yes |  | Path inside BALEIA_SQLITE_ROOT (relative to it, or absolute) |
| `read_write` | bool |  | `false` | Takes effect only when the server sets BALEIA_SQLITE_ALLOW_WRITE=true |

## `sqlserver` — SQL Server

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

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `9030` |  |
| `username` | text |  |  |  |
| `password` | secret |  |  |  |

## `trino` — Trino

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | e.g. http://trino:8080 |
| `service_user` | text |  | `baleia` | Identity for metadata reads and service statements |
| `admin_user` | text |  | `baleia-admin` | Identity that cancels queries; never runs user SQL |
| `username` | text |  |  | Password authentication (HTTPS only); must be allowed to impersonate the identities Baleia sends |
| `password` | secret |  |  |  |

## `unity` — Unity Catalog

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | url | yes |  | Server root, e.g. http://unity:8080 or a Databricks workspace URL |
| `token` | secret |  |  | OSS token or a Databricks personal access token |
| `iceberg_path` | text |  | `/api/2.1/unity-catalog/iceberg` | Override for Databricks: /api/2.1/unity-catalog/iceberg-rest |
