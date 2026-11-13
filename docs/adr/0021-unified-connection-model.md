# 0021. Unified connection model and Trino catalog support

- **Status:** Proposed
- **Date:** 2026-11-08
- **Amends:** ADR 0002
- **Preserves:** ADR 0003 catalog-store contract and lifecycle
- **Related:** CR — Unified Connection Model and Trino Catalog Support

## Context

Baleia registers resources through four existing concepts (ADR 0002):

- **Connection:** a registered resource, identified by its provider kind and configuration.
- **Engine:** a query-engine instance backed by a connection.
- **Catalog:** a logical container owned by a connection or native to an engine.
- **Binding:** the relationship between a catalog and a federated engine.

The existing model works, but two dimensions are currently entangled: the access path used by a connection and the Trino catalog implementation used to expose it.

### Current implementation

The coupling is present in the following places:

1. `internal/connections/connections.go` defines `Provider`, which owns the connection kind, family, roles, configuration schema, validation, metadata reader, ping and status.
2. `internal/bindings/trino/binder.go` implements `Binder.Supports()`, mapping connection kinds to Trino catalog types.
3. `internal/api/handlers_bindings.go` exposes that mapping through `GET /engines/{id}/binding-support`.
4. `web/src/lib/connections/ConnectionWizard.svelte` combines provider roles with the binding-support response to determine how a connection can be used.

The current binder mapping is:

```go
func (*Binder) Supports() map[connections.Kind][]string {
    return map[connections.Kind][]string{
        connections.KindPolaris:       {"iceberg"},
        connections.KindNessie:        {"iceberg"},
        connections.KindIcebergREST:   {"iceberg"},
        connections.KindHiveMetastore: {"iceberg", "hive", "delta_lake"},
        connections.KindGlue:          {"iceberg", "hive", "delta_lake"},
        connections.KindLakekeeper:    {"iceberg"},
        connections.KindUnity:         {"iceberg"},
        connections.KindPostgreSQL:    {"postgresql"},
        connections.KindMySQL:         {"mysql"},
        connections.KindMariaDB:       {"mariadb"},
        connections.KindSQLServer:     {"sqlserver"},
        connections.KindOracle:        {"oracle"},
    }
}
```

This mapping is maintained independently from the connection type definitions.

### Existing UI behavior

The current `ConnectionWizard` allows the user to:

- Create a connection.
- Register it as a direct engine.
- Bind it into one or more federated engines.
- Select a Trino catalog type for its catalog registration.

The implementation uses:

```typescript
let direct = $state(false);
let bindEngineIds = $state<string[]>([]);
let connector = $state('');
```

Direct access and federated access are independent selections. Both can be enabled for the same connection.

The wizard also uses a single selected connector when creating the catalogs in that operation.

This behavior does not match the target model defined in this ADR.

### Additional gaps

- The provider registry currently exposes 18 connection kinds, including engine kinds and planned engines.
- The Trino binder currently supports 12 connection kinds.
- The application wires eight direct query-engine kinds (across seven adapter packages), in addition to Trino.
- The current provider contract has no explicit access-mode declaration.
- The current provider status has three states: `supported`, `preview` and `unsupported`.
- The current database model still requires the legacy `catalog_connections.url` column to be non-null.
- The current catalog creation handler requires `name`, `trino_catalog_name` and `warehouse`.
- The current frontend defines connection family labels, descriptions and ordering independently of the backend registry.

The objective is to remove these inconsistencies without introducing another user-facing abstraction.

---

## Decision

### D.1 — Connection types are the single source of truth

Every resource registered in Baleia is represented as a **Connection**.

There is no separate first-class Connector resource and no second configuration workflow.

The existing `connections.Provider` interface and `connections.Registry` are extended to become the authoritative source of connection type metadata.

This is an evolution of the existing implementation, not a new domain abstraction.

Each connection type declares:

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | Stable identifier, corresponding to `connections.Kind` |
| `label` | string | Display name |
| `description` | string | User-facing explanation |
| `category` | enum | UI grouping |
| `access_modes` | []AccessMode | Supported access paths |
| `engine_kind` | string | Native query-engine kind for direct access |
| `trino_catalog_types` | []string | Internal Trino catalog mappings |
| `config_schema` | []Field | Configuration form and validation schema |
| `capabilities` | map[AccessMode]map[Capability]State | Capabilities for each access path |
| `version_requirements` | metadata | Validated engine and Trino version requirements |
| `status` | Status | Implementation status |

`trino_catalog_types` is internal implementation metadata. It is not a separate resource, selection step or user-facing abstraction.

The connection type defines the Trino catalog implementation required to expose the resource.

The frontend must consume the registry through the existing `GET /connection-providers` endpoint, extended with the new metadata.

The frontend must not independently determine:

- Whether a connection supports direct access.
- Whether a connection supports federated access through a given engine kind.
- Which Trino catalog types a connection can produce.
- Which engine kinds can consume a connection.
- Which capabilities are available for a connection type.

Category presentation metadata, including display label, description and ordering, must also come from the registry if the backend is to remain the single source of truth for connection type presentation.

### D.2 — Access mode is explicit and exclusive

A connection has one access mode.

The supported data access modes are:

- `direct`: Baleia accesses the resource through a native query-engine implementation.
- `federated`: Baleia exposes the resource through an existing compatible federated engine.

Trino is an engine kind, not an access mode. The selected `engine_id` identifies the concrete
federated engine instance. Doris, StarRocks and future engine kinds use `federated`; they do not
add access-mode values.

A connection cannot use both modes simultaneously.

If the user wants PostgreSQL through Trino and PostgreSQL directly, they register two independent connections to the same PostgreSQL server.

Each connection has its own:

- Configuration.
- Credentials.
- Ownership.
- Access permissions.
- Lifecycle.
- Engine or catalog relationships.

This is intentional. Independent access paths may require different credentials, owners, permissions and operational lifecycles.

Rules:

1. Both modes declared: the user must choose exactly one during registration.
2. One mode declared: Baleia selects it automatically.
3. No data access mode: the connection is an engine-family resource or another resource that does not itself represent a data access path.

`metadata` is not an access mode. Metadata reading remains an independent capability of a connection type.

The existing `RoleMetadata` behavior remains orthogonal to the access-mode decision.

The existing `RoleFederated` behavior also remains relevant for connection types representing query engines, such as Trino.

`RoleDirect` and the binder-derived bindable role must no longer be the source of truth for choosing a connection's access path.

#### Engine-family types

Trino, Doris and StarRocks are engine-family resources, not data sources.

They do not have a `direct` or `federated` access mode.

They remain registered connection types because their connection configuration is used to create federated engines.

The existing `federated_engine` role remains applicable.

#### Mode mutability

Access mode is immutable after creation.

Changing a connection from direct to federated, or vice versa, is a resource lifecycle change, not
an ordinary configuration update. The user creates a new connection for the other access path.

Configuration changes within the same access mode remain allowed.

### D.3 — Trino catalog mappings are internal metadata of connection types

Each connection type that supports Trino declares the Trino catalog type or types required to expose it.

The mapping is internal.

The user registers a connection such as PostgreSQL, BigQuery, Kafka or Hive Metastore. The user does not register a connection and then configure a separate Connector resource.

The mapping supports both directions:

**Many connection types to one Trino catalog type**

Examples:

- Polaris → `iceberg`
- Nessie → `iceberg`
- Iceberg REST → `iceberg`
- Lakekeeper → `iceberg`
- Unity Catalog → `iceberg`

**One connection type to multiple Trino catalog types**

Examples:

- Hive Metastore → `iceberg`, `hive`, `delta_lake`
- AWS Glue → `iceberg`, `hive`, `delta_lake`

Therefore, `trino_catalog_types` is a set, not a single string.

The current `Binder.Supports()` mapping must be derived from connection type definitions rather than maintained independently by the binder.

The binder continues to own the engine-specific projection mechanics:

- Building Trino catalog properties.
- Protecting sensitive configuration.
- Generating catalog DDL.
- Managing catalog-store registration.
- Handling catalog lifecycle and versioning.
- Confirming and observing catalog state.

It no longer owns the authoritative connection-kind-to-Trino-catalog mapping.

The internal Trino catalog type remains necessary because Trino requires it when creating a catalog. It is not a separate Baleia resource or user-facing configuration concept.

#### D.3.1 — One-to-many mappings and table redirection

The one-to-many mapping is required to preserve the existing Hive table redirection feature (#86).

A Hive Metastore or AWS Glue Data Catalog is a format-agnostic metadata store. It can contain Hive, Iceberg and Delta Lake tables.

Trino requires different catalog implementations to read those formats:

| Internal Trino catalog type | Data format |
|---|---|
| `hive` | Hive tables |
| `iceberg` | Iceberg tables |
| `delta_lake` | Delta Lake tables |

One Hive Metastore or Glue connection can therefore produce up to three Trino catalog registrations on the same engine.

These registrations are represented through the existing `Catalog` and `Binding` concepts. No new domain entity is introduced.

The existing `HiveRedirection` implementation in `internal/bindings/redirection.go` must remain functional.

It currently:

1. Applies only to Hive Metastore and Glue connections.
2. Applies only when the current binding uses the Hive catalog type.
3. Searches for sibling Iceberg and Delta Lake bindings.
4. Requires the sibling bindings to be enabled and active.
5. Requires the sibling catalogs to belong to the same connection and engine.
6. Injects the corresponding `hive.iceberg-catalog-name` and `hive.delta-lake-catalog-name` properties.

The new implementation must preserve these conditions.

The connection type definition must describe the available catalog registrations and their configuration mappings.

The connection wizard must create the corresponding catalog records without exposing a separate connector selector.

For Hive and Glue, the UI may present the resulting catalog formats as user-facing catalog outputs, with names that can be customized. The internal Trino catalog type is selected by the backend from the connection type definition.

The current wizard's single `connector` selection is insufficient for this requirement and must be removed.

### D.4 — Federated data-source lifecycle preserves engine-specific reconciliation

The existing Trino catalog-store integration defined by ADR 0003 remains the source of truth for the Trino plugin's desired catalog configuration.

For a data-source connection using existing federated engines (ADR 0029, workflow C), one backend
operation runs the lifecycle:

1. In one database transaction, persist the connection, the logical catalogs, the bindings and the
   desired catalog-registration state, including the catalog-store registry row.
2. Commit.
3. Apply `CREATE CATALOG` through the existing reconciliation.
4. Confirm the desired catalog version through the plugin.
5. Observe the catalog state.

Steps 3 to 5 cannot take part in the database transaction. A failure there leaves the committed
desired state pending or failed, observable and recoverable through reconciliation; it does not
undo steps 1 and 2.

The catalog row and registry row must continue to be written transactionally where the existing lifecycle requires it.

The plugin must continue to load registered catalogs without requiring Baleia to be running.

Desired, effective and observed states remain distinct.

No change to the catalog-store plugin contract is required by this ADR.

This catalog lifecycle is distinct from registering the Trino engine itself. Standalone federated
engine registration remains a first-class Engines-page workflow. Its backend operation creates the
engine-family connection and engine resource atomically; the engine may initially have no
user-defined bindings.

The change to data-source registration is who declares compatibility, how the application selects
an existing engine instance, and that one backend operation persists the connection, catalogs,
bindings and desired registration state atomically instead of the frontend sequencing them.

### D.5 — The interoperability matrix has three independent layers

A single `supported` or `unsupported` flag is insufficient.

Compatibility must be described through three independent layers.

#### Static — Connection type definition

Declared in the application and shipped with the Baleia binary.

Includes:

- Connection type.
- Supported access modes.
- Direct engine kind.
- Trino catalog mappings.
- Configuration schema.
- Declared capabilities.
- Validated version requirements.
- Implementation status.

#### Runtime — Deployment availability

Computed for the actual deployment and engine instance.

Includes:

- Engine version.
- Required Trino catalog plugin availability.
- Engine connectivity.
- Remote endpoint reachability.
- Authentication result.
- Configuration validity.

The current `Binder.Observe` implementation reports existing catalogs and their observed connector types. It does not prove that every Trino plugin installed in the deployment is available.

In particular, observing `system.metadata.catalogs` cannot establish the presence of a plugin for which no catalog has been created.

Plugin availability must therefore be determined through an explicit deployment inventory, a supported runtime mechanism, or a real catalog-registration attempt.

Baleia must not report a plugin as installed merely because its connector name appears in the static registry.

#### Operational — Live resource state

Uses the existing lifecycle and observation mechanisms.

Includes:

- Binding lifecycle state.
- `engine_catalog_observations`.
- Effective catalog version.
- Last connection-test result.
- Last synchronization error.
- Last observed catalog availability.

A missing plugin is an availability problem.

A supported catalog whose remote system is unreachable is an operational problem.

An invalid configuration is a validation problem.

These conditions must remain distinguishable.

### D.6 — Capabilities are declared per connection type and access mode

Capabilities must be declared per `(connection type, access mode)`.

They must never be inferred solely from successful catalog registration.

The initial vocabulary is:

| Capability | Meaning |
|---|---|
| `connect` | Establish a connection or initialize a catalog |
| `test_connection` | Validate connectivity and authentication |
| `list_catalogs` | Discover catalogs or equivalent logical containers |
| `list_schemas` | Discover schemas or namespaces |
| `list_tables` | Discover tables and equivalent objects |
| `read` | Execute read operations |
| `insert` | Insert data |
| `update` | Update existing data |
| `delete` | Delete data |
| `create_table` | Create tables |
| `drop_table` | Drop tables |
| `alter_table` | Modify table definitions |
| `metadata` | Retrieve physical metadata |
| `transactions` | Support multi-statement transactional operations |
| `views` | Discover and use views |

Capabilities must be version-aware.

For direct connections, engine capabilities must be consistent with the existing `queryengine` implementation.

For Trino-backed connections, capabilities must reflect the actual Trino catalog implementation and its version.

A connector that supports reads but not row-level updates must not be presented as fully writable.

A catalog that can be registered but cannot expose metadata must not be treated as having metadata discovery.

Capabilities for planned and experimental types remain `unknown` until their implementations are validated.

### D.7 — Endpointless and synthetic catalog types are ordinary connections

The following Trino catalog types do not require an external endpoint or credentials:

- Memory
- Black Hole
- Faker
- JMX
- TPC-H
- TPC-DS

They are registered through the same Connection workflow.

Their configuration schemas may be empty or contain only the properties relevant to their catalog implementation.

They still receive normal connection ownership, authorization and lifecycle handling.

Their catalog configuration is persisted through the same catalog-store mechanism as any other Trino-backed connection.

This means the invariant that every connection has a meaningful external endpoint is no longer valid.

The current `catalog_connections.url` column remains a legacy compatibility field and is still `NOT NULL`. For endpointless connection types, the application must persist an empty value rather than inventing an external URL.

`providers.LegacyURL` must return an empty value for types without a meaningful endpoint.

The current `catalogs.warehouse` validation must also become type-aware. An endpointless catalog must not be required to provide a fictitious warehouse value.

The existing database column may remain non-null with an empty value where required by the current schema.

The existing `Provider` contract requires `Ping` and `OpenMetadata`. Endpointless providers must implement those operations according to their actual behavior:

- A local synthetic catalog does not perform a remote network ping.
- Metadata may be read through the Trino binding.
- An unsupported direct metadata operation must return `ErrUnsupported`.
- Catalog registration and runtime availability are validated through the Trino path.

#### System catalog

`system` is reserved and already exists in Trino.

**Proposal (open):** `system` is not registered as a user-created connection. It is
represented as `not_applicable` in the interoperability inventory and excluded from
connection creation, preserving the existing system catalog restrictions (ADR
0005/0018). This is a proposal, not a decided outcome.

### D.8 — Delta Lake and Hudi are independent connection types

Delta Lake and Hudi are represented as connection types in their own right.

They are not limited to being formats discovered through Hive Metastore or AWS Glue.

The user may register a Delta Lake or Hudi resource through its own connection configuration.

Their Trino catalog mapping and endpoint configuration must be defined independently.

**OPEN:** the exact configuration schemas and supported protocols for Delta Lake and Hudi remain implementation work.

They must not be marked `supported` until the configuration and catalog registration have been validated against the target Trino version.

### D.9 — The 37 Trino catalog types are represented incrementally

The inventory must cover the 37 catalog types identified for the Trino integration.

Representation does not mean immediate implementation.

Each type must have an explicit status:

- `supported`
- `experimental`
- `planned`
- `unsupported`
- `not_applicable`

The initial implementation formalizes:

- The existing 12 Trino-bindable connection kinds.
- The existing eight direct query-engine kinds.
- Existing catalog managers and metastores.
- Existing engine-family connections.

The remaining Trino catalog types are added incrementally.

A type is marked `supported` only after its configuration schema, catalog mapping, secret handling, plugin requirements and runtime behavior have been validated.

The `planned` status must not be treated as permission to create the connection.

The API must reject creation of planned or unsupported types.

### D.10 — Connection categories are presentation metadata

The current `connections.Family` enum contains:

- `iceberg`
- `metastore`
- `database`
- `engine`

This is insufficient for the expanded inventory.

The registry must support categories appropriate to the new types, including:

- Relational databases.
- Cloud data platforms.
- Lakehouse and table formats.
- Streaming and messaging.
- NoSQL and search.
- Analytics and observability.
- Synthetic data.
- Internal catalogs.
- Engines.

Categories are used only for UI organization.

They must not determine access mode, engine compatibility or catalog mapping.

---

## Registry (target)

The following is the initial registry inventory.

The catalog type values are internal Trino mappings, not user-facing connector selections.

| Kind | Display name | Category | Access mode | Direct engine | Trino catalog types | Status |
|---|---|---|---|---|---|---|
| `postgresql` | PostgreSQL | Relational database | `direct`, `federated` | `postgresql` | `postgresql` | supported |
| `mysql` | MySQL | Relational database | `direct`, `federated` | `mysql` | `mysql` | supported |
| `mariadb` | MariaDB | Relational database | `direct`, `federated` | `mariadb` | `mariadb` | supported |
| `sqlserver` | SQL Server | Relational database | `direct`, `federated` | `sqlserver` | `sqlserver` | supported |
| `oracle` | Oracle | Relational database | `direct`, `federated` | `oracle` | `oracle` | supported |
| `sqlite` | SQLite | Relational database | `direct` | `sqlite` | — | supported |
| `clickhouse` | ClickHouse | Analytical database | `direct` | `clickhouse` | `clickhouse` | direct supported / Trino planned |
| `flightsql` | Apache Arrow Flight SQL | Engine service | `direct` | `flightsql` | — | supported |
| `polaris` | Apache Polaris | Catalog manager | `federated` | — | `iceberg` | supported |
| `nessie` | Nessie | Catalog manager | `federated` | — | `iceberg` | supported |
| `iceberg` | Iceberg REST | Catalog manager | `federated` | — | `iceberg` | supported |
| `lakekeeper` | Lakekeeper | Catalog manager | `federated` | — | `iceberg` | supported |
| `unity` | Unity Catalog | Catalog manager | `federated` | — | `iceberg` | supported |
| `hive` | Hive Metastore | Metastore | `federated` | — | `iceberg`, `hive`, `delta_lake` | supported |
| `glue` | AWS Glue | Metastore | `federated` | — | `iceberg`, `hive`, `delta_lake` | supported |
| `delta` | Delta Lake | Lakehouse | `federated` | — | `delta_lake` | planned |
| `hudi` | Apache Hudi | Lakehouse | `federated` | — | `hudi` | planned |
| `bigquery` | BigQuery | Cloud data platform | `federated` | — | `bigquery` | planned |
| `snowflake` | Snowflake | Cloud data platform | `federated` | — | `snowflake` | planned |
| `redshift` | Amazon Redshift | Cloud data platform | `federated` | — | `redshift` | planned |
| `singlestore` | SingleStore | Relational database | `federated` | — | `singlestore` | planned |
| `exasol` | Exasol | Relational database | `federated` | — | `exasol` | planned |
| `cassandra` | Apache Cassandra | NoSQL | `federated` | — | `cassandra` | planned |
| `mongodb` | MongoDB | NoSQL | `federated` | — | `mongodb` | planned |
| `redis` | Redis | Key-value store | `federated` | — | `redis` | planned |
| `elasticsearch` | Elasticsearch | Search | `federated` | — | `elasticsearch` | planned |
| `opensearch` | OpenSearch | Search | `federated` | — | `opensearch` | planned |
| `druid` | Apache Druid | Analytics | `federated` | — | `druid` | planned |
| `pinot` | Apache Pinot | Analytics | `federated` | — | `pinot` | planned |
| `duckdb` | DuckDB | Analytical database | `federated` | — | `duckdb` | planned |
| `ignite` | Apache Ignite | Distributed database | `federated` | — | `ignite` | planned |
| `kafka` | Apache Kafka | Streaming | `federated` | — | `kafka` | planned |
| `loki` | Grafana Loki | Observability | `federated` | — | `loki` | planned |
| `prometheus` | Prometheus | Observability | `federated` | — | `prometheus` | planned |
| `gsheets` | Google Sheets | External service | `federated` | — | `gsheets` | planned |
| `faker` | Faker | Synthetic data | `federated` | — | `faker` | planned |
| `memory` | Memory | Internal catalog | `federated` | — | `memory` | planned |
| `blackhole` | Black Hole | Internal catalog | `federated` | — | `blackhole` | planned |
| `jmx` | JMX | Internal / monitoring | `federated` | — | `jmx` | planned |
| `tpch` | TPC-H | Synthetic data | `federated` | — | `tpch` | planned |
| `tpcds` | TPC-DS | Synthetic data | `federated` | — | `tpcds` | planned |
| `system` | System | Internal catalog | — | — | reserved | not_applicable |

The following entries remain unresolved:

| Entry | Issue | Proposed treatment |
|---|---|---|
| `lakehouse` | No corresponding Trino catalog type established in the current inventory | Do not expose as a connection type until its meaning is defined |
| `thrift` | May refer to the Hive Metastore Thrift transport rather than an independent Trino catalog type | Fold into Hive Metastore if confirmed |

The Trino engine itself is not included in the catalog inventory because it is an engine-family connection, not a Trino data catalog type.

Doris and StarRocks remain engine-family connection types, not Trino catalog types.

DuckDB must remain Trino-only in this scope. Direct DuckDB access remains excluded by ADR 0006 because the Baleia build uses `CGO_ENABLED=0`.

---

## Access-mode matrix

| Connection type | Direct | Federated |
|---|---|---|
| PostgreSQL, MySQL, MariaDB, SQL Server, Oracle | supported | supported |
| SQLite, Flight SQL | supported | not applicable |
| ClickHouse | supported | planned |
| Polaris, Nessie, Iceberg REST, Lakekeeper, Unity | not applicable | supported |
| Hive Metastore, Glue | not applicable | supported |
| Delta Lake, Hudi | not applicable | planned |
| BigQuery, Snowflake, Redshift, SingleStore, Exasol | not applicable | planned |
| Cassandra, MongoDB, Redis, Elasticsearch, OpenSearch | not applicable | planned |
| Druid, Pinot, DuckDB, Ignite | not applicable | planned |
| Kafka, Loki, Prometheus, Google Sheets | not applicable | planned |
| Memory, Black Hole, Faker, TPC-H, TPC-DS, JMX | not applicable | planned |
| Trino, Doris, StarRocks | engine-family resource | — |
| System | — | not_applicable |

---

## Implementation requirements

### CR-01 — Extend the existing Provider contract

Extend `internal/connections.Provider` with:

- Explicit access modes.
- Trino catalog mapping metadata.
- Capability metadata.
- Category metadata.
- Description metadata.
- Version requirements.

Do not introduce a separate connection configuration abstraction.

The existing `ConfigSchema`, `Validate`, `EngineKind`, `Ping`, `OpenMetadata` and `Status` behavior remains part of the provider contract.

### CR-02 — Persist the selected access mode

Add an `access_mode` column to `catalog_connections`.

It must be populated at connection creation.

For engine-family connections, the value is null because those connections do not represent a data access path.

The backend must validate the selected mode against the connection type definition.

### CR-03 — Replace binder-owned connection mappings

Remove the connection-kind-to-Trino-catalog mapping from `Binder.Supports()`.

The connection type registry becomes the source of truth.

The binder retains projection mechanics and engine-specific catalog lifecycle operations.

### CR-04 — Extend the connection provider API

Extend `GET /connection-providers` to expose:

- Access modes.
- Trino catalog mappings.
- Capabilities.
- Version requirements.
- Category metadata.
- Implementation status.

Update the OpenAPI schema and generated API types.

The existing API response fields must remain compatible during the migration where practical.

### CR-05 — Update binding-support semantics

`GET /engines/{id}/binding-support` must no longer be the authoritative source of connection-to-Trino-catalog compatibility.

Engine-specific information such as reserved catalog names and protected property patterns may remain available through this endpoint.

Any compatibility data returned by it must be derived from the connection type registry.

### CR-06 — Remove the frontend connector selector

Remove the `connector` selection state from the user-facing connection wizard.

The frontend must not ask users to select a Trino connector after selecting a connection type.

The backend determines the Trino catalog implementation from the selected connection type and its declared mappings.

For one-to-many mappings, the UI displays the logical catalog outputs, not the internal Trino catalog implementation names.

### CR-07 — Implement exclusive access-mode selection

Replace the current independent direct checkbox and federated-engine checkboxes with an exclusive access-mode choice.

When `direct` is selected:

- Create the corresponding native engine.
- Create native catalogs as required.
- Do not create Trino catalog registrations.

When `federated` is selected:

- Do not create a direct engine.
- Register the corresponding logical catalogs and bindings in the selected compatible federated
  engine or engines.
- Persist the resulting catalog configurations through the catalog store.

Selecting multiple federated engine instances, if supported, remains a choice of where the
connection is exposed. It does not create additional access modes or new engines.

This connection workflow does not replace standalone federated-engine registration. The Engines
page retains **Register Engine**, and one backend operation creates the engine-family connection
and federated engine. The connection-level **Register as Engine** action is removed because native
engines are created automatically with `direct` connections.

### CR-08 — Support multiple catalog registrations per connection

The connection type mapping must support zero, one or multiple Trino catalog registrations.

Each resulting catalog is represented through the existing `Catalog` and `Binding` concepts.

The implementation must preserve the Hive/Glue one-to-many mapping and table redirection behavior.

### CR-09 — Support endpointless connections

Allow connection types whose configuration schema does not require an external endpoint or credentials.

For endpointless types:

- `LegacyURL` returns an empty string.
- The provider does not invent an endpoint.
- Catalog creation does not require a fictitious warehouse.
- The connection is still registered and authorized normally.
- The Trino catalog configuration is persisted through the existing catalog-store mechanism.

The existing `catalog_connections.url NOT NULL` constraint may remain while the legacy column is in use.

### CR-10 — Make catalog creation type-aware

The current `CreateCatalog` handler requires:

- `name`
- `trino_catalog_name`
- `warehouse`

This must be changed.

`warehouse` is required only when the selected connection type and its Trino mapping require it.

The backend must determine the internal Trino catalog type and projection properties from the connection type definition.

The current `Catalog.connector` field may remain as an internal persisted value because Trino requires it. It must not become a separate connection resource or user-facing configuration step.

### CR-11 — Expand the connection type database constraint

Add a migration expanding `catalog_connections_type_check` to include the new connection kinds.

The current constraint is maintained in:

- `db/migrations/00023_connection_config.sql`
- `internal/store/model/connections.go`

Both the SQL migration and the GORM model declaration must remain consistent.

The provider registry test must continue to verify that every registered kind is permitted by the database constraint.

### CR-12 — Separate static support from runtime availability

The existing provider status model must be expanded.

The target states are:

- `supported`
- `experimental`
- `planned`
- `unsupported`
- `not_applicable`

The existing `preview` state must be migrated to or mapped to `experimental`.

A planned type must not be creatable.

The current `core.provider()` implementation rejects `unsupported` but would otherwise accept additional states. It must be changed to explicitly allow only `supported` and `experimental`.

Deployment-specific availability must not be confused with static implementation status.

For example, SQLite remains a supported connection type even when `BALEIA_SQLITE_ROOT` is not configured. In that deployment, it is unavailable at runtime, not unsupported by the Baleia implementation.

The frontend must display the new statuses and prevent creation of planned, unsupported and not-applicable types.

### CR-13 — Make category metadata authoritative

Extend the existing provider category/family metadata to support the expanded inventory.

Remove hardcoded connection-family ordering and descriptions from the frontend.

The backend registry must expose category display names, descriptions and ordering.

### CR-14 — Preserve direct engine capabilities

The existing direct engine registry in `internal/engines` remains authoritative for the native query-engine implementations.

Connection type declarations must agree with the engine registry.

A type cannot declare direct support if its corresponding engine implementation is unavailable.

### CR-15 — Preserve secret handling and network security

All new connection types must use the existing credential encryption and secret handling mechanisms.

External network access must continue through `connections.Dialer` as required by ADR 0011.

A Trino catalog registration must continue to use the catalog-store secret contract defined by ADR 0003.

Endpointless and synthetic types must not introduce unnecessary network access.

### CR-16 — Preserve existing catalog lifecycle

The existing catalog-store lifecycle remains unchanged.

The implementation must preserve:

- Registry staging.
- Catalog DDL.
- Plugin confirmation.
- Desired, effective and observed states.
- Managed/adopted provenance.
- Idempotent lifecycle operations.
- Reserved catalog-name validation.

### CR-17 — Migrate existing connections

The migration must preserve:

- Connection identifiers where possible.
- Catalog identifiers.
- Engine identifiers.
- Binding identifiers.
- Trino catalog names.
- Secret references and protected values.
- Workspace grants.
- Connection ownership.

Connections that currently use both direct and Trino-backed federated access must be split into two
connections.

Recommended migration policy:

1. Preserve the original connection ID for the Trino-backed connection, because its existing catalogs already reference that connection.
2. Create a new connection ID for the direct access path.
3. Copy the connection configuration and credentials to the new connection.
4. Re-seal credentials using the new connection identity where required by the encryption contract.
5. Update the existing direct engine's `connection_id` to the new direct connection.
6. Preserve the existing Trino catalogs, bindings, grants and their identifiers.
7. Assign `access_mode = federated` to the original connection and `access_mode = direct` to the new connection.

Existing persisted `access_mode=trino` values are migrated to `federated` without changing
their Trino catalogs or bindings. The rollout must preserve semantics in mixed-version deployments.

The migration must report every connection that requires splitting.

It must not silently discard either access path.

---

## Migration details

### Database

- Add `access_mode` to `catalog_connections`.
- Expand `catalog_connections_type_check`.
- Update `internal/store/model/connections.go`.
- Keep the legacy `url` column non-null while it remains in use.
- Permit endpointless connection types to persist an empty URL.
- Update catalog creation validation so `warehouse` is conditional.
- Preserve existing catalog and binding constraints.

### Provider registry

Update:

- `internal/connections/connections.go`
- `internal/connections/providers/providers.go`
- Provider implementations.
- Provider registry tests.
- Generated `docs/connections.md`.

Every new connection type must be registered through the existing provider registry.

### API

Update:

- `internal/api/connection_config.go`
- Connection creation and update handlers.
- Provider serialization through `providerToPublic`.
- Binding support serialization.
- OpenAPI definitions.
- Generated API models.

The provider endpoint must expose the new registry metadata.

### Binder

Update:

- `internal/bindings/bindings.go`
- `internal/bindings/trino/binder.go`
- Binding projection validation.
- Catalog registration handlers.
- Tests for supported connection types and mappings.

The binder must no longer own the authoritative connection-kind-to-Trino-catalog mapping.

### Frontend

Update:

- `web/src/lib/connections/providers.ts`
- `web/src/lib/connections/ConnectionWizard.svelte`
- `web/src/lib/connections/ProviderForm.svelte`
- Connection API types and generated schemas.
- Connection details pages.

The wizard must use one exclusive access-mode selection.

The connector selector must be removed.

One-to-many mappings must be displayed as logical catalog outputs.

---

## Acceptance criteria

The change is complete when:

1. Every supported resource is registered through the unified Connection workflow.
2. No separate user-facing Connector resource or configuration workflow exists.
3. Connection types declare their access modes.
4. A connection has exactly one data access mode when it represents a data source.
5. Engine-family connections have no data access mode.
6. PostgreSQL, MySQL, MariaDB, Oracle and SQL Server support both direct and federated access where implemented.
7. Direct access creates a native engine and does not create Trino catalog registrations.
8. Federated access uses selected existing compatible engines and creates no engine.
9. All 37 identified Trino catalog types are represented in the interoperability inventory with explicit status.
10. Planned types cannot be created.
11. Endpointless types can be registered without fabricated endpoints or credentials.
12. Catalog creation does not require an irrelevant warehouse value.
13. Hive and Glue retain their one-to-many mappings.
14. Hive table redirection continues to work with active sibling catalogs.
15. Trino catalog configurations are persisted through `baleia-trino-catalog-store`.
16. The UI does not expose a separate connector selection.
17. The interoperability matrix distinguishes static support, runtime availability and operational state.
18. Capabilities are declared per connection type and access mode.
19. Existing connections are migrated without silently losing access paths.
20. Existing catalog IDs, bindings, grants and ownership are preserved.
21. A Trino instance can be registered from the Engines page before any data-source connection.
22. **Register Engine** remains available for standalone federated engines.
23. Direct connection creation automatically and atomically creates its native engine.
24. The obsolete connection-level **Register as Engine** action is absent.

---

## Open decisions

1. **Delta Lake and Hudi:** finalize their endpoint protocols and configuration schemas.
2. **Thrift:** confirm whether it is a separate connection type or part of Hive Metastore configuration.
3. **Lakehouse:** confirm whether this is an actual connection type or only a UI category.
4. **System:** retain as `not_applicable` and exclude it from user registration.
5. **Hive/Glue one-to-many UX:** confirm whether all three catalog outputs should be created by default or whether the user should choose which logical formats to expose.
6. **Trino plugin inventory:** define how deployment-level plugin availability is reported, since existing catalog observation alone cannot enumerate unused installed plugins.

---

## Current compliance against the code

Not compliant. This is the target state.

The following pieces already exist:

- `internal/connections.Provider` and `connections.Registry`.
- Provider configuration schemas and validation.
- `GET /connection-providers`.
- Direct engine registration through the existing engine registry.
- Trino binder and catalog-store integration.
- Trino catalog registration lifecycle.
- Hive/Glue table redirection.
- Connection credential encryption.
- Secret references and protected catalog properties.
- Catalog and binding lifecycle state.
- Engine catalog observations.

The following changes are required:

| Area | Current implementation | Required change |
|---|---|---|
| Connection type metadata | Provider owns kind, label, family, roles and config schema | Add access modes, catalog mappings, capabilities, version requirements and category metadata |
| Access mode | Implicit through provider roles and binder support | Explicit, persisted and exclusive |
| Trino mapping | `Binder.Supports()` | Derived from connection type metadata |
| Provider status | `supported`, `preview`, `unsupported` | Add `experimental`, `planned`, `not_applicable` and separate runtime availability |
| Connection wizard | Independent direct and federated selections | Exclusive access-mode selection |
| Catalog configuration | One connector selection per wizard operation | Internal mapping driven by connection type, including one-to-many mappings |
| Catalog creation | Requires warehouse | Conditional according to connection type |
| Endpointless types | Provider contract and legacy URL assume external resources | Support empty configuration and no external endpoint |
| Database constraint | 18 current connection kinds | Expand for the new connection types |
| Frontend categories | Hardcoded family labels, descriptions and ordering | Registry-driven category metadata |
| Runtime plugin availability | Existing catalog observation | Explicit deployment-level availability mechanism |
| Migration | No explicit access mode | Backfill and split existing dual-path connections |

### Existing code references

- `internal/connections/connections.go`
- `internal/connections/providers/providers.go`
- `internal/api/connection_config.go`
- `internal/api/handlers_bindings.go`
- `internal/bindings/bindings.go`
- `internal/bindings/trino/binder.go`
- `internal/bindings/redirection.go`
- `internal/api/handlers_catalogs_write.go`
- `internal/queryengine/registry.go`
- `internal/engines/engines.go`
- `web/src/lib/connections/providers.ts`
- `web/src/lib/connections/ConnectionWizard.svelte`
- `db/migrations/00023_connection_config.sql`
- `internal/store/model/connections.go`

---

## Consequences

- One connection workflow represents every supported resource type.
- The user selects a connection type and, when applicable, one access mode.
- Direct and federated access are independent connections, even when they point to the same external system.
- Standalone federated-engine registration remains independent of both data-source workflows.
- Trino catalog implementation details remain internal to the connection type and binding implementation.
- The binder retains engine-specific projection and lifecycle responsibilities without owning connection compatibility.
- Synthetic and internal Trino catalogs become ordinary registered connections.
- One connection may generate multiple Trino catalog registrations.
- The existing catalog-store contract remains intact.
- The interoperability matrix becomes the authoritative description of supported types, access paths, capabilities and availability.
- The frontend no longer needs to reconstruct compatibility from separate provider and binder registries.
