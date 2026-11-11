# 0025. Semantic search behind a vector store seam, embedded by Baleia

- Status: Proposed
- Date: 2026-11-11
- Issues: #940
- Extends: [0023](0023-portable-search-index.md): the vector index follows the same rule, it proposes candidates and never decides visibility.
- Related: [0020](0020-gorm-store.md) (no raw SQL), [0024](0024-cold-storage-and-retention.md) (the `PartitionManager` seam this one is shaped after).

## Context

Lexical search (#941–#943, ADR 0023) finds a table when the question shares a
word with it: a name, a word of a `snake_case` name, a singular form, a
glossary synonym, a near spelling. People adapt: they see the list and type
another word.

Agents do not. Through the MCP `search` tool an agent asks by concept
("net revenue by region") and the table is `billing_events` with a column
`net_revenue_brl`. A zero result there is worse than in the palette: the
agent gives up or writes SQL against a table it made up. The glossary does not
help, because it is curated by hand and the agent does not know it exists.

Facts that bound the decision:

- The MCP tool, the palette and the sidebar call the same federated search
  (`/workspaces/{id}/search`). A second, agent-only search would drift.
- The assistant already has a provider client (`internal/assistant/llm`):
  Anthropic or OpenAI-compatible, key sealed by the keyring, egress through
  `connections.Dialer`, an internal server for air-gapped installs.
  OpenAI-compatible servers (OpenAI, Azure OpenAI, vLLM, Ollama, LM Studio,
  TEI, LiteLLM) expose `/v1/embeddings`. Anthropic has no embedding endpoint.
- The corpus is metadata and content definitions, not customer rows: tens of
  thousands of objects. A vector of 1536 `float32` is 6 KiB; 50,000 objects are
  about 300 MB, 100 MB at 512 dimensions. Brute-force cosine over that is
  milliseconds; an ANN index is not needed at this size.
- No vector store is portable across the backends we support: `pgvector` is
  PostgreSQL only and needs SQL GORM cannot express (0020); Redis 8 query
  engine, `valkey-search`, ElastiCache, OCI Cache and Dragonfly each differ in
  module, version and license.
- Baleia already talks to OpenSearch (the optional audit `_bulk` export).

## Decision

1. **Semantic search is a product feature, not an add-on.** Any install with
   an embedding provider configured gets it, with no extra component. Without
   one, search stays lexical and nothing degrades.

2. **Baleia computes every embedding.** Embedding is one more method of the
   existing provider client (`Embed(ctx, texts) ([][]float32, error)` over
   `POST {base_url}/embeddings`; the OpenAI-compatible base URL already ends
   in `/v1`, as for `/chat/completions`), with its own settings: provider, model,
   dimensions and key, separate from the chat model (a Claude chat needs
   another embedding provider). Calls go through the keyring and the dialer.
   No provider SDK is added; Bedrock, Vertex and others are reached through an
   OpenAI-compatible gateway. A vector store never calls a provider itself.

3. **What is embedded.** One text per object, built deterministically:
   tables (qualified name, description, column names and comments), docs,
   saved queries (name, description, SQL), metrics and semantic models.
   Columns are not embedded on their own. Each row stores the provider, model,
   dimensions and a hash of its text; vectors of different models are never
   compared, and changing the model re-embeds the scope.

4. **Embedding runs in the background.** After a scope is rebuilt (0023), a
   worker activity embeds the rows whose text hash, or whose provider, model
   or dimensions, differ from the current setting, in batches, with a rate
   limit. Until a row has a current vector it is found lexically only.

5. **Vectors live behind a `VectorStore` seam.** A small interface
   (`Upsert`, `Delete`, `Nearest(scope, vector, k)`, `Capabilities`) with a
   registry, like the `PartitionManager`:
   - **`store` (default):** a GORM table `search_vectors` (scope, kind, ref,
     model, text hash, vector) with the vector as a custom GORM type stored as
     bytes on every backend; no raw SQL. Each process keeps the vectors of the
     scopes it searched in memory, bounded by a scope count, and ranks them by
     brute-force cosine. The cache is keyed like the vocabulary (0023,
     decision 4) plus a vector version: the scope's version and the time of
     its newest vector write. A background batch that lands moves the vector
     version, so no search keeps ranking an incomplete set.
   - **`opensearch` (optional):** the k-NN index of an OpenSearch cluster the
     operator runs or buys (AWS, OCI). Baleia writes the vectors it computed
     and asks for the nearest; through the dialer. For installs whose corpus or
     replica memory outgrows the default.

6. **Vectors only propose candidates.** The nearest refs are loaded from their
   live tables with the same predicates as lexical search (granted catalogs,
   live tables, visible spaces). A stale or foreign vector index can miss or
   misorder a hit; it cannot leak one.

7. **Hybrid ranking is fused in the federated search.** The lexical and the
   vector lists are merged by reciprocal rank fusion. The caller picks the
   mode: `mode=lexical|hybrid`. The editor's typeahead never embeds; the
   palette and the sidebar stay lexical while typing; the MCP `search` tool
   is hybrid by default. The query's embedding is cached by its text and the
   provider, model and dimensions, so a setting change never reuses a vector
   of another space.
   If the provider fails or times out, the search answers lexically and says
   so in the response.

8. **Sending metadata to a provider is the admin's choice.** Enabling
   embeddings states what leaves the process (names, descriptions, comments,
   doc text and saved SQL, which may carry literals, and the text of every
   uncached hybrid search, including the questions agents ask); no data rows
   are ever embedded. Air-gapped installs point at an internal server.

9. **It ships when it measures.** The golden set gains agent-style questions
   (concepts, other language, no shared word). The harness reports recall@10
   and the zero-result rate per group (existing and agent-style) and per mode
   (lexical and hybrid), each with its own baseline: hybrid must beat lexical
   on the agent-style group and must not fall below the lexical baseline on
   the existing one. An aggregate number would let one group hide the other.

## Consequences

- The agent finds tables by meaning on every backend, with no new component
  to run by default.
- Each replica holds the vectors of the scopes it searches: hundreds of MB on
  the largest catalogs at 1536 dimensions. Operators lower the dimensions or
  move to the `opensearch` adapter.
- A hybrid search costs one provider call for an uncached question; the
  agent's tool pays it, the typeahead never does.
- A model change re-embeds every scope: provider cost and a window in which
  those scopes are lexical only.
- Every new searchable kind adds its embedding text next to its lexical texts.

## Current compliance

Nothing is implemented. Order: golden set questions and baseline; provider
`Embed` and settings; `search_vectors`, the `store` adapter and the background
activity; `mode=hybrid` and the MCP default; the `opensearch` adapter last.

## Alternatives considered

- **pgvector.** PostgreSQL only, and its operators and index DDL are SQL GORM
  cannot express (0020).
- **Redis or Valkey vector modules.** Different module, version and license per
  distribution (Redis 8, `valkey-search`, ElastiCache, OCI Cache, Dragonfly);
  Redis stays a cache.
- **OpenSearch as the only store.** Simplest code, but semantic search would
  exist only where someone runs a JVM cluster; most installs and every agent
  on them would stay lexical.
- **OpenSearch computing embeddings (ML Commons connectors).** A second
  egress path to the provider, outside the keyring and the dialer.
- **Let the agent rephrase.** Cheap and worth keeping in the tool's
  description, but it is the model guessing words; it does not close the gap.
- **A Go embeddings library (`go-embeddings`).** Pulls the AWS SDK and Google
  oauth2 to wrap one HTTP call the provider client already makes.
