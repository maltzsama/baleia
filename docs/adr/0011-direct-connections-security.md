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
- **SQLite** files must resolve (after cleaning and resolving symlinks) inside `BALEIA_SQLITE_ROOT`.
  Query sessions open the file read-write and the file's own permissions decide whether a write
  succeeds (#651); metadata reads open it read-only. Without `BALEIA_SQLITE_ROOT` the provider is
  unavailable. Confinement is path-based, so its threat model is explicit: Baleia users choose a
  path and may change the database in it, but never create files (`ATTACH`, `DETACH`, `VACUUM`
  and `load_extension` are always refused, and Baleia offers no upload);
  the root and every directory under it must be writable only by operators. A component swapped
  between resolving and opening is caught by resolving again after the open, and a root writable
  by group or others is logged at startup.
- **Identity modes:** v1 uses the connection's credentials (`connection`); `per_user` comes later.
- **Baleia imposes no read-only work** (#651). What a user may read or write is decided by the
  engine and the catalog manager, or by the connection's service account. A catalog linked to a
  workspace is reachable by that workspace's members only; the link carries no read/write level. `queryengine.Capabilities.ReadOnly` (`enforced` or `none`) remains for
  Baleia's own probes, which ask for a read-only session best effort, and for MCP agents, which
  only run on engines that enforce it.
- **Connection tests (D5)** answer three different questions: `metadata` (can Baleia read
  metadata), `direct_engine` (can Baleia run SQL) and `bind:<engine>` (can the federated engine reach
  the source). A check from Baleia is never proof that a federated engine can reach a source (#12).

## Consequences

- Local databases in development need an explicit allow (`BALEIA_CONNECTIONS_ALLOW_LOOPBACK=true`).
- The CIDR lists apply to every caller, admins included. Creating and editing connections is limited to admins by default (`BALEIA_CONNECTIONS_CREATE=admins`); `everyone` opts a deployment into self-service, where the CIDR lists still bind every caller (#512, #610).
- api and worker must see the same SQLite path (shared volume).
- MySQL/MariaDB, Oracle, SQL Server and Flight SQL cannot enforce read-only sessions, so MCP agents
  cannot use them; every other caller can.

## Current compliance

Partially compliant.

- `connections.Dialer` exists and every provider dials through it, including the Iceberg REST
  HTTP client (environment proxies ignored) and the PostgreSQL (`DialFunc`) and MySQL
  (`RegisterDialContext`) drivers; `TestEveryDriverUsesDialer` proves it per provider (#11).
- Database providers: PostgreSQL (pgx `DialFunc`), MySQL and MariaDB (`RegisterDialContext`),
  SQL Server (`Connector.Dialer`), Oracle (`OracleConnector.Dialer`) and ClickHouse (HTTP through
  the dialer's client). Their metadata sessions are read-only where the engine allows it
  (PostgreSQL, MySQL, MariaDB, ClickHouse `readonly=1`) and every statement has a 30 s limit.
- Engine services: Flight SQL dials gRPC through `grpc.WithContextDialer` (TLS on by default,
  server name checked; auth none, basic or bearer token) and Trino uses the dialer's HTTP client,
  with password authentication only over HTTPS. Doris and StarRocks are declared unsupported
  (#21, #30) and never connect.
- SQLite files resolve inside `BALEIA_SQLITE_ROOT` (`TestSQLiteRootConfinement`); query sessions
  open them read-write and refuse `ATTACH`, `DETACH`, `VACUUM` and `load_extension`.
- Direct engines (sessions with read-only enforcement) are the engine issues (#81, #82, #83);
  transient tests are #12.

## Alternatives considered

- **Validate the hostname at save time only.** Rejected: DNS can change after validation.
- **Trust read-only database users.** Dropped with the read-only grants (#651): a source's own
  read-only user is simply respected by the source.
