# Connection providers

<!-- Generated from internal/connections/providers. Do not edit by hand:
     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->

Every connection kind Baleia knows, what it can be used for, and its configuration.
See [ADR 0002](adr/0002-connections-engines-catalogs-bindings.md) for the model and
[ADR 0011](adr/0011-direct-connections-security.md) for network safety.

Which federated engines can bind each kind is declared by binders and is added to this
page when they land (#80).

| Kind | Label | Family | Roles | Engine kind | Status |
|---|---|---|---|---|---|
| `clickhouse` | ClickHouse | database | metadata, direct_engine | clickhouse | supported |
| `iceberg` | Iceberg REST catalog | iceberg | metadata | — | supported |
| `mariadb` | MariaDB | database | metadata, direct_engine | mariadb | supported |
| `mysql` | MySQL | database | metadata, direct_engine | mysql | supported |
| `nessie` | Project Nessie | iceberg | metadata | — | supported |
| `oracle` | Oracle Database | database | metadata, direct_engine | oracle | supported |
| `polaris` | Apache Polaris | iceberg | metadata | — | supported |
| `postgresql` | PostgreSQL | database | metadata, direct_engine | postgresql | supported |
| `sqlite` | SQLite | database | metadata, direct_engine | sqlite | unsupported (BALEIA_SQLITE_ROOT not set) |
| `sqlserver` | SQL Server | database | metadata, direct_engine | sqlserver | supported |

## `clickhouse` — ClickHouse

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `host` | text | yes |  |  |
| `port` | int |  | `8123` | 8123 for HTTP, 8443 for HTTPS |
| `secure` | bool |  | `false` |  |
| `database` | text |  | `default` |  |
| `username` | text |  | `default` |  |
| `password` | secret |  |  |  |

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
| `username` | text | yes |  |  |
| `password` | secret |  |  |  |
