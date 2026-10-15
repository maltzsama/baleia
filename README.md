# Baleia

Self-hosted SQL IDE and Apache Iceberg catalog management platform for Trino.

Baleia is the **UI and control plane** for a Trino-based lakehouse: it manages catalog connections, sends every query to Trino, and displays the results. It does not implement its own query engine, Flight SQL proxy, or JDBC layer — Trino handles all catalog federation, and access control lives in the catalog manager (Polaris), not in Baleia.

> **Status:** actively developed, solo project. The backend API is substantially implemented (auth, API keys, users, teams, workspaces, catalog connections, catalog explorer). Query execution (`QueryFolder`/`QueryFile`/`Execution`), the `Lens` observability service, and the `Retriever` refresh scheduler are in progress. See [Roadmap](#roadmap).

## What it does

- **SQL editor** — multi-tab, CodeMirror 6 syntax highlighting, autocomplete from live catalogs, formatting, EXPLAIN
- **Query execution** — Temporal workflows run each statement of a multi-statement query sequentially against Trino, stopping on the first error
- **Live results** — streamed via WebSocket as pure execution events (`running` → `progress` → `statement_success`/`statement_error` → `finished`); the final event carries a presigned URL to the result set, no data goes over the socket itself
- **Catalog management** — register Polaris, Nessie, Glue, or Hive catalogs through a common connector interface; Baleia creates the matching Trino catalog automatically
- **Iceberg maintenance** — `OPTIMIZE`, `EXPIRE_SNAPSHOTS`, `REMOVE_ORPHAN_FILES`, and similar operations, executed as SQL against Trino (not through catalog-manager APIs)
- **Multi-tenant workspaces** with per-catalog role-based access
- **Saved queries** organized in folders

## Architecture

Architectural decisions are recorded as ADRs in [`docs/adr/`](docs/adr/README.md). They are the
current truth: where this README or the code disagrees with an ADR, the ADR's "Current compliance"
section says which issue closes the gap.

```mermaid
graph TB
    subgraph Frontend
        SPA["SvelteKit SPA<br/>(Svelte 5 + TS + Tailwind v4)"]
    end

    subgraph Backend["Backend (Go)"]
        API["API Server<br/>Chi + oapi-codegen"]
        WS["WebSocket Handler<br/>(event channel only)"]
        TL["Temporal Client"]
    end

    subgraph Worker["Temporal Worker"]
        EX["Execute Statement"]
        SR["Store Results<br/>(Arrow → S3)"]
        CE["Complete Execution"]
    end

    TR["Trino Coordinator"]

    subgraph Storage
        PG[("PostgreSQL 18<br/>Metadata")]
        S3[("SeaweedFS / Ceph RGW<br/>Result bucket")]
    end

    subgraph Catalogs["Catalog Managers"]
        POLARIS["Polaris"]
        NESSIE["Nessie"]
        HIVE["Hive / Glue"]
    end

    SPA -- "REST" --> API
    SPA -- "WS (execution events)" --> WS
    API --> TL
    TL -- "StartWorkflow" --> EX
    EX -- "Trino REST API" --> TR
    EX --> SR
    SR --> S3
    EX --> CE
    CE --> PG
    WS -- "poll execution state" --> PG
    API -- "catalog CRUD" --> PG
    TR --> POLARIS
    TR --> NESSIE
    TR --> HIVE
```

### Query flow

1. User writes SQL in the editor and hits Run
2. The API starts a Temporal workflow, delegated to a worker
3. The worker splits the SQL into statements and runs each sequentially against Trino's REST API
4. Progress is emitted over WebSocket as events only (`statement_running` → `progress` → `statement_success`/`statement_error`)
5. On completion, results are serialized to Arrow and uploaded to the S3-compatible result bucket; the final WebSocket event carries a presigned URL
6. The frontend fetches results directly from that URL — no proxying of result bytes through the API

### Design boundaries

- **No Flight SQL proxy, no JDBC implementation, no cross-database joins.** External clients that need direct SQL access (Python, JDBC/ODBC tools) talk to Trino directly; Polaris enforces their permissions.
- **Catalog managers ≠ Trino connectors.** Registering an external Trino connector (MySQL, PostgreSQL, Delta Lake, etc.) as an ad-hoc data source is a separate, planned capability (see Roadmap) — it is not the same thing as an Iceberg catalog manager.

### Trino security model

- **Three identities.** `BALEIA_TRINO_ADMIN_USER` (default `baleia_admin`) runs catalog DDL, reconciliation and Lens maintenance — it is **never** used to run user SQL. `BALEIA_TRINO_SERVICE_USER` (default `baleia`) is the worker's fallback identity. User queries run as the logged-in Baleia user's Trino identity (`BALEIA_TRINO_USER_IDENTITY`: `uuid` → `u_<uuid>`, or `email`) so `system.runtime.queries.user` reflects the real user.
- **The catalog guard is the control.** The `baleia-catalog-guard` access control (shipped by the catalog-store plugin) denies `CREATE/DROP CATALOG` and `SET SESSION AUTHORIZATION` to non-admin identities at the Trino level. The SQL editor's `IsForbiddenInEditor` gate (catalog DDL, `EXECUTE IMMEDIATE` smuggling catalog DDL, `SET SESSION AUTHORIZATION`) is UX and defense in depth, never the security boundary.
- **Trino is private.** The default compose stack does not publish Trino on the host; only the API/worker reach it via `expose`. Copy `docker-compose.override.yml.example` to publish it for local debugging.
- **Unauthenticated admin (network assumption).** Baleia's Trino has no authentication, so the admin identity reaches the guard without a verified principal. Compose and the minikube dev values set `baleia.allow-unauthenticated-admin=true`, which lets **any** client that can reach the coordinator connect as `BALEIA_TRINO_ADMIN_USER` and run `CREATE/DROP CATALOG`. That is safe only while Trino is reachable from the API and worker alone. The Helm chart defaults `trino.catalogGuard.allowUnauthenticatedAdmin` to `false`; with `false`, catalog management from Baleia is denied, so enable it only after restricting network access to the Trino service (e.g. a NetworkPolicy). Giving the API a real Trino credential, which removes the need for the flag, is planned.
- **Read-only mode.** A session opened with the read-only credential `baleia.mode=readonly` is denied every write/DDL check by the guard.

## Stack

| Layer | Technology |
|---|---|
| Backend | Go · Chi router · oapi-codegen (strict server mode) · sqlc |
| Frontend | Svelte 5 · TypeScript · SvelteKit (adapter-static, SPA) · Tailwind CSS v4 |
| Query engine | Trino |
| Catalog managers | Apache Polaris · Nessie · AWS Glue · Hive Metastore |
| Metadata store | PostgreSQL 18 |
| Workflow orchestration | Temporal (Go SDK) |
| Event streaming | NATS JetStream (Go SDK) |
| Process management | supervisord |
| SQL editing | CodeMirror 6 |
| Query results | Apache Arrow, stored in SeaweedFS or Ceph RGW (S3-compatible) |
| API testing | Bruno CLI |

## Prerequisites

- Docker and Docker Compose
- Go 1.23+
- Node.js 22+
- sqlc (for code generation)
- OrbStack (macOS) or native Docker (Linux)

## Quick start

```bash
# 1. Start all infrastructure
docker compose up -d

# 2. Apply database migrations (also runs automatically on API startup)
go run ./cmd/migrate -cmd=up

# 3. Start the API
make dev

# 4. Start the Temporal worker (separate process)
make worker
```

Open `http://localhost:8080`. First visit prompts a setup wizard to create the admin account.

To run the frontend dev server with HMR:

```bash
cd web && npm run dev
```

## Development commands

| Command | What it does |
|---|---|
| `make dev` | Run API on :9000 with local SMTP |
| `make worker` | Run Temporal worker |
| `make up` | Build the Trino plugin and bring up the compose stack |
| `make logs` | Follow api/worker logs from compose |
| `make down` / `make down-v` | Stop compose (down-v also destroys volumes) |
| `make migrate` / `migrate-down` / `migrate-status` / `migrate-reset` | Apply / revert / inspect / reset DB migrations |
| `make seed-tpch` | Insert the `tpch_test` registry row (used by plugin acceptance) |
| `make sqlc` | Regenerate Go code from SQL queries |
| `make generate` | Regenerate API handlers from the OpenAPI spec |
| `make lint` | Run golangci-lint |
| `make build` / `build-web` / `build-all` | Compile Go binaries / frontend / both |

The compose stack is the 5-minute quickstart. A Kubernetes (minikube) development
environment is provided under `deploy/` — see ``Kubernetes (minikube)`` in the
Development notes.

### Kubernetes (minikube)

```bash
make k8s-up          # start minikube, install dependencies + the Baleia chart
make k8s-forward     # port-forward api/trino/temporal-ui/mailpit/polaris
make k8s-logs        # follow all baleia pod logs
make k8s-down        # uninstall (keeps PVCs unless K8S_PURGE=1)
```

The API's Bruno collection runs against it end to end, and can be run again:

```bash
make k8s-forward
cd bruno && npx @usebruno/cli@2.13.0 run --env minikube
```
| `make compose-up` | Build and start all Docker services |
| `make compose-logs` | Tail logs from api + worker containers |

## Configuration

All settings are environment variables. Defaults work for local development with the provided `docker-compose.yaml`.

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `DATABASE_URL` | `postgresql://baleia:baleia@localhost:5432/baleia?sslmode=disable` | PostgreSQL connection string |
| `JWT_SECRET` | `change-me-in-production` | Key for signing JWTs |
| `BALEIA_ENCRYPTION_KEY` | _(unset)_ | Key that encrypts stored connection credentials (≥ 32 characters, must differ from `JWT_SECRET`). Required once any credential is stored; the API refuses to start without it. Derived with HKDF-SHA256; each value is bound to its connection row |
| `BALEIA_ENCRYPTION_KEY_PREVIOUS` | _(unset)_ | Previous encryption key, set only during rotation: values are read with the current or previous key and always written with the current one |
| `TEMPORAL_HOST` | `localhost:7233` | Temporal server address |
| `TRINO_URL` | `http://localhost:8088` | Trino coordinator REST endpoint. At API boot it also seeds the default Trino engine (with the service and admin identities below) and keeps it in step while that engine's connection is system-owned; engine endpoints live in the database (`engines`, [ADR 0002](docs/adr/0002-connections-engines-catalogs-bindings.md)). Trino is dialed through the connections dialer like every engine, so a loopback `TRINO_URL` needs `BALEIA_CONNECTIONS_ALLOW_LOOPBACK=true` (set by `make dev`) |
| `POLARIS_URL` | `http://localhost:8181` | Polaris REST API endpoint |
| `PUBLIC_URL` | `http://localhost:8080` | Externally reachable URL (used for presigned links) |
| `S3_ENDPOINT` | `localhost:9000` | S3-compatible storage endpoint (RustFS) |
| `S3_ACCESS_KEY` | `rustfsadmin` | S3 access key |
| `S3_SECRET_KEY` | `rustfsadmin` | S3 secret key |
| `S3_BUCKET` | `baleia` | S3 bucket for query results |
| `S3_USE_SSL` | `false` | Enable TLS for S3 connections |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASSWORD` / `SMTP_FROM` | — | Outbound email (password reset, invites) |
| `RATE_LIMIT_RPS` | `60` | Max requests per second (rate limiter) |
| `RATE_LIMIT_BURST` | `10` | Rate limiter burst size |
| `BALEIA_STATEMENT_TIMEOUT` | `30m` | Per-statement execution timeout; enforced by Temporal and by Trino (`query_max_execution_time`) |
| `BALEIA_RESULT_MAX_ROWS` | `1000000` | Max rows buffered per statement result before truncation |
| `BALEIA_RESULT_MAX_BYTES` | `536870912` | Max Arrow bytes per statement result before truncation |
| `BALEIA_SCHEDULES_ENABLED` | `true` | Ensure recurring Temporal Schedules (Lens pollers, table health, retriever) at worker boot |
| `BALEIA_CONNECTIONS_DENY_CIDRS` | _(unset)_ | Comma-separated CIDRs that connections may never reach, added to the built-in deny list (loopback, link-local incl. cloud metadata `169.254.169.254`, `0.0.0.0/8`) — see [ADR 0011](docs/adr/0011-direct-connections-security.md) |
| `BALEIA_CONNECTIONS_ALLOW_CIDRS` | _(unset)_ | If set, connections may only reach these comma-separated CIDRs (the deny list still wins) |
| `BALEIA_METRICS_ENABLED` | `true` | Serve Prometheus metrics at the API's `/metrics` (the catalog is `internal/metrics`, #87) |
| `BALEIA_METRICS_TOKEN` | _(unset)_ | When set, `/metrics` (API and worker) needs `Authorization: Bearer <token>`; it is never behind the user JWT. A credential: in the chart it is the optional Secret key `BALEIA_METRICS_TOKEN` (`secrets.values` or `existingSecret`), never `config` |
| `BALEIA_WORKER_METRICS_ADDR` | `:9090` | The worker's listener for `/metrics` (Temporal SDK, pool, build) and `/healthz`; empty disables it |
| `BALEIA_DB_MAX_CONNS` | api `20`, worker `10` | Size of Baleia's own database pool (observable as `baleia_db_pool_*`) |
| `BALEIA_DB_MIN_CONNS` | `2` | Connections the pool keeps open |
| `BALEIA_DB_MAX_CONN_LIFETIME` | `1h` | Recycle pool connections after this long |
| `BALEIA_SQLITE_ROOT` | _(unset)_ | Directory SQLite connections are confined to (symlinks resolved); unset disables the SQLite provider. The API and the worker must see the same files (shared volume) |
| `BALEIA_SQLITE_ALLOW_WRITE` | `false` | Let a SQLite connection that sets `read_write` open its file read-write; otherwise files open read-only |
| `BALEIA_CONNECTIONS_ALLOW_LOOPBACK` | `false` | Let connections reach loopback addresses. For local development only (`make dev` / `make worker` set it); never in a shared deployment |
| `BALEIA_TRINO_ADMIN_USER` | `baleia_admin` | Trino identity for catalog DDL/reconcile/maintenance (never user SQL) |
| `BALEIA_TRINO_SERVICE_USER` | `baleia` | Worker fallback identity for executions |
| `BALEIA_TRINO_USER_IDENTITY` | `uuid` | Map Baleia users to Trino users: `uuid`→`u_<uuid>` or `email` |

### Credential encryption

Connection credentials are stored as `v1:<key-id>:<base64>` envelopes: AES-256-GCM under a key derived (HKDF-SHA256) from `BALEIA_ENCRYPTION_KEY`, with the connection id as additional authenticated data, so a ciphertext copied to another connection does not open. A value that cannot be opened is reported as `credentials_unreadable`; it is never used as if it were the secret.

- **Fresh install:** set `BALEIA_ENCRYPTION_KEY` before saving the first credential. Without it the API starts with a warning and refuses to store credentials.
- **Upgrading from `JWT_SECRET`-encrypted credentials:** set `BALEIA_ENCRYPTION_KEY`, keep the existing `JWT_SECRET`, then run the one-shot, idempotent migration (`make migrate-credentials`, or `/migrate -cmd=credentials` in the image). It requires a valid `JWT_SECRET` to open the legacy values. Rows that cannot be decrypted are listed by id and left untouched; re-enter those credentials.
- **Rotating the key:** move the old value to `BALEIA_ENCRYPTION_KEY_PREVIOUS` and set the new `BALEIA_ENCRYPTION_KEY`. Values are read with either key, but reading never re-seals them. Run `make migrate-credentials` (or `/migrate -cmd=credentials`) to move every envelope to the new key; only after it reports no failures is it safe to drop `BALEIA_ENCRYPTION_KEY_PREVIOUS`. Envelopes left on the old key become `credentials_unreadable` once it is removed.

## Monitoring

Both processes expose Prometheus metrics:
- **API:** `GET /metrics` on its port.
- **Worker:** `GET /metrics` on `:9090`, its own listener.

The output covers Baleia's catalog (`baleia_*`: HTTP, auth, engines, schedules, catalog bindings, storage, database pool) and the Temporal SDK's `temporal_*`. Labels never carry identifiers or SQL.

[`deploy/prometheus/alerts.yml`](deploy/prometheus/alerts.yml) has the alerting rules. [`docs/operations.md`](docs/operations.md) covers the scrape config, the full catalog and a runbook per alert.

## Project structure

```
api/
  swagger.yaml                     OpenAPI 3.0 spec (source of truth)

cmd/
  api/main.go                      HTTP server entrypoint
  worker/main.go                   Temporal worker entrypoint
  migrate/main.go                  Standalone migration runner

config/
  oapi-models.yaml                 oapi-codegen config (models)
  oapi-server.yaml                 oapi-codegen config (server stubs)

db/
  migrations/                      Goose SQL migration files
  queries/                         Named SQL queries for sqlc

internal/
  api/                             Generated code (oapi-codegen + sqlc)
  handlers/                        Strict server handler implementations
  auth/                            JWT, password hashing, auth middleware
  connections/                     Connection provider contract, SSRF-safe dialer, one package per provider
  config/                          Environment config loader
  crypto/                          AES-256-GCM encryption for stored credentials
  db/                              sqlc/pgx database layer
  migrate/                         Programmatic migration runner
  polaris/                         Polaris REST API client helpers
  server/                          HTTP server bootstrap + middleware
  dialect/                         SQL per engine: quoting, splitting, previews, generation primitives
  policy/                          What users may do (SQL editor gate)
  storage/                         S3 client for SeaweedFS/Ceph
  temporal/                        Temporal workflow definitions + activities
  trino/                           Trino REST API client
  ws/                              WebSocket handler (execution event channel)
  arrowipc/                        Trino JSON → Arrow serialization

web/src/
  lib/components/                  Svelte components (editor, sidebar, results…)
  lib/stores/                      Svelte 5 runes-based stores (auth, workspaces…)
  routes/                          SvelteKit routes
```

## API

The OpenAPI spec is served at `GET /openapi.yaml` and is the single source of truth for endpoint contracts, request/response schemas, and authorization rules. Generate Go code from it with `make generate`.

## Adding a connection provider

1. Add the kind to the `catalog_connections_type_check` CHECK constraint (new migration)
2. Implement `connections.Provider` in `internal/connections/<kind>/` (config schema, `Validate`, `Ping`, `OpenMetadata`). Every network connection goes through the injected `connections.Dialer`; a driver without a dial hook is not shipped ([ADR 0011](docs/adr/0011-direct-connections-security.md))
3. Register it in `internal/connections/providers/providers.go` and add it to `TestEveryDriverUsesDialer`
4. Regenerate [`docs/connections.md`](docs/connections.md): `BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/`
5. To bind it into Trino, add its projection to the Trino binder (`internal/bindings/trino`: `Supports`, a projection function with a golden test, and `TestDDLHasNoSecrets`), then regenerate `docs/connections.md`
6. The API validates and stores connections through the provider's schema (`GET /connection-providers` exposes it), so no OpenAPI change is needed. Until the UI reads that endpoint (#22), add the card to `web/src/routes/(app)/datasources/+page.svelte`

## Source databases and SQL dialects

Provider (#11) and dialect (#14) integration tests run against real databases. On the minikube dev cluster (`make k8s-up`), `make k8s-source NAME=<mysql|mariadb|sqlserver|oracle|clickhouse>` starts one from `deploy/dev/sources/` and `make k8s-source-down NAME=…` removes it. The tests read `BALEIA_TEST_<KIND>_URL`; the `Sources` workflow runs them all nightly and on PRs that touch providers or dialects.

Each dialect (`internal/dialect`) ships keyword and function lists generated from the real server and committed with its version. `scripts/gen-dialect-lists.sh <dialect>` regenerates one (starting and removing its source database) and runs that dialect's execution test; `make dialect-lists` regenerates them all.

## Roadmap

- Remaining backend models/handlers: `QueryFolder`, `QueryFile`, `Execution`, `ExecutionStatement`
- **Lens** — unified observability: query history (Baleia + Trino), cluster monitoring, Iceberg table health, usage analytics
- **Retriever** — scheduled materialized-view refresh, using snapshot-ID comparison to pick incremental vs. full refresh
- Registering arbitrary Trino connectors (PostgreSQL, MySQL, Delta Lake, MongoDB, Kafka…) as ad-hoc data sources via Trino's dynamic catalog API, without restarts
- Remaining screens: admin, datasources management, query history/monitoring

## Releasing

Releases are automated with [release-please](https://github.com/googleapis/release-please). On every push to `main`:

1. `.github/workflows/release.yml` runs the test gate (`go vet` + `go test`), then release-please opens or updates a **Release PR** that bumps `.release-please-manifest.json` and writes `CHANGELOG.md` from Conventional Commits.
2. Merging the Release PR creates a **GitHub Release** and a `vX.Y.Z` tag, and the same workflow then publishes the container image to GHCR (`ghcr.io/<owner>/baleia:vX.Y.Z`), stamped with the release version via `-ldflags` (`internal/version`).
3. A failed image publish can be retried with `workflow_dispatch`, passing the existing `vX.Y.Z` tag.

Commit conventions drive the version: `feat:` → minor, `fix:` → patch, `feat!:` / `BREAKING CHANGE:` → major.

## Contributing

Baleia is a solo effort right now. If you know Svelte/SvelteKit and want to help shape the frontend, open an issue or reach out — this is an open invitation, not a filter.

All code, comments, documentation, and fixtures in this repository are English-only.

## License

Apache 2.0. See [LICENSE](LICENSE).

## Repository

https://github.com/maltzsama/baleia