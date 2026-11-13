# 0029. Connection and engine registration lifecycles

- Status: Proposed
- Date: 2026-11-13
- Issues: #998
- Builds on: [0002](0002-connections-engines-catalogs-bindings.md),
  [0006](0006-multi-engine-scope.md), [0021](0021-unified-connection-model.md)
- Amends: ADR 0002's direct-engine registration flow and ADR 0021's access-mode
  vocabulary and registration lifecycle.

## Context

ADR 0002 separates four concepts that remain valid:

- A **connection** stores the configuration and credentials for an external resource.
- An **engine** is a concrete query-engine instance backed by a connection.
- A **catalog** is a logical data container owned by a connection or native to an engine.
- A **binding** exposes a catalog through an engine.

Registration nevertheless conflates three independent choices:

- `access_mode`: how Baleia accesses a data source.
- `engine_kind`: the query-engine implementation, such as `trino` or `postgresql`.
- `engine_id`: one registered engine instance.

ADR 0021 originally named the access modes `direct` and `trino`. Trino is an engine kind, not an
access mode. That vocabulary would require another access mode for Doris, StarRocks and every
future federated engine.

The creation lifecycle is also split across frontend requests. A direct data-source connection can
be committed before its native engine, catalogs and bindings are created. A federated data-source
connection can be committed before its logical catalogs, bindings and desired registration state.
Failures therefore leave partial resources and make the frontend responsible for backend
orchestration.

At the same time, standalone registration of federated query-engine instances is a separate,
permanent requirement. A user must be able to register a Trino instance before registering any
data source and before creating any catalog binding. Removing standalone registration would make
the federated data-source workflow impossible to bootstrap.

The architecture consequently has three registration workflows. They must not be merged:

1. Register a standalone federated engine.
2. Register a direct data-source connection and its native engine.
3. Register a federated data-source connection against existing federated engines.

## Decision

### 1. Access modes are `direct` and `federated`

Data-source connections have exactly one immutable access mode:

| Access mode | Meaning |
|---|---|
| `direct` | Execute SQL through the data source's native query-engine adapter. |
| `federated` | Execute SQL through one or more separately registered federated engines. |

`trino`, `doris`, `starrocks`, `postgresql` and similar values are engine kinds. An
`engine_id` identifies a concrete registered instance. Adding a federated engine kind does not
add an access mode.

Connections whose family is itself a query engine, such as Trino, configure an engine instance;
they are not data-source access paths and have no `access_mode`.

Provider metadata declares the access modes a data-source type supports. A provider's capabilities
describe what is possible; the persisted access mode records what was selected for one connection.
Direct capability must never override a connection configured as `federated`.

A PostgreSQL server may have two Baleia connections, one `direct` and one `federated`. They have
independent configuration, credentials, ownership, authorization and lifecycle.

### 2. The wizard makes execution mode explicit

For a provider supporting both modes, the connection wizard displays an **Execution mode** choice
and does not preselect either option:

**Direct**

Execute queries directly against this data source.

**Through a query engine**

Execute queries using a registered query engine.

When a provider supports only one mode, the wizard selects it automatically. The backend validates
the submitted mode independently. Access mode cannot be changed after creation; registering the
other access path creates another connection.

When `federated` is selected, the wizard lists compatible registered federated engine instances.
Compatibility is derived from the backend provider, engine and binder registries, not from a
Trino-specific frontend list. Each option identifies both the engine instance and its kind.

### 3. Workflow A registers a standalone federated engine

The Engines page retains a **Register Engine** button. It starts a workflow for selecting and
configuring a federated engine kind. Trino is currently supported; Doris, StarRocks and other
federated engines can be added through the registries.

The backend owns the entire operation:

1. Validate authorization, engine kind, configuration, credentials and name.
2. Perform required remote validation before opening a database transaction.
3. Persist the engine-family connection and its engine resource in one transaction.
4. Return the registered engine, which may initially have no user-defined catalog bindings.

The user does not first register a data-source connection and the frontend does not coordinate two
creation requests. The public API must preserve a standalone federated-engine registration
operation. `POST /engines` may be adapted or replaced, but it must not be deleted without an
equivalent public operation.

The engine registry remains the source of supported engine kinds. This workflow is not the old
connection-level **Register as Engine** action.

### 4. Workflow B registers a direct data-source connection

For `access_mode=direct`, one backend creation operation:

1. Validates authorization, provider support, configuration and the derived engine name.
2. Checks connectivity and discovers native catalogs before opening a transaction.
3. Seals credentials.
4. Persists the connection, exactly one native engine, native catalogs and native bindings in one
   database transaction.
5. Commits and only then starts metadata synchronization.

The frontend makes one request. The user never registers the native engine separately. The adapter
is the provider's native query-engine adapter and must not be generically described as JDBC.

The existing persistence behavior in `internal/engines/manage` is reused inside the encompassing
transaction. It must not be duplicated or invoked through a nested independent transaction.

Remote connectivity and discovery do not run while the database transaction is open. A failure
before commit leaves no new connection, engine, catalog or binding. Uniqueness constraints and
idempotent orchestration prevent concurrent requests from creating duplicate resources.

### 5. Workflow C registers a federated data-source connection

For `access_mode=federated`, the user selects one or more existing compatible federated engine
instances. One backend creation operation:

1. Validates authorization, provider support, configuration, selected engine instances and
   provider-to-engine binder compatibility.
2. Validates or discovers initial logical catalogs and registration options before opening a
   transaction where remote work is required.
3. Seals credentials.
4. Persists the data-source connection, logical catalogs, bindings and desired catalog-registration
   state in one database transaction.
5. Commits, then applies external catalog configuration through the existing reconciliation
   lifecycle and starts metadata synchronization as appropriate.

This workflow creates no engine. Selecting `trino-production` uses that registered Trino instance;
it does not create another Trino engine or a PostgreSQL native engine.

The database transaction owns Baleia's authoritative desired state. External Trino, Doris or
StarRocks operations cannot participate in it. Desired, effective and observed state remain
distinct. A committed registration may be pending or failed externally, and that status must be
observable and recoverable through reconciliation.

### 6. API and authorization boundaries

The three public operations remain independent:

| Operation | Initiated from | Creates |
|---|---|---|
| Register federated engine | Engines | Engine-family connection + federated engine |
| Register direct data source | Connections | Data-source connection + native engine + native catalogs/bindings |
| Register federated data source | Connections | Data-source connection + logical catalogs/bindings + desired registration state |

Each operation is one backend-owned creation request from the frontend. An endpoint may use an
internal service shared with another endpoint, but the public contracts and user intent remain
separate.

The connection-level `register_engine` allowed action and **Register as Engine** UI are removed.
They incorrectly advertise native-engine registration on any writable connection. Their removal
does not remove standalone federated-engine registration or the Engines page button.

Errors distinguish authorization, validation, connectivity, compatibility, conflict, persistence
and post-commit external activation failures. Engine and connection names, ownership and bootstrap
authorization continue to follow the existing contracts.

### 7. Transaction boundaries

No remote network operation is performed while a Baleia database transaction is open.

- Workflow A atomically persists the engine-family connection and federated engine.
- Workflow B atomically persists the direct connection, native engine, native catalogs and native
  bindings.
- Workflow C atomically persists the federated connection, logical catalogs, bindings and desired
  external-registration state.

Post-commit metadata synchronization and external catalog activation are not evidence that the
database transaction failed. Their state and failures are recorded for retry and inspection.

### 8. Existing `trino` access modes are migrated

Persisted values are migrated as follows:

| Existing value | Result |
|---|---|
| `direct` | `direct` |
| `trino` | `federated` |
| `NULL` on engine-family connections | `NULL` |

The migration updates database constraints, backend constants, provider metadata, OpenAPI,
generated Go and TypeScript clients, frontend code, tests and documentation. Existing engine,
catalog and binding identifiers and relationships are preserved. A migrated Trino-backed
connection does not acquire a native engine.

The rollout must tolerate mixed application versions without letting old and new binaries assign
different semantics to the same stored value. It uses the repository's expand/contract migration
conventions where compatibility requires them.

### 9. Incomplete direct connections are reconciled

The old two-request workflow may have persisted `direct` connections without native engines. An
idempotent reconciliation path:

1. Finds direct connections with no associated engine.
2. Resolves and validates stored configuration and credentials.
3. Checks connectivity and discovers native catalogs outside the transaction.
4. Transactionally creates the missing engine, native catalogs and native bindings.
5. Rechecks invariants under concurrency and creates no duplicates.
6. Records failures for operator inspection and retry.

Reconciliation never deletes an existing connection as a repair strategy.

### 10. Required implementation changes

The implementation of this decision must update, as applicable:

- `api/swagger.yaml`, generated server models and the generated TypeScript client.
- Provider, engine and binder registry metadata and compatibility responses.
- Connection registration orchestration and `internal/engines/manage` transaction integration.
- Federated-engine registration so its backend operation owns both connection and engine creation.
- `internal/api/actions.go`, removing only obsolete connection-level `register_engine` semantics.
- `internal/api/handlers_engines.go` without removing public federated-engine registration.
- `internal/api/handlers_catalog_connections.go` for direct and federated atomic creation.
- `ConnectionWizard.svelte` for explicit mode and compatible-engine selection.
- The Engines page, preserving **Register Engine** for federated engines.
- Connection pages, removing **Register as Engine**.
- Store migrations and the incomplete-direct reconciliation path.
- Bruno, backend, frontend, migration, concurrency and end-to-end tests for all three workflows.

## Consequences

- Access mode describes execution architecture, not a product.
- Standalone federated engines can be registered before any data source and can initially be empty.
- Direct connections always own exactly one automatically created native engine.
- Federated data-source connections reference existing compatible federated engines and create no
  engine.
- Every registration workflow is owned by one backend operation with atomic database persistence.
- External catalog activation preserves the desired/effective/observed lifecycle.
- The obsolete connection-level **Register as Engine** action disappears while the Engines page's
  **Register Engine** action remains.
- Existing Trino relationships survive the vocabulary migration.
- Legacy incomplete direct connections have an observable, idempotent repair path.

## Current compliance

Compliant, with two gaps.

- All three workflows are backend-owned and have a public contract: `POST /engines` (workflow A),
  and `POST /catalog-connections` with `access_mode` `direct` (B) or `federated` (C). Each persists
  its resources in one transaction after the remote checks. The Engines page keeps **Register
  engine**, and connection-level `register_engine` is gone.
- The `trino` access mode is migrated to `federated`. The check constraint keeps accepting `trino`
  during a rolling upgrade, the API reports it as `federated`, and the next start rewrites it.
- Direct connections without an engine are repaired on API start, idempotently and under a row lock,
  and are never deleted.

Gaps:

- Reconciliation failures are recorded in the log only. An operator-visible record (a column or a
  table) needs a schema change and is not part of this decision's implementation.
- The wizard asks "How will you use it?" and its option texts differ from the wording in section 2.
  The behavior is the same: two options, none preselected when both are supported, and the sole
  mode selected automatically.

## Alternatives considered

### Use one access mode per federated engine kind

Rejected. It conflates the access path with engine kind and would repeat the migration for every
new federated engine.

### Remove standalone engine registration

Rejected. Federated data-source registration depends on pre-existing federated engines, and Trino
must be registerable without first creating a data-source connection.

### Keep standalone native-engine registration

Rejected. A native engine is an implementation resource owned by one direct connection and must be
created atomically with it.

### Let the frontend compose creation requests

Rejected. The frontend cannot provide atomic persistence or reliably recover from process,
network and concurrency failures.

### Put external catalog activation in the database transaction

Rejected. External engines do not participate in Baleia's transaction. The existing
desired/effective/observed lifecycle is the appropriate consistency boundary.

### Introduce another domain abstraction

Rejected. Connection, engine, catalog and binding already express every required relationship.
