# 0017. BI tools reach the engines only through Baleia (the BI gateway)

- Status: Proposed. Amends [0001](0001-control-plane-only.md) when accepted.
- Date: 2026-11-05
- Issues: #560 (BI files, removed in 0.6.2); design `DESIGN-catalog-scope.md` §6, §9.5

## Context

Workspace-scoped catalog access (0.6.2) makes the workspace the unit of reach: a statement may
reference only the catalogs shared with the workspace it runs in. Baleia sends that scope to Trino
as the extra credential `baleia.catalogs`, and the catalog guard denies every catalog to a user
identity that arrives without it.

That rule only holds if **Trino has one client: Baleia**. Ingestion engines (Spark, Flink) reach
catalogs through the catalog manager (Polaris, …), never through Trino; nothing else should reach
Trino at all.

The BI files of #560 broke that rule. `GET /engines/{id}/bi/{tool}` produced a Tableau `.tds` or
Power BI `.pbids` file pointing the tool at the engine's `external_url`, so the tool talked to the
engine directly, outside any workspace, with an identity Baleia never saw. Under 0.6.2 the guard
gives such an identity no catalog, and the endpoint is removed. BI users therefore have no path
until Baleia serves BI itself.

[0001](0001-control-plane-only.md) says Baleia implements no JDBC, ODBC or Flight SQL server and
that external clients talk to the engines directly. That second clause is what #560 followed, and
it is what this ADR reverses.

Facts the design must respect:

- Tableau and Power BI connect to Trino through their Trino connectors, which speak Trino's HTTP
  client protocol: `POST /v1/statement`, then `GET` on each `nextUri` until the result ends,
  `DELETE` to cancel. HTTP Basic authentication over TLS is supported by both.
- Trino honours `X-Trino-User` from Baleia's authenticated admin login only (ADR 0005, #646,
  #647). The bundled Trino is not reachable from outside the cluster (#645).
- Baleia API keys (`api_keys`) belong to a user (or a service account, ADR 0016) and carry no
  workspace.
- API replicas are stateless and behind a load balancer: consecutive `nextUri` requests of one
  query can land on different replicas.
- Trino's spooling protocol returns result segments as URIs to object storage or to the
  coordinator, which a client would fetch without Baleia.

## Decision

### Invariant

- **Trino's only client is Baleia.** Engines are never published to BI tools or other external
  SQL clients. `engines.external_url` stops being a client-facing address.
- External SQL clients reach Trino through the **BI gateway**, a Trino-protocol endpoint served by
  Baleia that applies the same workspace scope as the editor.

### The gateway

- A **separate listener** in the API process (`BALEIA_BI_GATEWAY_ADDR`, off by default), exposed
  through its own Service/Ingress in the chart. Trino clients expect the protocol at the server
  root, so it cannot live under `/api/v1`.
- It serves only `POST /v1/statement`, `GET`/`DELETE` on the URIs it issued, and `GET /v1/info`.
  Every other path is 404.
- **Trino engines only.** Direct engines (PostgreSQL, MySQL, SQL Server, …) are not served: that
  would need each database's wire protocol. A BI tool connecting to a user's own database with
  that database's own credentials is outside Baleia, and Baleia no longer hands out files for it.

### Credential: a workspace-bound BI key

- A new key kind: an API key with `workspace_id` and `engine_id`, created by a workspace member
  for themselves, or by a workspace admin for a service account of that workspace. It is shown
  once, revocable, expirable, and audited like other keys.
- The BI tool signs in with HTTP Basic: user = the key owner's e-mail, password = the key. The
  gateway checks the key hash, that the user matches, that the key is not expired, and on **every
  request** that the owner is still a member of the workspace and the engine still hosts a binding
  in the workspace's scope.
- The workspace and engine come from the key, never from the client. One key, one workspace, one
  engine: the BI tool sees exactly the catalogs that workspace has on that engine.
- BI is **read-only**: every gateway session carries `baleia.mode=readonly`, whatever the
  workspace role.

### What the gateway sends to Trino

- Authenticated as Baleia's admin login, with `X-Trino-User` = the key owner's Trino identity
  (`trinoidentity.For`), as for editor runs.
- `X-Trino-Extra-Credential`: `baleia.catalogs` = `engine_scope(workspace, engine)` resolved per
  request, `baleia.mode=readonly`, and the per-user catalog token when the owner has one
  (ADR 0015). Values percent-encoded.
- Client tags `baleia`, `bi`, and the workload class from the workload rules
  ([0014](0014-workload-management.md)), so Trino's resource groups apply. Baleia's own admission
  queue does not hold gateway queries in this version.
- **Request headers are an allow-list**: catalog, schema, time zone, language, source, client tags
  (appended to Baleia's), client info, client capabilities, session properties, prepared
  statements, trace token. Everything else is dropped, in particular `X-Trino-User`,
  `X-Trino-Original-User`, `X-Trino-Role`, `X-Trino-Original-Role`,
  `X-Trino-Extra-Credential`, `X-Trino-Transaction-Id` and `X-Trino-Query-Data-Encoding`
  (no spooling: results always come back inline through the gateway).
- **Response headers**: `X-Trino-Set-Authorization-User` and `X-Trino-Reset-Authorization-User`
  are dropped; the others pass through.

### Following a query across replicas

- Every URI in a response (`nextUri`, `infoUri`, `partialCancelUri`) is rewritten to the gateway's
  public address, with an HMAC over (Trino query id, the original path, key id, expiry), signed
  with a server secret shared by the replicas.
- A request on a rewritten URI is served only when the signature is valid and the key id in it is
  the key that authenticated the request. Another key, even of the same user, gets 404. No
  per-query state is kept in Baleia.
- `infoUri` points at the gateway and returns 404: the Trino web UI is never exposed.

### Data path limits (this is where 0001 is amended)

- The gateway streams pages; it stores nothing. It holds at most one page per request in memory
  to rewrite the URIs. Trino 484 caps a page at 1 MiB server-side (`TARGET_RESULT_SIZE`); there is
  no client-side page-size request, so the gateway accepts that cap and never asks for a different
  one. Results do not go through Temporal or object storage.
- Per-key and per-workspace limits on concurrent queries; a new statement over the limit gets
  HTTP 429.
- Each statement is recorded as an execution of origin `bi` (SQL, user, workspace, engine, Trino
  query id, status, timings, no results), so history, audit and monitoring see it.
- A workspace admin can turn BI off for the workspace; a global admin can turn the gateway off
  entirely. Both revoke at the next request.

## Consequences

- 0001 changes from "external clients talk to the engines directly" to "external SQL clients
  talk to the BI gateway". The other clauses of 0001 stand: no JDBC/ODBC/Flight SQL
  implementation (the gateway relays Trino's own protocol, it does not implement a driver), no
  cross-engine joins, no result data in Temporal.
- Baleia becomes a data path for BI traffic, with its own capacity planning: gateway replicas
  scale with BI concurrency, and the page cap bounds memory per request.
- BI access is scoped and audited like the editor, and can be revoked per key, per member, per
  workspace or globally.
- The BI file returns, pointing at the gateway: host and port of the gateway, the catalog and
  schema; no credentials.
- BI tools cannot write, and cannot see catalogs outside the key's workspace and engine. A user
  who needs another workspace needs another key.
- A BI tool that only supports OAuth2 against the engine is not served until the gateway supports
  Baleia's identity providers (follow-up).

## Current compliance

Not implemented. In 0.6.2 the BI file endpoint is removed and the guard denies any identity that
does not come through Baleia, so the invariant holds with no BI path at all. This ADR is
implemented by: workspace-bound BI keys, the gateway listener and proxy, URI signing, execution
records of origin `bi`, chart Service/Ingress, the BI file pointing at the gateway, and an e2e
test with the Trino JDBC driver (Tableau and Power BI both use the Trino protocol, so the JDBC
driver is the reference client).

To verify while implementing: which `/v1/*` endpoints the Tableau and Power BI connectors call
beyond `/v1/statement` and `/v1/info`; that both send Basic credentials on every request; and
that a client with spooling support falls back to the inline protocol when the encoding header is
removed.

## Alternatives considered

- **Expose Trino to BI tools with a Trino-side authenticator** (a `PasswordAuthenticator` in the
  plugin that validates Baleia keys, and a guard that resolves the scope from Baleia's database).
  Rejected: Trino becomes reachable from outside, which breaks the invariant and widens its
  attack surface (#645); every access check would need a database lookup; and Baleia would see
  BI statements only from Trino's logs.
- **Flight SQL or a PostgreSQL wire-protocol server in Baleia.** Rejected: protocol translation
  from Trino to another wire format is a driver implementation (what 0001 rejects), and Tableau
  and Power BI already speak Trino's protocol.
- **Extracts only** (BI tools import results Baleia already stored). Kept as a complement, not a
  replacement: no live or DirectQuery connections.
- **Workspace chosen by the client** (session property or username suffix). Rejected: the
  workspace would be a claim from the client instead of a property of the credential, and every
  connector exposes these settings differently.
- **Spooled results through the gateway** (rewriting segment URIs). Rejected for now: segments can
  point at object storage directly; inline results keep every byte on the path Baleia controls.
