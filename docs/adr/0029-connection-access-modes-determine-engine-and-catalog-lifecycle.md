# 0029. Connection access modes determine engine and catalog lifecycle

- **Status:** Proposed
- **Date:** 2026-11-13
- **Issues:** #998
- **Builds on:** [0002](0002-connections-engines-catalogs-bindings.md), [0006](0006-multi-engine-scope.md), [0021](0021-unified-connection-model.md)
- **Amends:** ADR 0002's standalone direct-engine registration flow and ADR 0021's access-mode vocabulary and connection-creation lifecycle.

## Context

ADR 0002 separates connections, engines, catalogs and bindings into distinct domain concepts.

A connection represents a configured resource. An engine executes SQL. A catalog represents a logical data container, while a binding exposes that catalog through an engine.

Engines belong to two architectural classes:

- **Direct engines:** Execute SQL through a connection's native query-engine adapter, such as PostgreSQL, MySQL or ClickHouse.
- **Federated engines:** Execute SQL against catalogs exposed through a separate query-engine instance, such as Trino, with Doris and StarRocks planned for future support.

ADR 0021 introduced an exclusive `access_mode` for data-source connections, initially using `direct` and `trino`.

However, `trino` identifies a specific engine implementation rather than an architectural access mode.

This creates an unnecessary dependency between the connection model and Trino. Supporting Doris or StarRocks should not require introducing additional access modes or changing the connection lifecycle again.

The appropriate distinction is between direct and federated execution.

### Current implementation

The backend already declares supported access modes through `connections.TypeMeta.AccessModes` and persists the selected mode in `catalog_connections.access_mode`.

The connection wizard also allows the user to select a supported mode.

However, several inconsistencies remain.

**Direct connections are created through two API requests.**

`ConnectionWizard.svelte` first calls `POST /catalog-connections`, then `POST /engines`.

If engine registration fails, an incomplete direct connection remains persisted.

**Federated connections are also assembled through multiple requests.**

The wizard creates the connection, then creates logical catalogs and their bindings through subsequent API operations.

This makes the frontend responsible for coordinating resource creation and handling partial failures.

**Standalone direct-engine registration remains available.**

`catalogConnectionActions` (`internal/api/actions.go`) grants `register_engine` to every writable connection.

Consequently, the Engines page and connection detail pages offer engine registration for catalog managers and metastores that cannot execute SQL.

`CreateEngine` (`internal/api/handlers_engines.go`) checks `RoleDirect`, but does not validate the connection's persisted access mode.

A provider may support direct execution even when a particular connection was explicitly configured for federated access.

**Provider capabilities and connection configuration are being conflated.**

A PostgreSQL provider may support both execution paths.

A specific PostgreSQL connection must use exactly one selected access mode.

The provider's supported modes describe what is possible; the connection's persisted mode describes what was chosen.

The connection-creation lifecycle must enforce that distinction.

## Decision

### 1. Access modes are direct and federated

Data-source connections support two architectural access modes:

| Access mode | Meaning |
|---|---|
| `direct` | Baleia executes SQL through a native query-engine adapter associated with the connection. |
| `federated` | Baleia executes SQL through a separately registered federated engine exposing the connection's catalogs. |

The existing `trino` access-mode value is replaced by `federated`.

Trino remains an engine kind, not an access mode.

Future support for Doris, StarRocks or another compatible federated engine extends the engine registry and binding implementations without introducing new access-mode values.

The following concepts remain independent:

- **Access mode:** `direct` or `federated`.
- **Engine kind:** `postgresql`, `trino`, `doris`, `starrocks`, etc.
- **Engine instance:** A specific registered query engine identified by its engine ID.

A connection has exactly one access mode when it represents a data source.

Engine-family connections do not have a data-source access mode. Their purpose is to configure federated engine instances.

### 2. Provider metadata declares supported access modes

The provider registry remains the source of truth for supported access modes.

Examples:

| Connection type | Supported access modes |
|---|---|
| PostgreSQL | `direct`, `federated` |
| MySQL | `direct`, `federated` |
| MariaDB | `direct`, `federated` |
| SQL Server | `direct`, `federated` |
| Oracle | `direct`, `federated` |
| SQLite | `direct` |
| ClickHouse | `direct` until a federated binding is implemented |
| Polaris | `federated` |
| Unity Catalog | `federated` |
| Iceberg REST | `federated` |
| Lakekeeper | `federated` |
| Nessie | `federated` |
| Hive Metastore | `federated` |
| AWS Glue | `federated` |
| Trino | No data-source access mode |

A provider declares a mode only when the corresponding implementation is supported.

Planned engine integrations do not make a mode or a particular engine binding available.

Existing metadata and implementation-status capabilities remain independent of access mode.

### 3. Access mode is selected during connection creation

The connection wizard must expose the supported execution paths before creating a connection.

The user-facing options are:

**Direct**

Baleia queries the source using its native query-engine adapter.

**Through a query engine**

Baleia queries the source through a compatible federated engine.

The selection rules are:

1. If the provider supports both modes, the user must explicitly choose one.
2. If the provider supports exactly one mode, it is selected automatically.
3. If the provider supports no data-source access mode, the resource follows its engine-family lifecycle.
4. Unsupported access modes are never displayed.
5. The backend independently validates the requested mode against provider metadata.
6. A connection's access mode is immutable after creation.

The frontend must not silently default to direct or federated when both options are supported.

The selected mode determines the resources created by the backend.

### 4. Federated execution requires engine selection

When the user selects `federated`, the wizard presents the compatible registered federated engines.

The engine selection is a separate dimension from access mode.

For example:

- Access mode: `federated`
- Engine instance: `trino-production`
- Engine kind: `trino`

The interface displays engine instances, including their names and kinds, rather than asking users to configure an abstract connector implementation.

Only engines with a validated, supported binding implementation for the selected connection type may be offered.

Compatibility must come from the backend's provider and binder registries, including applicable runtime availability checks.

The initial implementation supports Trino.

Doris and StarRocks become selectable only when their engine adapters and binding implementations are available.

A federated connection may be exposed through multiple compatible engine instances, subject to the existing catalog and binding model.

Selecting more than one engine does not change the connection's access mode.

The connection wizard must require at least one federated engine for a completed federated data-source registration.

### 5. One creation request for both access modes

`POST /catalog-connections` owns the complete initial registration of a data-source connection.

The frontend performs one creation request regardless of the selected access mode.

The request includes:

- Connection type and name.
- Connection configuration and credentials.
- Selected `access_mode`.
- Federated engine selections when `access_mode=federated`.
- Initial catalog registration options where required.

The backend validates the request and orchestrates the appropriate lifecycle.

The frontend does not make subsequent engine-registration or initial catalog-creation calls to complete the new connection.

The API response retains the connection as its primary resource and includes the identifiers or representations needed to navigate to the resources created by the operation.

### 6. Direct connection lifecycle

A `direct` connection automatically owns exactly one native engine.

The backend:

1. Validates authorization, provider support, configuration and engine naming.
2. Checks source connectivity.
3. Discovers the source's native catalogs.
4. Seals credentials.
5. Begins a database transaction.
6. Persists the connection with `access_mode=direct`.
7. Persists its native engine.
8. Persists native catalogs.
9. Persists native bindings.
10. Commits the transaction.
11. Schedules metadata synchronization after commit.

The native engine name is derived from the connection name using the existing engine-name normalization and validation rules.

The user does not separately select or register a native engine.

The transaction must include all database resources required for a complete direct connection.

Any failure before commit leaves no newly persisted connection, engine, native catalog or binding.

Connectivity checks and catalog discovery occur before opening the database transaction.

Metadata synchronization occurs after commit and does not determine whether connection creation succeeds.

The existing native-engine persistence logic in `internal/engines/manage` must be reused inside the encompassing transaction.

Nested independent transactions and duplicate implementations of native catalog or binding creation are prohibited.

### 7. Federated connection lifecycle

A `federated` connection does not create a native engine.

Instead, the backend uses the selected existing federated engine instances.

The backend:

1. Validates authorization, provider support and connection configuration.
2. Validates the selected engine instances.
3. Verifies provider-to-engine binding compatibility.
4. Validates catalog names, formats and required registration options.
5. Performs external validation and discovery that can safely occur before persistence.
6. Seals credentials.
7. Begins a database transaction.
8. Persists the connection with `access_mode=federated`.
9. Persists its logical catalogs.
10. Persists bindings and the corresponding desired catalog-registration state.
11. Commits the transaction.
12. Applies the desired catalog configuration to the selected engines through the existing binding lifecycle.
13. Records observed state and synchronization outcomes.

The database transaction covers the application's authoritative desired state.

External engine operations are not part of the database transaction.

For Trino, the existing catalog-store lifecycle remains authoritative:

- Persist desired catalog configuration.
- Stage catalog registration.
- Apply the catalog through Trino.
- Confirm the desired version.
- Observe the resulting catalog state.

The initial creation request does not bypass or replace this lifecycle.

The API must distinguish between:

- **Creation committed:** The connection and desired catalog/binding state have been persisted.
- **Catalog active:** The federated engine has successfully applied and confirmed the registration.
- **Catalog pending or failed:** Registration is awaiting application or reconciliation.

A successful database commit must never be presented as proof that external catalog activation has completed.

If external application fails after commit, the desired state remains persisted and observable through the existing binding lifecycle.

The reconciler retries or repairs the failed application according to the existing catalog-store guarantees.

### 8. Access-mode isolation

The persisted access mode controls which lifecycle operations are permitted.

A connection with `access_mode=direct`:

- Owns a native engine.
- Uses the native query-engine adapter.
- Does not create federated bindings as part of its access path.

A connection with `access_mode=federated`:

- Does not own a native engine.
- Uses one or more compatible federated engines.
- Exposes its catalogs through the existing binding system.

The backend must not infer the active access mode from `RoleDirect`, provider kind, category or the presence of an engine implementation.

The same external PostgreSQL server may be represented by two independent connections:

- One configured for direct execution.
- One configured for federated execution.

These connections have independent credentials, ownership, lifecycle and authorization.

The frontend cannot change a connection's access mode after creation.

Changing the access path requires registering a separate connection.

### 9. Remove standalone direct-engine registration

The standalone `register_engine` action is removed.

This includes:

- The Engines page's direct-engine registration dropdown.
- The Connections page's standalone registration button.
- The connection detail page's **Register as Engine** action.
- `register_engine` in connection `allowed_actions`.
- The obsolete `action=register_engine` API filtering contract.
- The public direct-engine registration operation `POST /engines`.

The persistence functionality currently used by `POST /engines` remains available internally to the connection-creation service.

Federated-engine bootstrap and registration remain separate from direct-connection creation and are not removed.

The Engines page continues to display and administer registered engines.

Creating a direct engine is achieved by creating a direct connection.

### 10. Transaction boundaries and failure handling

Connection creation has one backend orchestration entry point, but transaction boundaries must reflect the underlying systems.

**Direct mode**

The connection, engine, native catalogs and native bindings commit atomically in the Baleia database.

A failure before commit leaves none of those newly created resources.

**Federated mode**

The connection, logical catalogs, bindings and desired registration state commit atomically in the Baleia database.

External catalog application follows the existing desired/effective/observed lifecycle.

A failed external operation does not retroactively roll back a committed database transaction.

Both modes must avoid holding database transactions open during external network operations.

Errors must distinguish validation, authorization, connectivity, name conflicts, persistence failures and federated activation failures.

Creation retries must not silently duplicate engines, catalogs or bindings.

### 11. Migration of existing access modes

Existing persisted values must be migrated:

| Existing value | New value |
|---|---|
| `direct` | `direct` |
| `trino` | `federated` |
| `NULL` for engine-family resources | `NULL` |

Provider metadata must replace `AccessModeTrino` with `AccessModeFederated`.

The migration includes:

- Persisted `catalog_connections.access_mode` values.
- Database constraints and validation.
- Backend access-mode constants.
- Provider metadata.
- OpenAPI schemas.
- Generated Go and TypeScript types.
- Frontend mode selection.
- Connection tests and compatibility checks.
- Documentation and examples.

The migration must preserve existing catalog and binding relationships.

A connection previously configured with `access_mode=trino` remains associated with its existing Trino bindings.

It must not acquire a native engine.

No migration may infer `direct` merely because the provider supports direct execution.

The rollout must account for mixed application versions. Old and new binaries must not interpret the same persisted access-mode value differently.

Use the repository's existing expand/contract migration conventions where required.

### 12. Reconciliation of incomplete direct connections

The previous two-request workflow may have left connections with `access_mode=direct` but no native engine.

The rollout must identify and reconcile these records.

For each incomplete direct connection:

1. Verify that no engine is already associated.
2. Resolve stored configuration and credentials.
3. Validate connectivity.
4. Discover native catalogs.
5. Create the missing engine, catalogs and bindings in one database transaction.
6. Record failures for operator inspection.

Reconciliation must be idempotent and safe under concurrent execution.

Existing resources must never be duplicated.

The reconciler must not delete incomplete connections without explicit operator action.

New direct connections cannot enter this partial state through the creation API.

### 13. Federated engine registration remains independent

Engine-family connections, including Trino, represent query-engine resources rather than data sources.

They do not use the data-source `direct` or `federated` access modes.

Their own registration and bootstrap lifecycle remains separate.

This ADR does not require implementing Doris, StarRocks or any additional federated engine.

It ensures that adding such engines later does not require redefining data-source access modes.

## Consequences

- Access modes describe execution architecture rather than specific engine products.
- PostgreSQL and other compatible providers can be registered for either direct or federated execution.
- The selected mode becomes authoritative for connection lifecycle.
- Direct connections automatically own a native engine.
- Federated connections use selected existing federated engines.
- Connection creation is initiated through one API request in either mode.
- Database persistence is atomic for all resources belonging to the initial registration.
- External federated catalog activation retains its existing reconciliation and observation guarantees.
- Standalone direct-engine registration disappears from the UI and public API.
- Legacy `trino` access-mode values are migrated to `federated`.
- Existing Trino bindings remain intact.
- Incomplete direct connections are reconciled during rollout.
- Future federated engines extend the registry and binding implementations without introducing new access modes.

## Current compliance

**Partially implemented.**

- Provider metadata already declares supported access modes.
- Connection creation already persists an exclusive access-mode selection.
- The wizard already offers a mode selection when a provider supports multiple modes.
- The current mode vocabulary uses `direct` and `trino`, rather than `direct` and `federated`.
- The wizard currently creates direct engines through a second API request.
- The wizard currently creates federated catalogs and bindings through subsequent API operations.
- `internal/engines/manage` already creates native engines, catalogs and bindings transactionally, but connection creation is outside that transaction.
- The current Trino catalog-store and binding lifecycle already separates desired, effective and observed state.
- `catalogConnectionActions` still grants `register_engine` to every writable connection.
- `CreateEngine` checks provider roles without enforcing the persisted connection access mode.
- Standalone registration remains visible in multiple frontend pages.

Issue #998 implements the complete lifecycle change, the access-mode migration and the corresponding API/frontend updates.

## Alternatives considered

### Keep `trino` as an access mode

Rejected.

Trino is an engine implementation, not an architectural execution path.

Adding Doris or StarRocks would otherwise introduce more engine-specific access modes and duplicate the federated lifecycle.

### Introduce one access mode per federated engine

Rejected.

Modes such as `trino`, `doris` and `starrocks` conflate execution architecture with engine selection.

The engine kind and instance are already represented by the existing engine model.

### Keep standalone direct-engine registration

Rejected.

A direct engine is an implementation resource owned by a direct connection. It should not require a separate user-facing registration action.

### Keep the frontend responsible for creating related resources

Rejected.

The frontend must not orchestrate a multi-resource lifecycle or implement recovery from partial backend persistence.

### Use compensating deletion instead of database transactions

Rejected.

Compensating deletion cannot guarantee atomicity when requests fail, processes crash or concurrent operations occur.

All initial database resources must commit together.

### Require federated catalog activation to participate in the database transaction

Rejected.

External query engines do not participate in Baleia's database transactions.

The existing desired/effective/observed catalog lifecycle provides the appropriate consistency model.

### Allow access-mode changes after creation

Rejected.

Switching from direct to federated execution changes engine ownership, catalog relationships, credentials and lifecycle semantics.

A separate connection must be registered instead.

### Create another abstraction for federated execution

Rejected.

The existing connection, engine, catalog and binding contracts already represent the required relationships.

The change concerns access-mode vocabulary, registration orchestration and lifecycle ownership.
