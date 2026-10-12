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
| `make migrate` | Apply pending DB migrations |
| `make sqlc` | Regenerate Go code from SQL queries |
| `make generate` | Regenerate API handlers from the OpenAPI spec |
| `make lint` | Run golangci-lint |
| `make build` | Compile Go binaries to `bin/` |
| `make compose-up` | Build and start all Docker services |
| `make compose-logs` | Tail logs from api + worker containers |

## Configuration

All settings are environment variables. Defaults work for local development with the provided `docker-compose.yaml`.

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `DATABASE_URL` | `postgresql://baleia:baleia@localhost:5432/baleia?sslmode=disable` | PostgreSQL connection string |
| `JWT_SECRET` | `change-me-in-production` | Key for signing JWTs and encrypting stored credentials |
| `TEMPORAL_HOST` | `localhost:7233` | Temporal server address |
| `TRINO_URL` | `http://localhost:8088` | Trino coordinator REST endpoint |
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
  catalog_connections/             Client interface + adapters for Polaris, Nessie, Glue, Hive
  config/                          Environment config loader
  crypto/                          AES-256-GCM encryption for stored credentials
  db/                              sqlc/pgx database layer
  migrate/                         Programmatic migration runner
  polaris/                         Polaris REST API client helpers
  server/                          HTTP server bootstrap + middleware
  sqlsplitter/                     Multi-statement SQL parser
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

## Adding a catalog manager type

1. Add the type to the `catalog_connections_type_check` CHECK constraint (new migration)
2. Implement the `Client` interface for the new manager in `internal/catalog_connections/`
3. Extend `trinoConnector()` and `trinoCatalogProperties()` in `handlers_catalog_connections.go`
4. Add the credentials schema to `api/swagger.yaml`
5. Add the connector card to `web/src/routes/datasources/+page.svelte`
6. Regenerate: `make sqlc && make generate`

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