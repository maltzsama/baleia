# 0005. Trino security model

- Status: Accepted (owner decision D8)
- Date: 2026-10-14
- Issues: #9, #92, #147

## Context

Before #92, every query ran as one Trino user and catalog DDL was blocked only by a prefix check in
Go. Any SQL that slipped past the check (for example through `EXECUTE IMMEDIATE`) could create or
drop catalogs, and Trino could not tell Baleia users apart.

The README states that access control lives in the catalog manager (Polaris). A spike on #9 tested
whether Polaris can see each Baleia user: setting `iceberg.rest-catalog.session=USER` made Trino
open one REST session per user, Polaris rejected the token requests (400) and table reads failed.
Per-user identity towards Polaris needs an OAuth2 token exchange with an IdP that Polaris trusts,
which is separate work.

## Decision

- **Catalog DDL is authorized inside Trino** by the `baleia-catalog-guard` system access control
  shipped with the plugin. Only identities in `baleia.admin-users` may create or drop catalogs or
  impersonate an admin.
- **Separate identities.** An admin identity (`BALEIA_TRINO_ADMIN_USER`) runs catalog DDL,
  reconciliation and maintenance and never runs user SQL. User SQL runs as a **per-user** Trino
  identity sent in `X-Trino-User` (D8). A service identity is the fallback for jobs without a user.
- **Read-only mode** is enforced by the guard: a session with the extra credential
  `baleia.mode=readonly` is denied every write and DDL check.
- The Go check on SQL text (`policy.ForbiddenInEditor`, splitting with the target's dialect) is UX
  and defense in depth only, never the security boundary. It is a policy on users and never reads
  engine capabilities (`TestGateNotCapabilityDriven`).
- **Trino is private.** The coordinator is not published by default. Because Baleia's Trino has no
  authentication, the guard's `baleia.allow-unauthenticated-admin` flag is enabled only where Trino
  is reachable from the API and worker alone (compose, minikube dev values); the Helm chart defaults
  it to `false` (#147).
- **Polaris sees one service principal per connection.** Per-user authorization is enforced in
  Trino (guard, read-only mode) and in Baleia (workspace rules, #16), not in Polaris.

## Consequences

- `system.runtime.queries.user` shows the real Baleia user.
- A user cannot manage catalogs even with crafted SQL.
- Exposing Trino to other clients requires real Trino authentication first; until then the admin
  identity can be claimed by anyone who reaches the coordinator.
- Per-user identity towards Polaris is future work (token exchange), not a property switch.

## Current compliance

Partially compliant.

- Guard, admin identity and per-user identities are in place (#92).
- Baleia does not send `baleia.mode=readonly` yet; mapping `read` grants to read-only sessions
  arrives with the engine contract's read-only capability (#20, [0011](0011-direct-connections-security.md)).
- The API has no real Trino credential, so the unauthenticated-admin flag is still needed (#147
  follow-up).
- The README's claim that access control lives in Polaris holds only per service principal.

## Alternatives considered

- **Go-side SQL filtering only.** Rejected: bypassable, and Trino would still trust any client.
- **One Trino user for everything.** Rejected: no per-user attribution or enforcement.
- **`iceberg.rest-catalog.session=USER` towards Polaris.** Rejected for now: does not work without
  token exchange (spike on #9).
