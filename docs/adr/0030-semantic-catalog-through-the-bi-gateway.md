# 0030. External catalogs read the semantic catalog through the BI gateway

- Status: Accepted. Extends [0017](0017-bi-gateway.md).
- Date: 2026-11-23
- Issues: #1124, #1133

## Context

External data catalogs (OpenMetadata first, DataHub later) already discover Baleia's physical
metadata through the BI gateway: OpenMetadata's stock Trino connector signs in with a workspace BI
key and introspects the catalogs in the workspace scope (#1122, #1123).

Semantic models and metrics are not Trino objects, so that connector cannot see them. OpenLineage
cannot carry them either: OpenMetadata turns OpenLineage jobs and datasets into pipelines and
tables, never into the data models and metrics a semantic layer is made of (#1132). The
supported way for OpenMetadata to discover a semantic layer is an ingestion connector that pulls
it from the source. For Baleia, that connector is
[`baleia-openmetadata`](https://github.com/maltzsama/baleia-openmetadata) (#1134). It needs a
read-only description of the workspace's semantic catalog.

Facts the design must respect:

- The catalog discovers Baleia, not the other way around. Baleia stores no catalog credentials and
  makes no outbound call to a catalog.
- [0017](0017-bi-gateway.md) makes the BI key a gateway credential: it is valid only at the BI
  gateway listener, never on `/api/v1` (`authenticateAPIKey` rejects it).
- The physical tables a semantic item reads must be named exactly as the catalog's Trino connector
  sees them through the gateway: the **engine's** catalog names, not Baleia's catalog names.

## Decision

The BI gateway listener serves the workspace semantic catalog, read-only, at three routes under a
prefix that cannot collide with Trino's `/v1/` protocol paths:

| Route | Response |
|---|---|
| `GET /baleia/v1/semantic` | Contract version, workspace, model and metric counts |
| `GET /baleia/v1/semantic/models?after=&limit=` | Semantic models, by slug |
| `GET /baleia/v1/semantic/metrics?after=&limit=` | Standalone metrics, by slug |

- **Credential and switches.** Requests authenticate exactly like a statement: HTTP Basic with the
  key owner's e-mail and the BI key, then the global and workspace BI switches. The key's workspace,
  owner and engine define the response; the client never chooses them.
- **Content.** Only models and metrics in spaces the key's owner can read. Saved queries are
  workbench content and are not exposed.
- **Physical names.** Source tables are listed as `catalog.schema.table` in the key engine's names,
  and only when the table exists in the workspace's metadata, is granted to the workspace and its
  catalog is bound to the key's engine. Content on other catalogs is still listed, without those
  tables.
- **SQL.** A standalone metric's statement is rendered and a model metric's statement is compiled
  for the key engine's dialect with the same code the editor and Explore use. A statement that
  cannot be rendered is omitted, not guessed.
- **Contract.** The JSON contract lives in `internal/semanticcatalog` and carries a
  `contract_version` that changes only on an incompatible change. Pages are cursor-based (`after` is
  the last slug of the previous page; `limit` defaults to 100, at most 500). One snapshot holds at
  most 10,000 items.
- **Audit.** Each successful read is audited as `bi_gateway.semantic_read` with the key id, never
  the key. Reads are not query executions and do not appear in query history.

## Consequences

- One credential per workspace serves both OpenMetadata connectors (Trino and Baleia), on one
  origin and one TLS setup. Revoking the key or turning BI off stops both.
- The gateway now serves Baleia data besides relaying Trino. The routes stay read-only, are
  versioned independently of `/api/v1`, and are not part of `api/swagger.yaml`.
- The catalog's view is the key owner's view: content in a space the owner cannot read is invisible
  to the catalog. A workspace that wants the whole catalog exposed issues the key from an account
  that can read every space.
- A future DataHub connector reads the same routes.

## Current compliance

The routes, the reader and their tests ship with #1133. The end-to-end check against OpenMetadata
2.0 and 2.1 is #1135 (API) and #1137 (connector).

## Alternatives considered

- **A route under `/api/v1` authenticated by the BI key.** It would put the contract in
  `api/swagger.yaml`, but it would make the BI key an API credential, which
  [0017](0017-bi-gateway.md) rules out, and the connector would need a second origin.
- **A user API key on `/api/v1`.** A full-scope user credential for a read-only, single-workspace
  integration, with no BI switch to turn it off.
- **OpenLineage events pushed by Baleia.** Rejected in #1132: OpenLineage cannot represent data
  models and metrics in OpenMetadata, and pushing would make Baleia hold catalog credentials.
- **Expose models and metrics as views through the gateway.** The Trino connector would ingest them,
  but as tables or views, not as data models and metrics, and they would duplicate the physical
  catalog.
