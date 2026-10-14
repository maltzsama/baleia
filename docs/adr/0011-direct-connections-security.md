# 0011. Security of direct connections to user-supplied hosts

- Status: Accepted (owner decisions D5, D11)
- Date: 2026-10-14
- Issues: #9, #11, #12, #16, #20

## Context

With direct engines and metadata readers ([0002](0002-connections-engines-catalogs-bindings.md)),
the API and worker open network connections to hosts that users type in. Without a guard, a user can
make Baleia reach loopback services, cloud metadata endpoints (`169.254.169.254`) or internal
networks (SSRF). SQLite adds file paths, and engines differ in whether they can enforce read-only
access.

## Decision

- **One dialer for every provider and engine** (`connections.Dialer`). It resolves the host, rejects
  denied addresses and dials the **resolved IP** (no second lookup, no DNS-rebinding window).
  Defaults deny loopback, link-local (including cloud metadata) and `0.0.0.0/8`; operators configure
  extra deny or allow CIDRs. A driver without a dial hook is not shipped.
- **SQLite** files must resolve (after cleaning and resolving symlinks) inside `BALEIA_SQLITE_ROOT`,
  and are opened read-only unless both the connection and the server allow writes. Without
  `BALEIA_SQLITE_ROOT` the provider is unavailable.
- **Identity modes:** v1 uses the connection's credentials (`connection`); `per_user` comes later.
- **Read-only enforcement is per engine**, declared in `queryengine.Capabilities.ReadOnly`
  (`enforced` or `none`). A `read` grant runs in a read-only session; on an engine that cannot
  enforce it, granting `read` requires write access or an explicit assertion that the credentials
  are read-only (#13).
- **Connection tests (D5)** answer three different questions: `metadata` (can Baleia read
  metadata), `direct_engine` (can Baleia run SQL) and `bind:<engine>` (can the federated engine reach
  the source). A check from Baleia is never proof that a federated engine can reach a source (#12).

## Consequences

- Local databases in development need an explicit allow (`BALEIA_CONNECTIONS_ALLOW_LOOPBACK=true`).
- api and worker must see the same SQLite path (shared volume).
- MySQL/MariaDB, Oracle, SQL Server and Flight SQL cannot enforce read-only; grants on them are
  explicit about it.

## Current compliance

Not compliant yet: no direct connections exist. The dialer and providers are #11; read-only
capabilities and sessions are #20; transient tests are #12.

## Alternatives considered

- **Validate the hostname at save time only.** Rejected: DNS can change after validation.
- **Trust read-only database users.** Kept only as an explicit, per-grant assertion.
