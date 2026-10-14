# 0001. Baleia is a control plane, never a data plane

- Status: Accepted
- Date: 2026-10-14
- Issues: #9, #89

## Context

Baleia manages connections, catalogs and engines, and runs user SQL on those engines. It would be
easy to grow it into a query proxy (a JDBC or Flight SQL server in front of the engines) or a
federation layer that joins data across engines. Either would make Baleia responsible for
throughput, memory and correctness of data movement that the engines already handle.

Before #89, result rows travelled through Temporal payloads, which put data on the control channel
and hit payload size limits.

## Decision

- Baleia is a **control plane**. Query engines (Trino now, other engines per
  [0006](0006-multi-engine-scope.md)) are the **data plane**.
- Baleia does not proxy results beyond streaming results it has already stored (per-statement Arrow
  results in object storage).
- Baleia does not implement a JDBC, ODBC or Flight SQL server. External clients talk to the engines
  directly.
- Baleia never joins data across engines. Federation is the job of a federated engine.
- Temporal carries control (ids, statuses, handles, small summaries), never result data.

## Consequences

- Result size is bounded by object storage, not by Temporal or API memory.
- Users who need a programmatic SQL endpoint use the engine's own endpoint and its access control.
- A cross-engine query requires binding both catalogs into one federated engine.

## Current compliance

Compliant since #89: statement results are stored as Arrow in object storage
(`internal/arrowipc`, `internal/storage`) and workflows pass only references.

## Alternatives considered

- **Flight SQL / JDBC server in Baleia.** Rejected: duplicates the engines' endpoints and makes
  Baleia a data path with its own scaling and security surface.
- **Cross-engine joins in Baleia.** Rejected: that is a query engine; Trino, Doris and StarRocks
  already do it.
