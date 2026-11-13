# 0029. Direct engine lifecycle is owned by connection creation

- Status: Proposed
- Date: 2026-11-13
- Issues: #998
- Builds on: [0002](0002-connections-engines-catalogs-bindings.md),
  [0006](0006-multi-engine-scope.md), [0021](0021-unified-connection-model.md)
- Amends: the standalone direct-engine registration flow described in
  [0002](0002-connections-engines-catalogs-bindings.md)

## Context

[0002](0002-connections-engines-catalogs-bindings.md) separates connections, engines, catalogs and
bindings. A direct engine executes SQL through its connection's native adapter; a federated engine
hosts catalogs exposed through other connections.

[0021](0021-unified-connection-model.md) defines exclusive data-source access modes:

- `direct`: Baleia executes SQL through the connection's native adapter.
- `trino`: Baleia exposes the connection's resources through an existing Trino engine.

A connection cannot use both modes simultaneously.

The connection wizard already creates a native engine when registering a `direct` connection.
However, it performs two separate API requests:

1. `POST /catalog-connections` persists the connection.
2. `POST /engines` registers the engine and its native catalogs.

This creates two problems.

**Redundant registration.** The standalone `register_engine` action is offered independently of
connection creation. `catalogConnectionActions` (`internal/api/actions.go`) grants it to every
writable connection, including metadata-only catalog managers and metastores. `CreateEngine`
(`internal/api/handlers_engines.go`) rejects providers without `RoleDirect`, so the API advertises
operations it cannot perform.

**Non-atomic creation.** If the connection is persisted but engine registration fails, Baleia
retains an incomplete direct connection. The wizard must coordinate two resources and recover from
failures that should belong to the backend.

The persisted `access_mode` already identifies the connection's intended execution path.
Direct-engine registration should therefore be part of that connection's lifecycle, not a separate
operation.

## Decision

### Direct connections own their engines

Every successfully created connection with `access_mode=direct` has exactly one native engine.

The engine is created automatically by the backend as part of connection creation. Users never
register it separately.

The connection's persisted `access_mode` determines its execution path. Provider metadata
determines whether the requested mode is supported.

- `direct`: creates the connection, native engine, native catalogs and native bindings.
- `trino`: creates the connection without a native engine; its catalog registration and binding
  lifecycle remain governed by ADR 0021.
- No data-source access mode: engine-family resources follow their existing federated-engine
  lifecycle.

Metadata-only catalog managers and metastores never become direct engines.

### One backend operation

`POST /catalog-connections` owns the complete creation of a direct connection and its engine.

The frontend sends one request. It does not call `POST /engines` afterward.

The backend performs:

1. Validate the request, authorization, provider status and selected access mode.
2. Validate the connection configuration and native engine name.
3. Check connectivity and discover native catalogs using the provider.
4. Seal the connection credentials.
5. Begin a database transaction.
6. Insert the connection with `access_mode=direct`.
7. Insert its native engine, referencing the connection.
8. Insert the discovered native catalogs.
9. Insert their native bindings.
10. Commit the transaction.
11. Schedule metadata synchronization after the commit.

The connection and engine are persisted together or not at all.

If validation, connectivity, discovery or any database write fails, the request fails without
leaving a newly persisted partial direct connection.

External connectivity checks and metadata discovery occur **before** the database transaction. No
network operation is performed while holding the transaction open.

Metadata synchronization is post-commit work. Its failure affects metadata freshness, not the
existence or completeness of the connection and engine.

### Reuse existing engine persistence

The implementation reuses the existing native-engine creation logic from `internal/engines/manage`,
including native catalog and binding creation.

The current `manage.Service.Create` already persists those resources transactionally. Its
persistence operations must be made reusable inside the transaction that also creates the
connection.

This must not introduce a second implementation of native catalog or binding creation.

Transaction ownership belongs to the application service orchestrating direct-connection creation.
Nested independent transactions must not be used for the same operation.

The existing store abstraction and transaction conventions remain in effect.

### Engine naming

The native engine uses the connection's name, subject to the existing engine-name validation and
uniqueness rules.

The connection wizard does not require a second engine-registration step or an independently chosen
engine name.

Name conflicts are detected before persistence when possible and enforced by database constraints
during the transaction.

A conflicting name fails the creation request without leaving a partial connection.

### API contract

`POST /catalog-connections` remains the public creation endpoint for data-source connections.

For `access_mode=direct`, a successful response is returned only after the connection, engine,
native catalogs and bindings have been committed.

The response retains the connection resource as its primary representation. The frontend can obtain
the associated engine through the existing engine APIs.

`POST /engines` is no longer a public registration operation for direct connections. Its persistence
functionality is retained internally and invoked by the connection-creation service.

The obsolete public direct-engine registration endpoint and its OpenAPI operation are removed as
part of #998, provided no remaining internal workflow depends on them. Existing federated-engine
registration and bootstrapping are unaffected.

### Remove standalone registration from the UI

The standalone **Register Engine** action is removed from:

- The Engines page.
- The Connections page.
- The connection detail page.

`register_engine` is removed from connection `allowed_actions`, the corresponding API filtering
contract and the generated frontend types.

The Engines page continues to list and administer engines. Creating a direct engine is initiated
through **New Connection**, with `access_mode=direct`.

The frontend must not independently determine whether an existing connection can be registered as a
direct engine.

### Creation failures

A failed direct-connection creation request leaves no newly persisted connection, engine, native
catalog or binding.

The user can correct the configuration and retry the complete creation request.

The backend distinguishes:

- Invalid or unsupported access mode.
- Invalid configuration.
- Connection or engine name conflict.
- Unreachable source or failed native catalog discovery.
- Database persistence failure.

A failure after transaction commit, such as metadata synchronization failure, is reported through
the existing metadata and operational status mechanisms. It does not cause the committed creation
to be presented as rolled back.

The operation does not perform external resource creation that would require compensating
transactions.

### Existing incomplete connections

Connections with `access_mode=direct` but no native engine may already exist because of the previous
two-request workflow.

The migration for #998 must identify these records and provide a deterministic reconciliation path.

For each incomplete direct connection, the reconciler:

1. Validates that no engine is already associated with the connection.
2. Opens its stored configuration and credentials.
3. Validates connectivity and discovers native catalogs.
4. Creates the missing engine, native catalogs and bindings in one transaction.
5. Records failures for operator inspection without deleting or silently changing the connection.

Reconciliation must be idempotent. Existing engines, native catalogs and bindings must never be
duplicated.

New direct connections cannot enter this incomplete state through the creation API after the change.

### Federated engines remain separate

Trino, Doris and StarRocks are engine-family resources, not direct data-source connections.

They do not use `access_mode=direct` and are not created through the direct-connection workflow.

This ADR does not change federated-engine bootstrapping, registration, catalog binding or
reconciliation.

## Consequences

- Creating a direct connection also creates its native engine and associated resources.
- The frontend performs one creation request instead of orchestrating two.
- Database persistence is atomic across connection, engine, native catalogs and native bindings.
- Validation and discovery failures leave no incomplete direct connection.
- Native engine registration is no longer an independent user-facing operation.
- `register_engine` and the obsolete direct-engine registration UI are removed.
- Connections configured for Trino cannot be registered as direct engines.
- Catalog managers and metastores never appear as direct-engine candidates.
- The existing native-engine persistence implementation is reused rather than duplicated.
- Previously incomplete direct connections require reconciliation during the rollout.
- Metadata synchronization remains asynchronous and independently observable.
- Federated-engine lifecycle and Trino catalog bindings are unaffected.

## Current compliance

**Partially implemented.**

- `internal/connections/connections.go` defines `AccessModeDirect` and `AccessModeTrino`.
- Providers declare supported access modes through `Meta().AccessModes`.
- `CreateCatalogConnection` persists the selected access mode.
- `web/src/lib/connections/ConnectionWizard.svelte` creates an engine for `direct` connections, but
  through a second API request.
- `internal/engines/manage/manage.go` creates native engines, catalogs and bindings transactionally,
  but the connection is persisted separately.
- `CreateEngine` checks `RoleDirect` without validating the persisted `access_mode`.
- `catalogConnectionActions` grants `register_engine` to every writable connection.
- The Engines, Connections and connection detail pages still expose standalone engine registration.

Issue #998 must:

1. Move direct-engine creation into the backend connection-creation workflow.
2. Extend the transaction boundary to include connection persistence.
3. Reuse the existing native-engine and catalog persistence logic.
4. Remove the second `createEngine` request from the connection wizard.
5. Remove standalone direct-engine registration from the UI and public API.
6. Remove `register_engine` from `allowed_actions` and the API contract.
7. Add reconciliation for existing incomplete direct connections.
8. Add tests for successful atomic creation, rollback on failure, name conflicts, unsupported access
   modes, duplicate prevention and idempotent reconciliation.
9. Update OpenAPI, generated clients, frontend tests and documentation.

## Alternatives considered

### Keep the existing two-request creation flow

Rejected.

The frontend would remain responsible for coordinating connection and engine lifecycle, and
failures could leave incomplete resources.

### Keep standalone registration restricted to direct connections

Rejected.

A direct engine is an implementation resource created as part of its connection. Restricting the
action would preserve an unnecessary lifecycle step.

### Automatically register missing engines whenever connections are listed

Rejected.

Read operations must not silently mutate infrastructure. Existing incomplete connections are
repaired through an explicit reconciliation process.

### Create the connection first and compensate by deleting it if engine creation fails

Rejected.

Compensating deletion is not equivalent to a database transaction. Process crashes and concurrent
requests can interrupt cleanup.

The connection, engine, catalogs and bindings must commit atomically.

### Perform network discovery inside the database transaction

Rejected.

External requests may be slow or fail independently. Holding database transactions open across
network operations increases contention and failure risk.

Validation and discovery occur before persistence.

### Introduce a new engine registration abstraction

Rejected.

The existing connection, engine, catalog and binding contracts already represent the required
resources. The change concerns orchestration and transaction ownership, not the domain model.

### Combine direct and federated engine registration

Rejected.

Direct engines belong to data-source connections configured for direct execution. Federated engines
are independent query-engine resources with different lifecycle requirements.
