# Configuration

Baleia (the API and the worker) is configured with environment variables. The defaults work for local development with the provided `docker-compose.yaml`. In Kubernetes, the Helm chart sets them from `config` and its Secret (`deploy/helm/baleia/values.yaml`).

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `DATABASE_URL` | `postgresql://baleia:baleia@localhost:5432/baleia?sslmode=disable` | Connection string of Baleia's own store. The DSN selects the GORM dialector: a `postgres://` URL for PostgreSQL, or the MySQL go-sql-driver DSN (`user:pass@tcp(host:port)/db`) for MySQL/MariaDB |
| `JWT_SECRET` | `change-me-in-production` | Key for signing JWTs |
| `BALEIA_ENCRYPTION_KEY` | _(unset)_ | Key that encrypts stored connection credentials (≥ 32 characters, must differ from `JWT_SECRET`). Required once any credential is stored; the API refuses to start without it. Derived with HKDF-SHA256; each value is bound to its connection row |
| `BALEIA_ENCRYPTION_KEY_PREVIOUS` | _(unset)_ | Previous encryption key, set only during rotation: values are read with the current or previous key and always written with the current one |
| `BALEIA_ACCESS_TOKEN_TTL` | `15m` | Lifetime of an access token |
| `BALEIA_REFRESH_TOKEN_TTL` | `720h` (30 days) | Lifetime of a refresh token; a password change or reset, a role change, a suspension or a removal revokes them earlier |
| `BALEIA_RESET_TOKEN_TTL` | `30m` | Lifetime of a password-reset link |
| `BALEIA_BCRYPT_COST` | `12` | Password hashing cost, between 10 and 31; higher is slower to sign in and to brute-force |
| `BALEIA_ALLOW_AMBIENT_AWS_CREDENTIALS` | `false` | Lets an AWS Glue connection with no keys or role of its own use the host's default AWS credential chain. Off by default: otherwise any connection owner could use the platform's IAM role |
| `BALEIA_GLUE_ALLOW_INSECURE_ENDPOINT` | `false` | Lets an AWS Glue connection use an `http://` endpoint override (a local emulator such as moto). An external AWS Glue catalog is always `https` and never needs this |
| `BALEIA_AUDIT_EXPORT_URL`, `BALEIA_AUDIT_EXPORT_INDEX`, `BALEIA_AUDIT_EXPORT_AUTH`, `BALEIA_AUDIT_EXPORT_USER`, `BALEIA_AUDIT_EXPORT_PASSWORD`, `BALEIA_AUDIT_EXPORT_TOKEN`, `BALEIA_AUDIT_EXPORT_BATCH`, `BALEIA_AUDIT_EXPORT_INTERVAL`, `BALEIA_AUDIT_EXPORT_CA_FILE` | export off | Shipping the audit trail to OpenSearch/Elasticsearch: defaults, authentication modes and behavior in [Audit export](operations.md#audit-export) |
| `TEMPORAL_HOST` | `localhost:7233` | Temporal server address |
| `BALEIA_TEMPORAL_UI_URL` | — | Temporal UI base URL; when set, a maintenance run links to its workflow there |
| `BALEIA_TEMPORAL_NAMESPACE` | `default` | Temporal namespace the worker's workflows live in (used in the UI link) |
| `TRINO_URL` | `http://localhost:8088` | Trino coordinator REST endpoint. At API boot it also seeds the default Trino engine (with the service and admin identities below) and keeps it in step while that engine's connection has no owner (`created_by` is NULL); engine endpoints live in the database (`engines`, [ADR 0002](adr/0002-connections-engines-catalogs-bindings.md)). Trino is dialed through the connections dialer like every engine, so a loopback `TRINO_URL` needs its loopback range removed from `BALEIA_CONNECTIONS_DENY_CIDRS` |
| `POLARIS_URL` | `http://localhost:8181` | Polaris REST API endpoint |
| `POLARIS_CLIENT_ID` / `POLARIS_CLIENT_SECRET` | _(unset)_ | Credentials stored for a `polaris` connection created without any; both must be set |
| `PUBLIC_URL` | `http://localhost:8080` | Externally reachable URL (used for presigned links) |
| `S3_ENDPOINT` | `localhost:9000` | Endpoint of any S3-compatible store (AWS S3, RustFS, MinIO, Ceph RGW, Google Cloud Storage, Oracle Object Storage), as host[:port] |
| `S3_ACCESS_KEY` | `rustfsadmin` | S3 access key |
| `S3_SECRET_KEY` | `rustfsadmin` | S3 secret key |
| `S3_BUCKET` | `baleia` | S3 bucket for query results |
| `S3_REGION` | _(empty)_ | SigV4 signing region. Empty discovers it (AWS, RustFS, MinIO); set it for stores that require it, e.g. `sa-saopaulo-1` on Oracle Object Storage |
| `S3_USE_SSL` | `false` | Enable TLS for S3 connections |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASSWORD` / `SMTP_FROM` | — | Outbound email (password reset, invites) |
| `BALEIA_DEFAULT_LOCALE` | `en` | Fallback language for transactional e-mails when the recipient has no `users.locale` (`en` or `pt-BR`) |
| `RATE_LIMIT_RPS` | `60` | Max requests per second (rate limiter) |
| `RATE_LIMIT_BURST` | `10` | Rate limiter burst size |
| `BALEIA_STATEMENT_TIMEOUT` | `30m` | Per-statement execution timeout; enforced by the worker and by the engine (Trino: `query_max_execution_time`) |
| `BALEIA_RECONCILE_INTERVAL` | unset | Apply binding reconciliation on a schedule (at least `5m`); unset: no schedule, only `POST /engines/{id}/reconcile` |
| `BALEIA_RECONCILE_MAX_ACTIONS` | `20` | Changes one reconcile run may apply; the rest are reported |
| `BALEIA_METADATA_SYNC_INTERVAL` | `15m` | How often every catalog's metadata snapshot is refreshed (at least `5m`) |
| `BALEIA_MAINTENANCE_MAX_CONCURRENT_RUNS` | `4` | Automatic Iceberg maintenance runs in progress at once, across tables (#500) |
| `BALEIA_ICEBERG_MAINTENANCE_MIN_RETENTION` | `1h` | Minimum retention projected into managed Iceberg catalogs for `expire_snapshots` and `remove_orphan_files`; table parameters below the floor a catalog has loaded are refused (at least `1m`). Changing it, including the first deploy that introduces it, changes the catalog version: every managed Iceberg catalog is re-created on its engine once, and until that reconcile the old floor (Trino's `7d`) still applies |
| `BALEIA_EXECUTION_TIMEOUT` | `2h` | Cap for a whole execution: all statements run in one engine session, so a script may take statements × statement timeout, capped by this |
| `BALEIA_WORKLOAD_ENABLED` | `false` | Record each run's workload class and admit it through its engine queue (#399); off, runs start immediately and are never `queued` |
| `BALEIA_BI_GATEWAY_ADDR` | _(unset)_ | Start the BI gateway listener on this address, e.g. `:8443`; unset disables the gateway (ADR 0017). It serves Trino's protocol at the server root, not under `/api/v1` |
| `BALEIA_BI_GATEWAY_URL` | _(unset)_ | Externally reachable base URL the gateway rewrites Trino `nextUri`/`infoUri`/`partialCancelUri` to, e.g. `https://bi.example.com`. Set it to an **HTTPS** URL when TLS terminates before the gateway; unset derives the scheme and host from each request (local use only). A startup warning is logged when it is unset |
| `BALEIA_BI_GATEWAY_SECRET` | _(unset)_ | HMAC key shared by every replica that signs and validates the gateway's rewritten Trino URIs; required (at least 32 characters) when `BALEIA_BI_GATEWAY_ADDR` is set. It must be the same value on every replica and must not be per-pod |
| `BALEIA_BI_MAX_CONCURRENT_PER_KEY` | `5` | Running BI statements one key may have; a new statement over it gets HTTP 429. `0` disables the per-key limit |
| `BALEIA_BI_MAX_CONCURRENT_PER_WORKSPACE` | `20` | Running BI statements one workspace may have across its keys; over it HTTP 429. `0` disables the per-workspace limit |
| `BALEIA_TRINO_RESOURCE_GROUPS_ENVIRONMENT` | `production` | Environment the managed Trino resource groups are published under (#635); must match the handed-over cluster's `node.environment` |
| `BALEIA_WORKLOAD_ADAPTIVE_QUEUED_THRESHOLD` | `20` | With workload management on, admit only `interactive` runs while the Trino cluster has more queued queries than this (`0` disables the trigger; [adaptive admission](operations.md#adaptive-admission-trino)) |
| `BALEIA_WORKLOAD_ADAPTIVE_BLOCKED_THRESHOLD` | `10` | The same, for blocked queries |
| `BALEIA_WORKLOAD_ADAPTIVE_MEMORY_HEADROOM_PCT` | `10` | The same, when the cluster's free memory falls below this percentage |
| `BALEIA_RESULT_MAX_ROWS` | `1000000` | Max rows buffered per statement result before truncation |
| `BALEIA_LOB_MAX_BYTES` | `1048576` | Max bytes an Oracle engine reads from one CLOB/NCLOB/BLOB value into a result (1 MiB) |
| `BALEIA_RESULT_MAX_BYTES` | `536870912` | Max Arrow bytes per statement result before truncation |
| `BALEIA_SCHEDULES_ENABLED` | `true` | Ensure recurring Temporal Schedules (Lens pollers, table health, retriever) at worker boot |
| `BALEIA_CONNECTIONS_DENY_CIDRS` | _(unset)_ | Comma-separated CIDRs that connections may never reach. The dialer hardcodes no ranges, so this is the operator's deny policy; it always wins over `BALEIA_CONNECTIONS_ALLOW_CIDRS`. The Helm chart ships `127.0.0.0/8,::1/128,0.0.0.0/8,::/128` as its default; cloud-metadata ranges (e.g. `169.254.0.0/16` on OKE/AWS) are **not** denied by default — add them for your environment. See [ADR 0011](adr/0011-direct-connections-security.md) |
| `BALEIA_CONNECTIONS_ALLOW_CIDRS` | _(unset)_ | If set, connections may only reach these comma-separated CIDRs. An explicit `BALEIA_CONNECTIONS_DENY_CIDRS` entry still wins |
| `BALEIA_CONNECTIONS_CREATE` | `admins` | Who may create and edit connections: `admins` (the default), or `everyone`. The CIDR lists bind admins too; `everyone` is for deployments where every authenticated user may point connections at approved hosts |
| `BALEIA_LOG_LEVEL` | `info` | Lowest level the API, the worker and `cmd/migrate` log: `debug`, `info`, `warn` or `error`. They log JSON lines to stdout; a line logged during a traced request or activity carries its `trace_id` and `span_id` (#379) |
| `BALEIA_PLUGIN_VERSION` | `0.10.0` | The catalog-store plugin version Baleia expects on Trino; **Settings → System** compares it with what the plugin reports. Keep it equal to the chart's `trino.plugin.version` |
| `BALEIA_TRINO_EVENTS_TOKEN` | _(unset)_ | Bearer token Trino's HTTP event listener sends to `POST /internal/trino/query-events` (at least 32 characters). Set, the API records each completed query as it ends; unset, the endpoint does not exist. The chart wires both sides with `trino.queryEvents.enabled` (#406) |
| `BALEIA_PPROF_ADDR` | _(unset)_ | Where the API and the worker serve the Go runtime profiles (`/debug/pprof/`), e.g. `127.0.0.1:6060`; unset serves none. The listener has no authentication: bind it to loopback or a port only reachable from the pod (`kubectl port-forward`), never to a public interface (#380) |
| `BALEIA_METRICS_ENABLED` | `true` | Serve Prometheus metrics at the API's `/metrics` (the catalog is `internal/metrics`, #87) |
| `BALEIA_METRICS_TOKEN` | _(unset)_ | When set, `/metrics` (API and worker) needs `Authorization: Bearer <token>`; it is never behind the user JWT. A credential: in the chart it is the optional Secret key `BALEIA_METRICS_TOKEN` (`secrets.values` or `existingSecret`), never `config` |
| `WORKER_HEALTH_PORT` | `8081` | The worker's liveness listener (`/healthz`) |
| `BALEIA_WORKER_METRICS_ADDR` | `:9090` | The worker's listener for `/metrics` (Temporal SDK, pool, build) and `/healthz`; empty disables it |
| `BALEIA_DB_MAX_CONNS` | api `20`, worker `10` | Size of Baleia's own database pool (observable as `baleia_db_pool_*`) |
| `BALEIA_DB_MIN_CONNS` | `2` | Connections the pool keeps open |
| `BALEIA_DB_MAX_CONN_LIFETIME` | `1h` | Recycle pool connections after this long |
| `BALEIA_DB_MAX_CONN_IDLE_TIME` | `30m` | Close pool connections idle for this long |
| `BALEIA_DB_HEALTH_CHECK_PERIOD` | `1m` | How often the pool checks idle connections |
| `BALEIA_DB_STATEMENT_TIMEOUT` | `1m` | `statement_timeout` on Baleia's own sessions (not user queries, which use `BALEIA_STATEMENT_TIMEOUT`); `0` disables it |
| `BALEIA_DB_IDLE_IN_TX_TIMEOUT` | `1m` | `idle_in_transaction_session_timeout`: a leaked transaction is ended and its locks released; `0` disables it |
| `BALEIA_DB_LOCK_TIMEOUT` | `30s` | `lock_timeout` on Baleia's own sessions; `0` disables it |
| `BALEIA_HTTP_READ_HEADER_TIMEOUT` | `10s` | Time a client has to send the request headers to the API |
| `BALEIA_HTTP_IDLE_TIMEOUT` | `2m` | How long an idle keep-alive connection to the API stays open |
| `BALEIA_HTTP_SHUTDOWN_TIMEOUT` | `20s` | How long the API drains in-flight requests on shutdown (keep it below the pod's termination grace period); `0` waits without a deadline |
| `BALEIA_SECRET_FILE_BASE_DIR` | _(unset)_ | Directory `@baleia-secret[file:name]` references resolve under ([secrets.md](secrets.md)); unset refuses `file:` references. Read once at startup |
| `BALEIA_SQLITE_ROOT` | _(unset)_ | Directory SQLite connections are confined to (symlinks resolved); unset disables the SQLite provider. The API and the worker must see the same files (shared volume) |
| `BALEIA_SQLITE_ALLOW_WRITE` | `false` | Let a SQLite connection that sets `read_write` open its file read-write; otherwise files open read-only |
| `BALEIA_TRINO_ADMIN_USER` | `baleia_admin` | Trino identity for catalog DDL/reconcile/maintenance (never user SQL) |
| `BALEIA_TRINO_PASSWORD` | — | Password of `BALEIA_TRINO_ADMIN_USER` on a coordinator that requires authentication. Baleia logs in with it on every request to `TRINO_URL` (default engine, catalog DDL, Lens) and impersonates the service and per-user identities, so Trino's access control must let that login impersonate them. Needs an `https` `TRINO_URL` and `BALEIA_ENCRYPTION_KEY`: the default engine stores a sealed reference to this variable, never the password. Unset: no authentication |
| `BALEIA_TRINO_CA_FILE` | — | PEM bundle trusted for the coordinator's certificate, besides the system roots (a private CA or a self-signed certificate). With the Helm chart, mount the bundle with `extraVolumes` and `extraVolumeMounts` |
| `BALEIA_TRINO_SERVICE_USER` | `baleia` | Worker fallback identity for executions |
| `BALEIA_TRINO_USER_IDENTITY` | `uuid` | Map Baleia users to Trino users: `uuid`→`u_<uuid>` or `email` |
| `BALEIA_AGENTS_ENABLED` | `false` | Mount the agent management API and `/mcp` ([mcp.md](mcp.md)). Off by default |
| `BALEIA_AGENT_TOKEN_DEFAULT_TTL` | `720h` | Default lifetime of an agent token when `expires_in_days` is omitted |
| `BALEIA_AGENT_TOKEN_MAX_TTL` | `2160h` | Maximum accepted agent-token lifetime (90 days) |
| `BALEIA_AGENT_MAX_ROWS` | `5000` | Ceiling for an agent policy's `max_rows_returned` |
| `BALEIA_AGENT_MAX_RESULT_BYTES` | `1048576` | Ceiling for `max_result_bytes` (1 MiB) |
| `BALEIA_AGENT_MAX_QUERY_RUNTIME_SECONDS` | `900` | Ceiling for `max_query_runtime_seconds` |
| `BALEIA_AGENT_MAX_CONCURRENT_QUERIES` | `10` | Ceiling for `max_concurrent_queries` |
| `BALEIA_AGENT_MAX_QUERIES_PER_HOUR` | `2000` | Ceiling for `max_queries_per_hour` |
| `BALEIA_AGENT_MAX_TOOL_CALLS_PER_MINUTE` | `600` | Ceiling for `max_tool_calls_per_minute` |
| `BALEIA_MCP_ALLOWED_ORIGINS` | _(unset)_ | Comma-separated `Origin` allow-list for `/mcp`; empty rejects any request that carries one |

## Store backend

Baleia's own metadata (users, workspaces, content, models, connections, catalogs, audit, snapshots) lives in PostgreSQL by default. MySQL 8 and MariaDB are supported through GORM's MySQL dialector. The `DATABASE_URL` DSN alone selects the dialector; there is no separate backend setting.

| Backend | `DATABASE_URL` example |
|---|---|
| PostgreSQL | `postgresql://baleia:baleia@localhost:5432/baleia?sslmode=disable` |
| MySQL 8 / MariaDB | `baleia:baleia@tcp(localhost:3306)/baleia?parseTime=true` |

Baleia infers the backend from the DSN: `postgres://`/`postgresql://` → PostgreSQL, `@tcp(`/`@unix(` → MySQL. A MySQL/MariaDB DSN must use the go-sql-driver form, not a URL.

The schema is created and evolved by the versioned Go migrations in `internal/store` (`store-up`, also applied at API start). Both backends are exercised by the test suite.

Caveats:

- ADR 0020 removed or redesigned what GORM cannot express without handwritten SQL: no PL/pgSQL functions or triggers (effective roles are computed in Go), no table partitions (audit retention uses plain tables), no `pg_trgm` (search is a portable Go implementation), and no advisory locks (transactions and row locks instead).
- The bundled Trino catalog-store plugin reads its registry over PostgreSQL and logs in as the `baleia_trino` role, which the API and `store-up` bootstrap on PostgreSQL. A MySQL/MariaDB store serves the API and worker, but does not provide that plugin's registry path.
- **The query-execution path is unchanged.** User SQL, Trino and the direct engines keep their own dialect layer and never go through the store; only Baleia's own metadata does.

## Credential encryption

Connection credentials are stored as `v1:<key-id>:<base64>` envelopes: AES-256-GCM under a key derived (HKDF-SHA256) from `BALEIA_ENCRYPTION_KEY`, with the connection id as additional authenticated data, so a ciphertext copied to another connection does not open. A value that cannot be opened is reported as `credentials_unreadable`; it is never used as if it were the secret.

- **Fresh install:** set `BALEIA_ENCRYPTION_KEY` before saving the first credential. Without it the API starts with a warning and refuses to store credentials.
- **Upgrading from `JWT_SECRET`-encrypted credentials:** set `BALEIA_ENCRYPTION_KEY`, keep the existing `JWT_SECRET`, then run the one-shot, idempotent migration (`make migrate-credentials`, or `/migrate -cmd=credentials` in the image). It requires a valid `JWT_SECRET` to open the legacy values. Rows that cannot be decrypted are listed by id and left untouched; re-enter those credentials.
- **Rotating the key:** move the old value to `BALEIA_ENCRYPTION_KEY_PREVIOUS` and set the new `BALEIA_ENCRYPTION_KEY`. Values are read with either key, but reading never re-seals them. Run `make migrate-credentials` (or `/migrate -cmd=credentials`) to move every envelope to the new key; only after it reports no failures is it safe to drop `BALEIA_ENCRYPTION_KEY_PREVIOUS`. Envelopes left on the old key become `credentials_unreadable` once it is removed.

See also [secrets.md](secrets.md) for external secret references and the catalog registry at rest.
