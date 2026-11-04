# Architecture

Baleia is a **control plane**: it manages connections, catalogs and engines, runs your SQL on those engines and shows the results. The engines are the data plane. Baleia is not a query proxy, a JDBC/ODBC/Flight SQL server or a federation layer: external clients talk to the engines directly, and joins across engines are the job of a federated engine such as Trino ([ADR 0001](adr/0001-control-plane-only.md)).

Architectural decisions are recorded in [`adr/`](adr/README.md). They are the current truth: where this page or the code disagrees with an ADR, the ADR's "Current compliance" section names the issue that closes the gap.

## Components

| Component | What it does |
|---|---|
| **Web app** (`web/`) | SvelteKit single-page app: workbench, data explorer, connections, content, monitoring, settings. |
| **API** (`cmd/api`) | Go HTTP server. The OpenAPI spec (`api/swagger.yaml`) is the contract. Serves the web app, the REST API, the execution WebSocket and `/mcp`. |
| **Worker** (`cmd/worker`) | Temporal worker: runs executions, metadata syncs, binding reconciliation, monitoring pollers and other recurring jobs. |
| **PostgreSQL** | Baleia's own metadata: users, workspaces, connections, catalogs, content, executions, audit. |
| **Object storage** | Any S3-compatible store (RustFS in the dev stacks) holding each statement's result as an Arrow file. |
| **Temporal** | Durable workflows for everything long-running. It carries ids and statuses, never result rows. |
| **Engines** | Where SQL runs: Trino (federated) and the direct engines PostgreSQL, MySQL/MariaDB, SQL Server, Oracle, SQLite, ClickHouse and Arrow Flight SQL ([ADR 0006](adr/0006-multi-engine-scope.md)). |
| **Catalog managers** | Polaris, Nessie, Lakekeeper, Iceberg REST, Unity, Glue and Hive Metastore, reached by Baleia for metadata and bound into Trino as catalogs ([connections.md](connections.md)). |

```mermaid
flowchart LR
    Browser["Web app"] -->|REST and WebSocket| API
    API -->|starts workflows| Temporal
    Temporal --> Worker
    Worker -->|runs SQL| Engines
    Worker -->|writes Arrow results| Storage[("Object storage")]
    API -->|reads result pages| Storage
    API --- DB[("PostgreSQL")]
    Worker --- DB
    Engines -->|Trino catalogs| Managers["Catalog managers"]
```

## How a query runs

1. You press Run (or ⌘/Ctrl+Enter). The web app sends the selection, or the statement at the cursor, with the target: engine, catalog binding and schema.
2. The API checks access, decides the run's workload class, records the execution and starts a Temporal workflow. With [workload management](guides/workload.md) on, the workflow first asks the engine's admission workflow for a slot in that class, and the execution is `queued` until it gets one.
3. The worker splits the script with the engine's dialect and runs the statements in order in one engine session (on Trino, as the signed-in user's Trino identity). It stops at the first error; the rest are marked skipped.
4. Each statement's rows are written to object storage as an Arrow file, capped by `BALEIA_RESULT_MAX_ROWS` and `BALEIA_RESULT_MAX_BYTES`.
5. The WebSocket sends status events only: a snapshot first, then statement and execution status. Rows never travel over it.
6. The web app reads the result in pages (`GET …/statements/{position}/results?offset=&limit=`). The API seeks the covering Arrow batches in the stored file, so neither side loads the whole result. Exports (CSV, TSV, JSON Lines, Arrow) stream from the same file.

## Trino security model

- **Three identities.** `BALEIA_TRINO_ADMIN_USER` (default `baleia_admin`) runs catalog DDL, reconciliation and Lens maintenance — it is **never** used to run user SQL. `BALEIA_TRINO_SERVICE_USER` (default `baleia`) is the worker's fallback identity. User queries run as the logged-in Baleia user's Trino identity (`BALEIA_TRINO_USER_IDENTITY`: `uuid` → `u_<uuid>`, or `email`) so `system.runtime.queries.user` reflects the real user.
- **The catalog guard is the control.** The `baleia-catalog-guard` access control (shipped by the catalog-store plugin) denies `CREATE/DROP CATALOG` and `SET SESSION AUTHORIZATION` to non-admin identities at the Trino level. The SQL editor's `IsForbiddenInEditor` gate (catalog DDL, `EXECUTE IMMEDIATE` smuggling catalog DDL, `SET SESSION AUTHORIZATION`) is UX and defense in depth, never the security boundary.
- **Trino is private.** The default compose stack does not publish Trino on the host; only the API/worker reach it via `expose`. Copy `docker-compose.override.yml.example` to publish it for local debugging. The chart ships a NetworkPolicy that admits only the API and worker pods (`trino.networkPolicy`, on by default), which takes effect when the cluster's CNI enforces NetworkPolicy.
- **The bundled Trino requires authentication (#647).** Compose and the Helm chart run it with HTTPS and password authentication; plain HTTP refuses clients. Baleia logs in as `BALEIA_TRINO_ADMIN_USER` with `BALEIA_TRINO_PASSWORD` (#646) and trusts the coordinator's CA (`BALEIA_TRINO_CA_FILE`). The base access control is file-based: only that login may impersonate other identities, so no other client can claim a Baleia user, and catalogs stay open (`owner`). The catalog guard is stacked after it with `baleia.allow-unauthenticated-admin=false`, so catalog management needs the authenticated admin. A client without the password gets `401`; another login cannot impersonate, claim the admin or manage catalogs.
- **The catalog manager authorizes each user (#648, [ADR 0015](adr/0015-per-user-catalog-identity.md)).** Catalogs bound from a connection set to per-user identity use `iceberg.rest-catalog.session=USER`. Every query session carries a bearer token as the extra credential `token`, filled by the worker or API when the session opens (`queryengine.CatalogCredentials`, never in workflow input): the user's identity-provider access token, kept sealed in `user_idp_tokens` and refreshed before it expires; or, for users without one and background work, the token of the connection behind the session's catalog. A user whose identity-provider session ended carries none and is refused. Glue, Hive Metastore and Nessie act as the connection's account for every user.
- **Read-only mode.** A session opened with the read-only credential `baleia.mode=readonly` is denied every write/DDL check by the guard. Baleia uses it for MCP agents whose owner is a viewer of the workspace (#653), the built-in assistant, semantic custom SQL and its own probes; a workspace grant never opens a read-only session (#651).

See [ADR 0005](adr/0005-trino-security-model.md) for the decision record.
