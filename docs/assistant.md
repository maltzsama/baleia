# Embedded assistant

The assistant answers data questions from the workbench. It is **off by default**
and configured by an administrator. It never touches a database or engine
directly: the model proposes tool calls, and Baleia executes them through the
same read-only MCP tools external agents use (`/mcp`), so the capability policy
and the audit trail apply unchanged.

## Enabling it

1. Open `/settings/assistant` (global administrators).
2. Choose a provider:
   - **Anthropic** — the Messages API.
   - **OpenAI-compatible** — OpenAI, Azure OpenAI with a compatible endpoint,
     vLLM, Ollama or LM Studio. `base_url` is required.
3. Set the model and the API key. The key is sealed with the keyring
   (`BALEIA_ENCRYPTION_KEY`, AAD `assistant:api_key`); it is write-only and is
   never returned by the API or written to logs.
4. Pick the data-sharing level and the limits.
5. Use **Test connection** to send a one-token prompt.

The API process needs egress to the provider's `base_url`. Air-gapped
deployments point it at an internal OpenAI-compatible server.

## Data leaving the instance

| Level | What the model sees |
|---|---|
| `metadata_only` (default) | schemas, table and column names, docs, metric definitions, row counts, truncation and error codes — **not** result rows |
| `metadata_and_samples` | the same, plus a bounded sample of result rows (the workspace's own data) |

The user always sees full results in the UI. Switching the level is an explicit
administrator action and is audited (`assistant.settings_update`).

The following never leave the instance: connection URLs and credentials, other
users' data, the provider API key, and the raw upstream error text (errors are
sanitized before they reach the model).

## Turns and budgets

- A turn runs in the API process, bounded by `max_turn_seconds`,
  `max_tool_calls_per_turn` and `max_output_tokens_per_turn`.
- The instance also enforces a **daily token budget per user**
  (`daily_token_budget_per_user`). When it is exhausted the turn persists a
  `budget_exhausted` notice instead of calling the provider.
- Tool calls run as an automatically provisioned `kind='assistant'` agent bound
  to the conversation's workspace, under the owner's rights downgraded to
  read-only. Refusals are shown to the model and recorded in the audit trail.
- Conversations are private to their owner; administrators see the audit trail,
  not the chat.

## Persistence and recovery

Conversations and messages are stored in `assistant_conversations` and
`assistant_messages`. History survives a restart. A turn that was running when a
replica stopped is marked `interrupted` by the sweeper (60 s heartbeat) and the
user can retry it; the in-flight provider call is not resumed.
