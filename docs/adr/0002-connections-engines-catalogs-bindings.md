# 0002. Connections, engines, catalogs and bindings

- Status: Accepted (owner decisions D10, D11)
- Date: 2026-10-14
- Issues: #9, #11, #13, #20, #80

## Context

The backend fuses four concerns that must evolve independently: credentials for external systems,
the engines that run SQL, the logical containers of data, and how a container becomes visible inside
an engine. Today one `catalog_connections` row drives all of them, the engine is a concrete
`*trino.Client`, and knowledge of each connection type is spread over several `switch` statements.
Adding a second engine or a database provider would touch every one of those places.

## Decision

Four concepts, each with its own table and package:

| Concept | Table | What it is |
|---|---|---|
| **Connection** | `catalog_connections` | A provider kind + endpoint + sealed credentials for any external system: Iceberg catalog managers, metastores, databases, query engines. |
| **Engine** | `engines` | An instance of a query-engine kind, backed by one connection. Kinds are **federated** (host catalogs from other connections: Trino; later Doris, StarRocks) or **direct** (the connection itself executes SQL: PostgreSQL, MySQL/MariaDB, …). |
| **Catalog** | `catalogs` | A logical container owned by a connection (an Iceberg warehouse, a database), or **native** to an engine (the databases a direct engine exposes). Independent of the engine that queries it. |
| **Binding** | `catalog_bindings` | Catalog × federated engine → engine-side catalog name, connector and mode: `managed` (Baleia created it), `adopted` (exists in the engine, not created by Baleia) or `native` (a direct engine's own catalog). |

A **target** for execution is a binding plus a schema.

Packages:

- `internal/connections` — provider registry and contract (#11).
- `internal/queryengine` — engine contract and kind registry (#20).
- `internal/bindings` — binder contract; one binder per federated engine kind (#80).

`internal/api/**` and `internal/temporal/**` never import a concrete driver or `internal/trino`.
They use the three contracts above, constructed at the wiring sites
([0007](0007-dispatch-and-wiring.md)).

### v1 provider scope (D11)

| Kind | Metadata (direct) | Direct engine | Bindable into Trino | Bindable later into Doris/StarRocks |
|---|---|---|---|---|
| polaris, nessie, iceberg (REST), lakekeeper, unity | ✓ Iceberg REST | — | ✓ `iceberg` (rest) | ✓ `type=iceberg` |
| hive (HMS) | — (engine introspection) | — | ✓ `iceberg` / `hive` / `delta_lake` | ✓ `type=hms` / `iceberg` |
| glue | ✓ AWS Glue API | — | ✓ `iceberg` / `hive` / `delta_lake` | ✓ |
| postgresql | ✓ | ✓ | ✓ `postgresql` | ✓ `type=jdbc` |
| mysql / mariadb | ✓ | ✓ | ✓ `mysql` / `mariadb` | ✓ `type=jdbc` |
| sqlserver | ✓ | ✓ | ✓ `sqlserver` | ✓ `type=jdbc` |
| oracle | ✓ | ✓ | ✓ `oracle` | ✓ `type=jdbc` |
| clickhouse | ✓ | ✓ | not in v1 | ✓ `type=jdbc` |
| sqlite | ✓ | ✓ | — (no Trino connector) | — |
| flightsql | — (engine introspection) | ✓ | — (no Trino connector) | — |
| trino | — | federated engine | n/a | n/a |
| doris, starrocks | — | federated engine (later) | n/a | n/a |

The bindable columns are declared by binders (`bindings.Binder.Supports(kind)`), not by providers.
#11 generates this matrix into `docs/connections.md` from both registries; that file becomes the
maintained copy.

## Consequences

- A new provider, engine kind or binder is one package plus a registration; no `switch` edits.
- A catalog can be bound into several federated engines and queried directly at the same time.
- Authorization (#16) is expressed on catalogs, not on connections or engine-side names.
- Migrations must backfill: every existing connection gets a catalog, the default Trino engine is
  created from `TRINO_URL`, and every existing Trino catalog becomes a `managed` binding.

## Current compliance

Partially compliant.

- `engines` and `catalog_bindings` exist and `catalogs` has `origin`/`source_ref` (#13 PR 1,
  migration 00024). Every Trino cluster is backed by a system-owned `trino` connection and a
  federated engine; every catalog has a `managed` binding. A transitional trigger keeps that
  binding in step while the catalog handlers still write the Trino columns of `catalogs`; it is
  dropped when those writes move to bindings (#80).
- Runs resolve an explicit target (binding + schema), the workspace default or the workspace's only
  target, and store it with a snapshot; the editor picks targets from
  `GET /workspaces/{id}/targets`. The Trino workflow still receives the engine catalog only: the
  schema and read-only mode reach the engine with the workflow input of #13 PR 3 / #20, and
  direct-engine targets are refused until then.
- Direct engines are registered through `/engines` on connections whose provider has the
  `direct_engine` role; their native catalogs (`origin = native`, no Trino columns, migration
  00025) and native bindings are created with them. Federated engines are only the default Trino
  engine Baleia bootstraps; registering others needs a Trino provider (#11 PR 3).
- The engine contract (`internal/queryengine`) and the Trino adapter (`internal/queryengine/trino`)
  are in place, and `internal/api` and `internal/temporal` run SQL only through them (#20 PR 2,
  #25): statements, the explorer, connection tests, retrievers and Iceberg maintenance. Catalog
  DDL goes through `internal/bindings/trino` (the binder contract is #80) and Lens monitoring
  through `internal/trino/monitor`. Until executions carry their target's engine (#13 PR 3), they
  run on the default Trino engine.
- The provider registry exists (`internal/connections`) for polaris, nessie, iceberg, postgresql
  and mysql. Create and update validate through `Provider.Validate`, store non-secret values in
  `catalog_connections.config` and seal only secrets; `GET /connection-providers` exposes the
  schemas. Connection tests and Lens table health read through the registry (#11).
- The `url` column is still written (derived with `providers.LegacyURL`) and the credentials blob
  of older rows still carries non-secret values, because the Trino projection reads them:
  `internal/catalogs/projection.go` (`switch conn.Type`) and `trinoConnector` in
  `internal/api/handlers_catalog_connections.go` move to binders in #80. The typed
  `*_credentials` request fields (`internal/api/catalog_credentials.go`) stay until the UI uses
  the provider schema (#22).
- Glue and Hive have no provider yet (#86).

## Alternatives considered

- **Keep connection = catalog = Trino catalog.** Rejected: cannot express direct engines, a
  catalog bound into two engines, or catalogs that Trino has but Baleia did not create.
- **Model engines as a flag on connections.** Rejected: a connection used both as a direct engine
  and as a bindable source would need two sets of settings on one row.
