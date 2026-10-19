# MCP server

Baleia exposes a **read-only** Model Context Protocol server at `POST /mcp` so
external agents (Claude Desktop/Code, IDE assistants, automations) can discover
governed metadata and run read-only queries with the rights of a dedicated agent
identity (#58).

The server is mounted only when `BALEIA_AGENTS_ENABLED=true`. It is stateless
streamable HTTP: every request is authenticated on its own, with no session
affinity across replicas.

## Authentication

Every request needs an agent token minted in the UI (`/settings/agents`):

```json
{
  "mcpServers": {
    "baleia": {
      "type": "http",
      "url": "https://baleia.example.com/mcp",
      "headers": { "Authorization": "Bearer bal_agt_…" }
    }
  }
}
```

- User JWTs, API keys and cookies are **not** accepted on `/mcp`; agent tokens
  are not accepted on any other route.
- The agent acts for its owner with the owner's **member** rights intersected
  with the workspaces it is bound to. It is always read-only.
- `BALEIA_MCP_ALLOWED_ORIGINS` is a comma-separated allow-list for the `Origin`
  header. Empty (the default) rejects any request that carries one; native MCP
  clients do not send it.

## Tools

| Tool | Purpose |
|---|---|
| `list_workspaces` | Workspaces this agent is bound to |
| `list_catalogs` | Catalogs granted to a bound workspace, with their engines |
| `list_schemas` | Namespaces of a catalog |
| `list_tables` | Tables of a schema |
| `describe_table` | Columns, types, docs and freshness |
| `search_metadata` | Search tables, columns and metrics |
| `list_metrics` | Semantic metrics of a workspace |
| `render_metric` | Render a metric's SQL for a binding |
| `run_query` | Run **one** read-only statement and wait for it |
| `get_execution` | State of an execution this agent created |
| `get_results` | Stored rows of a finished execution, with caps |
| `cancel_execution` | Cancel an execution this agent created |

Results are read from the stored Arrow object, never by re-running the query.
Integers beyond 2^53 and decimals are returned as strings; timestamps are
ISO-8601; binary values are base64.

## Policy and limits

Every call goes through `internal/agentpolicy` (#60): tool allow-list, bound
workspaces, allowed catalogs/engines, a per-minute burst limit, one-statement
and read-only checks, engine read-only enforcement, result caps and durable
concurrency/hourly quotas. A refusal is returned as:

```json
{ "error": { "code": "refused", "reason": "quota", "detail": "…", "retry_after_seconds": 30 } }
```

`reason` is one of `policy`, `scope`, `rate`, `quota`, `budget`, `statement`.

## How it is built

`internal/mcp` calls the same strict-server handlers the UI uses, in-process, so
authorization and audit run unchanged. It imports none of `internal/db`,
`internal/trino`, `internal/queryengine`, `internal/temporal`,
`internal/storage` or `internal/catalogmanager`; `TestImportBoundary` enforces
that.
