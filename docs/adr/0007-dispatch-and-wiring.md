# 0007. Adapters are constructed only at the wiring sites

- Status: Accepted
- Date: 2026-10-14
- Issues: #9, #20

## Context

Handlers and activities construct the Trino client themselves. That couples request handling to a
concrete engine, hides configuration in constructors spread over the code, and makes it impossible
to substitute a fake in tests or add a second engine without editing those call sites.

## Decision

- Engine adapters, connection providers, binders and external database clients are constructed
  only at the two wiring sites, `internal/server/server.go` (API) and `cmd/worker/main.go` (worker),
  and injected as interfaces — the same pattern as `db.Querier`.
- Kind registries (`queryengine.RegisterKind`, the provider registry, the binder registry) are
  consumed only at the wiring sites and in the packages that implement the kinds.
- Baleia's own database (`pgxpool.New`, `sql.Open` for migrations) is opened only in `cmd/*` and
  `internal/migrate`.
- `internal/arch_test.go` (`TestAdapterConstructionOnlyInWiring`) enforces this by parsing the
  module and failing when a guarded constructor appears outside its allowed files.

## Consequences

- Handlers and activities are testable with fakes.
- Adding an engine kind does not touch handlers or activities.
- The test's allow-list for existing offenders must only shrink; an entry that no longer offends
  fails the test so it gets removed.

## Current compliance

Compliant. `trino.New`/`NewWithHTTPClient` are called only at the wiring sites and in the Trino
adapter and binder packages; `queryengine.Manager` builds engine instances from `engines` rows
through registered kind factories with the shared dialer; driver packages are imported only under
`internal/queryengine/<kind>` and `internal/connections/<kind>`. The architecture tests
(`TestAdapterConstructionOnlyInWiring`, `TestNoDriversOutsideAdapters`,
`TestNoTrinoImportsInDomain`) have no exceptions left.

## Alternatives considered

- **A DI framework.** Rejected: two wiring sites do not justify it.
- **Convention without a test.** Rejected: agents and new contributors re-introduce constructors.
