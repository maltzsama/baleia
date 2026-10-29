# Development

## Prerequisites

- Docker (with Compose) and, for the Kubernetes environment, minikube, kubectl and Helm
- Go (the version in `go.mod`)
- Node.js 22+
- `sqlc` and `oapi-codegen` are pinned and run through `go run` by `make sqlc` / `make generate`

## Environments

There are two ([ADR 0010](adr/0010-dev-environments.md)):

- **Compose**: the quickest way to run everything on one machine.

  ```bash
  make up      # Postgres, Temporal, RustFS, Polaris, Trino (with the catalog-store plugin), API, worker
  make logs    # follow the API and worker
  make down    # stop (keeps data); make down-v also deletes the volumes
  ```

  Open http://localhost:8080; the first visit runs the setup wizard that creates the admin account. Source databases for provider and dialect tests start with `docker compose --profile sources up -d`.

- **minikube with the Helm chart**: the development environment, with the same chart as production.

  ```bash
  make k8s-up          # start minikube, install the dependencies and the Baleia chart
  make k8s-forward     # port-forward the API, Trino, Temporal UI, Mailpit and Polaris
  make k8s-logs        # follow every Baleia pod
  make k8s-down        # uninstall (keeps volumes unless K8S_PURGE=1)
  ```

To work on the code with fast reloads, run the processes on the host against either stack: `make dev` (API on :9000) and `make worker`, and `cd web && npm run dev` for the web app with hot reload.

## Commands

| Command | What it does |
|---|---|
| `make dev` / `make worker` | Run the API (:9000) / the Temporal worker on the host |
| `make up` / `down` / `down-v` / `logs` | Compose stack |
| `make k8s-up` / `k8s-forward` / `k8s-logs` / `k8s-down` | minikube environment |
| `make migrate` / `migrate-down` / `migrate-status` / `migrate-reset` | Database migrations (also applied at API start) |
| `make generate` / `make sqlc` | Regenerate the API handlers from `api/swagger.yaml` / the database code from `db/queries` |
| `make lint` | golangci-lint |
| `make build` / `build-web` / `build-all` | Go binaries / web app / both |
| `make e2e` / `make e2e-down` | minikube, the e2e seed and the Playwright suite / tear the cluster down |

## Tests

- **Backend:** `go vet ./...` and `go test ./...`. Tests that need a database read `BALEIA_TEST_DATABASE_URL` and are skipped without it.
- **Web app:** from `web/`, `npm run check`, `npm run lint`, `npm run format:check`, `npm test` and `npm run build`. [`AGENTS.md`](../AGENTS.md) has the frontend rules.
- **End to end:** `make e2e` runs the Playwright suite (`web/e2e`) against minikube, seeded by `cmd/e2e-seed`.
- **API:** the Bruno collection in `bruno/` runs against a running stack: `cd bruno && npx @usebruno/cli@2.13.0 run --env minikube --env-var admin_password=e2e-password` (the minikube seed's admin is `admin@e2e.test`). The environments leave the password empty: pass local-only values with `--env-var`, never commit them. Cases that need more setup skip themselves: `07_update_password` (set `admin_password_new`) and `14_login_ldap` (an LDAP provider). The password-reset cases send real reset requests, which are limited to 5 per 15 minutes per IP: runs closer together than that answer 429.

## Repository layout

| Path | Contents |
|---|---|
| `api/swagger.yaml` | The OpenAPI contract, source of truth for the API |
| `cmd/` | Entry points: `api`, `worker`, `migrate`, and `e2e-seed` (test data, build tag `e2e`) |
| `db/migrations`, `db/queries` | Goose migrations and the named queries sqlc compiles |
| `internal/api`, `internal/server` | Handlers (and the generated server), HTTP bootstrap and middleware |
| `internal/auth`, `authz`, `identity`, `policy` | Sign-in, authorization, directory mapping, what users may do |
| `internal/connections`, `catalogs`, `bindings`, `metadata` | Connection providers and the network dialer, catalogs, engine bindings, metadata snapshots |
| `internal/queryengine`, `dialect`, `trino`, `trinoidentity` | Engines, SQL dialects, the Trino client and user identities |
| `internal/temporal`, `storage`, `arrowipc`, `ws` | Workflows and activities, object storage, Arrow results, the execution WebSocket |
| `internal/content`, `semantic` | Saved content (spaces, versions, bundles) and the semantic layer |
| `internal/audit`, `metrics`, `obs` | Audit trail, Prometheus metrics, logging and tracing |
| `internal/mcp`, `agentpolicy`, `assistant` | MCP server for agents, agent policies, the assistant |
| `web/` | The SvelteKit web app and its Playwright suite |
| `deploy/helm`, `deploy/dev`, `deploy/prometheus` | Helm chart, minikube environment, alert rules |
| `docs/` | This documentation and the ADRs |

## Adding a connection provider

1. Add the kind to the `catalog_connections_type_check` CHECK constraint (new migration)
2. Implement `connections.Provider` in `internal/connections/<kind>/` (config schema, `Validate`, `Ping`, `OpenMetadata`). Every network connection goes through the injected `connections.Dialer`; a driver without a dial hook is not shipped ([ADR 0011](adr/0011-direct-connections-security.md))
3. Register it in `internal/connections/providers/providers.go` and add it to `TestEveryDriverUsesDialer`
4. Regenerate [`docs/connections.md`](connections.md): `BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/`
5. To bind it into Trino, add its projection to the Trino binder (`internal/bindings/trino`: `Supports`, a projection function with a golden test, and `TestDDLHasNoSecrets`), then regenerate `docs/connections.md`
6. The API validates and stores connections through the provider's schema, and the connection wizard renders its form from `GET /connection-providers`, so neither the OpenAPI spec nor the web app needs a change

## Source databases and SQL dialects

Provider (#11) and dialect (#14) integration tests run against real databases. On the minikube dev cluster (`make k8s-up`), `make k8s-source NAME=<mysql|mariadb|sqlserver|oracle|clickhouse>` starts one from `deploy/dev/sources/` and `make k8s-source-down NAME=…` removes it. The tests read `BALEIA_TEST_<KIND>_URL`; the `Sources` workflow runs them all nightly and on PRs that touch providers or dialects.

Each dialect (`internal/dialect`) ships keyword and function lists generated from the real server and committed with its version. `scripts/gen-dialect-lists.sh <dialect>` regenerates one (starting and removing its source database) and runs that dialect's execution test; `make dialect-lists` regenerates them all.
