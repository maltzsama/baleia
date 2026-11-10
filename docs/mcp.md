---
description: "Connect Claude Desktop, IDE assistants and your own agents to Baleia's MCP server to discover governed metadata and run SQL with their owner's role."
---

# MCP server

Baleia exposes a Model Context Protocol server at `POST /mcp` so
external agents (Claude Desktop/Code, IDE assistants, automations) can discover
governed metadata and run queries with the rights of a dedicated agent
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
  with the workspaces it is bound to. It follows the owner's workspace role, like a
  person (#653): in a workspace where the owner is a **viewer** the agent only reads
  (read-only session, read statements only); where the owner is an **editor** or
  **admin** it may also write, for example create tables or load data, and the
  catalog manager decides what it may write where. Catalog management and session
  authorization stay refused for every agent. The built-in assistant stays read-only.
- An agent can be linked to a [service account](guides/administration.md#access)
  instead of a person (#653): it then acts with that account's workspace roles,
  and whoever manages the account manages the agent.
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
| `search_metadata` | Search tables and columns by name or comment |
| `search` | One ranked search over tables, columns, docs, saved queries, metrics, models and folders, with a count per kind |
| `list_metrics` | Semantic metrics of a workspace |
| `render_metric` | Render a metric's SQL for a binding |
| `list_models` | Semantic models visible in a workspace |
| `describe_model` | A model's source, dimensions, metrics and joins |
| `query_semantic` | Compile an explore query over a model to SQL; run it with `run_query` |
| `run_query` | Run **one** statement and wait for it; read-only for a viewer agent, read or write for an editor or admin agent |
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

With [workload management](guides/workload.md) on, a run over the agent's
concurrency limit is not refused: it waits in its engine's queue for its
class (`background`, unless a rule or cost routing assigns another), with the
agent as its fairness key. The hourly quota still refuses.

## How it is built

`internal/mcp` calls the same strict-server handlers the UI uses, in-process, so
authorization and audit run unchanged. It imports none of `internal/db`,
`internal/trino`, `internal/queryengine`, `internal/temporal`,
`internal/storage` or `internal/catalogmanager`; `TestImportBoundary` enforces
that.
