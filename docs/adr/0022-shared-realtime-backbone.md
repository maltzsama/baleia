# 0022. Redis is the shared real-time, cache and rate-limit backbone

- Status: Accepted
- Date: 2026-11-10
- Issues: #755, #763, #764

## Context

Baleia must run its API as multiple replicas. Today every "real-time" feature is
either in-process or built on database polling, so a client connected to one
replica never sees events produced on another, and per-process state is
silently partitioned:

- **Execution WebSocket** (`internal/ws/handler.go`): a `pollTicker` per
  connection reads Postgres once a second for the execution and its
  statements.
- **Assistant SSE** (`internal/assistant/turn.go`): an in-process `Hub` fans
  turn events to subscribers on the same process; deltas are best-effort and
  local. Persisted messages are replayed from the database, which is already
  cross-replica.
- **Monitoring SSE** (`internal/monitoring/events`): the cross-process hop is
  behind a `Transport` seam. The pgx `LISTEN`/`NOTIFY` adapter was removed in
  favour of the GORM `Outbox` (a polled table), per ADR 0020; either way the
  seam is what a shared bus replaces.
- **Rate limiting** (`internal/server/rate_limiter.go`): in-memory token
  buckets per client IP, per process.
- **Caches** (`internal/api/cache.go` `TTLStore` and the domain caches around
  catalog identity, provider discovery/JWKS and dialect metadata): in-process
  maps.
- **WebSocket tickets** (`internal/wsticket`): an in-memory single-use ticket
  store; its own comment says a multi-replica deployment needs a shared store.

There is no Redis client, no Redis deployment, and no shared cache or limiter
in the tree today.

## Current state and inventory (#764)

What must become shared, and what can stay local:

| Component | Today | After this decision |
|---|---|---|
| Execution status/statement changes | per-connection DB `pollTicker` | Published on the bus; the WebSocket subscribes and re-reads the DB (the DB stays the source of truth) |
| Execution WS ticket (`wsticket`) | in-memory, single-use | Redis `SET` with TTL + `GETDEL` so a ticket must be consumed on any replica, once |
| Assistant turn events (`message`/`delta`) | in-process `Hub` | Bus per conversation; replay stays from the database; deltas stay best-effort |
| Monitoring change notifications | GORM `Outbox` poll | Redis pub/sub behind the existing `Transport` seam; `Outbox` remains the fallback |
| Rate limiting (generic and auth routes) | in-memory per replica | Redis token bucket / fixed window; in-process fallback when Redis is absent |
| TTL caches | in-process | Redis only for the caches ADR-safe across replicas (catalog metadata, provider discovery/JWKS, dialect data); request- and tenant-scoped authorization decisions stay local |
| Webhook delivery | none | Redis Streams + consumer group (at-least-once for `execution.finished`/`catalog.synced`) |

Everything that is a security or authorization decision derived from the
request (the caller's identity, a per-request grant check) stays local; only
the data that is genuinely global and safe to share moves to the shared layer.

## Decision

- **Redis is the shared backbone.** One service provides cross-replica
  pub/sub, a distributed cache, a distributed rate limiter and Redis Streams.
  **Dragonfly is an allowed drop-in**: it speaks the same protocol, so it is a
  deployment choice, not a second code path.

- **NATS / JetStream is not adopted.** It would add a second service and a
  second client for a scope Redis already covers. It is reconsidered only if a
  durable inter-service event backbone becomes the primary need rather than an
  add-on.

- **The client is `github.com/redis/go-redis/v9`**, wrapped by one internal
  package (`internal/realtime`) that owns the connection and exposes the bus,
  cache, limiter and Streams surfaces. Domain code depends on the wrapper, not
  on the driver. The address is read from `BALEIA_REDIS_URL`; empty means "no
  shared layer".

- **Graceful degradation.** With no `BALEIA_REDIS_URL` (dev, single replica),
  the app keeps today's behaviour: the assistant hub and monitoring outbox
  stay in-process, the limiter stays per replica, the execution WebSocket keeps
  polling, and the ticket store stays local. A multi-replica deployment must
  set Redis; the absence of it is logged at startup and never silently
  degrades a multi-replica correctness guarantee.

- **Redis Streams is a transient delivery buffer, never the durable store of
  record.** The source of truth for executions, catalogs and audit remains the
  store (Postgres/MySQL/MariaDB); the durable audit record is the `audit_events`
  table (and its OpenSearch export). A Stream holds an event only until a
  consumer group acknowledges delivery (or it is dead-lettered); it is not a
  log archive.

- **Delivery guarantees are explicit and bounded.** `execution.finished` and
  `catalog.synced` webhooks are **at-least-once** (Streams + consumer group +
  ACK + replay from `last-delivered-id`). `audit.*` webhooks are
  **best-effort**, because `internal/audit/postgres.go` records events without
  failing the request and a lost write is only counted (`audit_write_failed`).
  No consumer may promise a stronger guarantee for audit than the audit store
  itself provides. Audit events are published inside `Postgres.record` after
  the insert succeeds.

- **Migration order** is the one the epic executes, one PR at a time: decision
  and inventory, Redis foundation, cache and rate limit, monitoring bus,
  assistant SSE, execution WebSocket, webhooks API, webhooks relay.

## Consequences

- **Easier:** the API can scale horizontally; real-time features become
  cross-replica instead of per-process; rate limits and caches hold across the
  fleet; webhook delivery gets retries and replay without a second messaging
  product.

- **Harder:** a new stateful dependency to deploy, monitor and secure (HA,
  persistence, eviction policy, network policy); every real-time writer must
  publish consistently and every reader must handle reconnect/resync; the
  fallback paths double the code a feature has to test.

- **Must now be true:** no new in-process hub or per-replica limiter is
  introduced for a cross-replica feature; Redis access goes through
  `internal/realtime`; Streams are never treated as durable storage; the audit
  webhook guarantee is never described as at-least-once.

## Current compliance

Not yet implemented. The code still uses the in-process hubs, the per-replica
limiter, the polled outbox and the per-connection `pollTicker`; the deployment
has no Redis. The issues below close the gap and this section is updated as
they land:

- **#775, #778:** Redis deployment and the `internal/realtime` client.
- **#776, #777:** shared cache and distributed rate limiter.
- **#780:** monitoring events onto the bus.
- **#779:** assistant SSE onto the bus.
- **#766:** execution WebSocket off the `pollTicker`, plus the shared
  `wsticket` store.
- **#768 (split):** webhook registration and the Streams relay.

## Alternatives considered

- **NATS / JetStream as the backbone.** Rejected for now: a second service and
  client for a scope Redis already covers (pub/sub, cache, rate limit). Revisit
  only if a durable inter-service event backbone becomes the primary need.

- **Keep Postgres `LISTEN`/`NOTIFY` for the cross-process hop.** Rejected: it
  cannot carry cache or rate-limit state, and ADR 0020 removed the pgx hop from
  the store path. The `Transport` seam it sat behind is retained and reused.

- **Keep polling the database for every real-time view.** Rejected: it is the
  per-connection load this epic removes, and it does not give cross-replica
  push.

- **A second dedicated cache (e.g. Memcached) and a separate limiter store.**
  Rejected: Redis covers all three needs with one service and one client.

- **Storing audit or application logs in Redis.** Rejected: Redis Streams is a
  transient delivery buffer, not a log archive. Durable audit stays in the
  store and its OpenSearch export; application logs stay on stdout and are
  collected by the platform.
