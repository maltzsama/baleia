# Configuration

Baleia (the API and the worker) is configured with environment variables. The defaults work for local development with the provided `docker-compose.yaml`. In Kubernetes, the Helm chart sets them from `config` and its Secret (`deploy/helm/baleia/values.yaml`).

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `DATABASE_URL` | `postgresql://baleia:baleia@localhost:5432/baleia?sslmode=disable` | PostgreSQL connection string |
| `JWT_SECRET` | `change-me-in-production` | Key for signing JWTs |
| `BALEIA_ENCRYPTION_KEY` | _(unset)_ | Key that encrypts stored connection credentials (≥ 32 characters, must differ from `JWT_SECRET`). Required once any credential is stored; the API refuses to start without it. Derived with HKDF-SHA256; each value is bound to its connection row |
| `BALEIA_ENCRYPTION_KEY_PREVIOUS` | _(unset)_ | Previous encryption key, set only during rotation: values are read with the current or previous key and always written with the current one |
| `TEMPORAL_HOST` | `localhost:7233` | Temporal server address |
| `TRINO_URL` | `http://localhost:8088` | Trino coordinator REST endpoint. At API boot it also seeds the default Trino engine (with the service and admin identities below) and keeps it in step while that engine's connection is system-owned; engine endpoints live in the database (`engines`, [ADR 0002](adr/0002-connections-engines-catalogs-bindings.md)). Trino is dialed through the connections dialer like every engine, so a loopback `TRINO_URL` needs `BALEIA_CONNECTIONS_ALLOW_LOOPBACK=true` (set by `make dev`) |
| `POLARIS_URL` | `http://localhost:8181` | Polaris REST API endpoint |
| `POLARIS_CLIENT_ID` / `POLARIS_CLIENT_SECRET` | _(unset)_ | Credentials stored for a `polaris` connection created without any; both must be set |
| `PUBLIC_URL` | `http://localhost:8080` | Externally reachable URL (used for presigned links) |
| `S3_ENDPOINT` | `localhost:9000` | S3-compatible storage endpoint (RustFS) |
| `S3_ACCESS_KEY` | `rustfsadmin` | S3 access key |
| `S3_SECRET_KEY` | `rustfsadmin` | S3 secret key |
| `S3_BUCKET` | `baleia` | S3 bucket for query results |
| `S3_USE_SSL` | `false` | Enable TLS for S3 connections |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASSWORD` / `SMTP_FROM` | — | Outbound email (password reset, invites) |
| `BALEIA_DEFAULT_LOCALE` | `en` | Fallback language for transactional e-mails when the recipient has no `users.locale` (`en` or `pt-BR`) |
| `RATE_LIMIT_RPS` | `60` | Max requests per second (rate limiter) |
| `RATE_LIMIT_BURST` | `10` | Rate limiter burst size |
| `BALEIA_STATEMENT_TIMEOUT` | `30m` | Per-statement execution timeout; enforced by the worker and by the engine (Trino: `query_max_execution_time`) |
| `BALEIA_RECONCILE_INTERVAL` | unset | Apply binding reconciliation on a schedule (at least `5m`); unset: no schedule, only `POST /engines/{id}/reconcile` |
| `BALEIA_RECONCILE_MAX_ACTIONS` | `20` | Changes one reconcile run may apply; the rest are reported |
| `BALEIA_METADATA_SYNC_INTERVAL` | `15m` | How often every catalog's metadata snapshot is refreshed (at least `5m`) |
| `BALEIA_EXECUTION_TIMEOUT` | `2h` | Cap for a whole execution: all statements run in one engine session, so a script may take statements × statement timeout, capped by this |
| `BALEIA_RESULT_MAX_ROWS` | `1000000` | Max rows buffered per statement result before truncation |
| `BALEIA_LOB_MAX_BYTES` | `1048576` | Max bytes an Oracle engine reads from one CLOB/NCLOB/BLOB value into a result (1 MiB) |
| `BALEIA_RESULT_MAX_BYTES` | `536870912` | Max Arrow bytes per statement result before truncation |
| `BALEIA_SCHEDULES_ENABLED` | `true` | Ensure recurring Temporal Schedules (Lens pollers, table health, retriever) at worker boot |
| `BALEIA_CONNECTIONS_DENY_CIDRS` | _(unset)_ | Comma-separated CIDRs that connections may never reach, added to the built-in deny list (loopback, link-local incl. cloud metadata `169.254.169.254`, `0.0.0.0/8`) — see [ADR 0011](adr/0011-direct-connections-security.md) |
| `BALEIA_CONNECTIONS_ALLOW_CIDRS` | _(unset)_ | If set, connections may only reach these comma-separated CIDRs (the deny list still wins) |
| `BALEIA_LOG_LEVEL` | `info` | Lowest level the API, the worker and `cmd/migrate` log: `debug`, `info`, `warn` or `error`. They log JSON lines to stdout; a line logged during a traced request or activity carries its `trace_id` and `span_id` (#379) |
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
| `BALEIA_CONNECTIONS_ALLOW_LOOPBACK` | `false` | Let connections reach loopback addresses. For local development only (`make dev` / `make worker` set it); never in a shared deployment |
| `BALEIA_TRINO_ADMIN_USER` | `baleia_admin` | Trino identity for catalog DDL/reconcile/maintenance (never user SQL) |
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

## Credential encryption

Connection credentials are stored as `v1:<key-id>:<base64>` envelopes: AES-256-GCM under a key derived (HKDF-SHA256) from `BALEIA_ENCRYPTION_KEY`, with the connection id as additional authenticated data, so a ciphertext copied to another connection does not open. A value that cannot be opened is reported as `credentials_unreadable`; it is never used as if it were the secret.

- **Fresh install:** set `BALEIA_ENCRYPTION_KEY` before saving the first credential. Without it the API starts with a warning and refuses to store credentials.
- **Upgrading from `JWT_SECRET`-encrypted credentials:** set `BALEIA_ENCRYPTION_KEY`, keep the existing `JWT_SECRET`, then run the one-shot, idempotent migration (`make migrate-credentials`, or `/migrate -cmd=credentials` in the image). It requires a valid `JWT_SECRET` to open the legacy values. Rows that cannot be decrypted are listed by id and left untouched; re-enter those credentials.
- **Rotating the key:** move the old value to `BALEIA_ENCRYPTION_KEY_PREVIOUS` and set the new `BALEIA_ENCRYPTION_KEY`. Values are read with either key, but reading never re-seals them. Run `make migrate-credentials` (or `/migrate -cmd=credentials`) to move every envelope to the new key; only after it reports no failures is it safe to drop `BALEIA_ENCRYPTION_KEY_PREVIOUS`. Envelopes left on the old key become `credentials_unreadable` once it is removed.

See also [secrets.md](secrets.md) for external secret references and the catalog registry at rest.
