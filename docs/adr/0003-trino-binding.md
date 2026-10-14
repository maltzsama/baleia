# 0003. How a catalog reaches Trino (the Trino binder)

- Status: Accepted (owner decisions D3, D4)
- Date: 2026-10-14
- Issues: #9, #31, #37, #38, #39, #80

## Context

Trino loads catalogs through the `baleia-trino-catalog-store` plugin, which reads
`trino_catalog_registry` in Baleia's Postgres. The plugin must be able to boot Trino without Baleia
running, so the registry holds complete, usable configuration. Trino also has catalogs that Baleia
did not create (from files, or created by an operator), and Baleia must not destroy them.

## Decision

The Trino binder is one implementation of `bindings.Binder` ([0002](0002-connections-engines-catalogs-bindings.md)).

- **Registry row = desired configuration with real values.** Rows are protected by the dedicated
  `baleia_trino` database role. (D4, Phase A by default: secrets referenced as
  `@baleia-secret[env:…]` or `@baleia-secret[file:…]` are stored as references, never resolved
  into the database; catalog-origin secrets are stored resolved. Phase B only on owner request; see
  #31.)
- **DDL text carries placeholders only.** `CREATE CATALOG` statements sent to Trino contain
  `@baleia-secret[...]` placeholders, never secret values; the plugin resolves them.
- **The plugin persists on `CREATE CATALOG` / `DROP CATALOG` and loads at boot.** There is no
  polling.
- **`catalog_version` is SHA-256 over the resolved values**, byte-compatible with Trino's
  `FileCatalogStore.computeCatalogVersion`. The algorithm is pinned by shared golden vectors
  (`catalog_version_vectors.json`) tested in both repositories (#39).
- **Provenance (D3, managed-only authority).** A binding is `managed` (Baleia wrote it) or
  `adopted` (it exists in Trino, Baleia did not create it). Baleia creates, updates and drops only
  `managed` catalogs. `adopted` catalogs are reported and never mutated or dropped, including by
  reconciliation (#37).
- **State.** Desired (registry/binding), effective (what the engine loaded) and observed (last
  check) are separate, and lifecycle procedures are idempotent (#38).
- **Changing the contract.** A change to the registry schema, the version algorithm, reserved names
  or the placeholder syntax is released in the plugin first, then the pin in Baleia is bumped (#39).

## Consequences

- Trino boots with its catalogs even when Baleia is down.
- The registry is a credential store and must be treated as one (role separation, no logging).
- Reconciliation can report drift without risking an operator's catalogs.

## Current compliance

Partially compliant.

- Registry, placeholders and the version algorithm exist (`db/migrations/00018_trino_catalog_store.sql`,
  `internal/trino/secret.go`, `internal/catalogs/version.go`).
- Provenance is implicit in `trino_catalog_registry.updated_by IN ('baleia','trino')`; there is no
  `managed`/`adopted` binding mode yet (#13, #80).
- Desired, effective and observed state are not separated (#38).
- Golden vectors are copied into `internal/catalogs/testdata/` rather than fetched from the plugin's
  contract fixtures (#39).
- `SecretPlaceholder` emits only the catalog-origin form; env/file references are #31.

## Alternatives considered

- **Registry with placeholders only, secrets fetched from Baleia at load.** Rejected: Trino could
  not boot without Baleia.
- **Plugin polls the registry.** Rejected: DDL already notifies the plugin; polling adds load and
  a window of inconsistency.
- **Baleia owns every Trino catalog.** Rejected: it would drop catalogs operators created outside
  Baleia.
