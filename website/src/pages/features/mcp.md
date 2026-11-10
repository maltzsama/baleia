---
title: "MCP server for your data catalog and SQL engines"
description: "Let Claude Desktop, IDE assistants and your own agents discover governed metadata and run SQL through Baleia's MCP server, with their owner's role."
---

# MCP server for your data catalog and SQL engines

Baleia exposes a Model Context Protocol server at `POST /mcp`, so external agents (Claude Desktop and Claude Code, IDE assistants, automations) can discover your governed metadata and run queries with the rights of a dedicated agent identity. It is off by default and mounted only when the operator enables agents.

## Governed by the same roles as people

- **Agent tokens.** Every request is authenticated with an agent token minted in the UI. User JWTs, API keys and cookies are not accepted on `/mcp`, and agent tokens are not accepted anywhere else.
- **The owner's role applies.** In a workspace where the owner is a viewer, the agent only reads. Where the owner is an editor or admin, it may also write, and the catalog manager decides what it may write where. Catalog management stays refused for every agent.
- **Service accounts.** An agent can be linked to a service account instead of a person.

## What an agent can do

`list_workspaces`, `list_catalogs`, `list_schemas`, `list_tables`, `describe_table` and `search_metadata` to discover data; `list_models`, `describe_model`, `query_semantic`, `list_metrics` and `render_metric` for the [semantic layer](/features/semantic-layer); and `run_query`, `get_execution`, `get_results` and `cancel_execution` to run one statement and read its stored results, with caps.

## Limits that hold

Every call passes through a policy: a tool allow-list, the workspaces the agent is bound to, allowed catalogs and engines, a per-minute burst limit, one-statement and read-only checks, result caps and hourly and concurrency quotas. A refusal comes back with a reason (`policy`, `scope`, `rate`, `quota`, `budget` or `statement`) so the agent can react. The server calls the same handlers the UI uses, so authorization and audit run unchanged.

## Try it

- [Read the MCP server reference](/docs/mcp)
- [Read about the optional built-in assistant](/docs/assistant)
- [Run Baleia locally in a few minutes](/docs/quick-start)
