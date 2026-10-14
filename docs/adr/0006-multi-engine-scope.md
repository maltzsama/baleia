# 0006. Multi-engine scope

- Status: Accepted (owner decision D1, revised)
- Date: 2026-10-14
- Issues: #9, #14, #20, #21, #30, #81, #82, #83

## Context

Baleia started as a SQL editor for Trino, but it has always been meant to query other engines
directly, not only through Trino federation. Contracts designed around Trino alone would have to be
broken later.

## Decision

- Baleia is **multi-engine**.
- **v1 implements** Trino (federated) and the direct engines PostgreSQL, MySQL/MariaDB, SQL Server,
  Oracle, SQLite, ClickHouse and Arrow Flight SQL.
- **Later:** Doris and StarRocks (federated). The contracts (#20, #14, #80) are validated now
  against their documented behaviour: MySQL wire protocol, connection-scoped sessions,
  `CREATE CATALOG … PROPERTIES`, `KILL QUERY`, no impersonation.
- Baleia never federates queries itself ([0001](0001-control-plane-only.md)).
- **Not supported:** generic JDBC (needs a JVM; reachable later through Doris/StarRocks `jdbc`
  catalogs) and DuckDB (needs cgo; the image builds with `CGO_ENABLED=0`). Every driver must be
  pure Go.

## Consequences

- The engine contract is Arrow-first, session-based and declares capabilities (identity,
  read-only, cancel) per kind.
- The UI selects a target (engine, catalog, schema) rather than assuming Trino.
- Each new driver must accept a custom dialer ([0011](0011-direct-connections-security.md)).

## Current compliance

Not compliant yet: only Trino exists, as a concrete client (#20, #25). Direct engines are #81, #82
and #83; Doris and StarRocks are #21 and #30.

## Alternatives considered

- **Trino only, other sources through Trino connectors.** Rejected (original D1): sources without a
  Trino connector (SQLite, Flight SQL) would be unreachable, and every direct query would pay a Trino
  hop.
- **Generic JDBC through a sidecar JVM.** Rejected: operational weight; Doris/StarRocks cover it.
