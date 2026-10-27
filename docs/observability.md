# Observability

## Metrics

`/metrics` is served by the API (gated by the optional `BALEIA_METRICS_TOKEN`)
and by the worker on `BALEIA_WORKER_METRICS_ADDR`. See ADR 0009 and the
Prometheus rules in `deploy/prometheus`.

## Distributed tracing (#47)

Tracing is **off by default**. Turn it on with:

| Env | Default | Meaning |
|---|---|---|
| `BALEIA_TRACING_ENABLED` | `false` | Install the OpenTelemetry tracer provider. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | OTLP endpoint, e.g. `http://jaeger:4317` (gRPC). |
| `OTEL_SERVICE_NAME` | `api` / `worker` | Overrides the resource service name. |
| `OTEL_EXPORTER_OTLP_*` | — | Any other standard OpenTelemetry variable. |

When disabled, nothing is installed: the global provider stays the no-op one,
there is no exporter and no background goroutine.

### Boundaries

One trace covers, with the W3C `traceparent` propagated across every hop:

- **HTTP server** — an `otelhttp` server span wrapping the chi mux; its name is
  the matched route template (`internal/obs`, `HTTPHandler` + `RouteName`).
- **Temporal** — the SDK's OpenTelemetry interceptor on the client (API and
  worker) and the worker, so client → workflow → activity spans connect.
- **Trino HTTP** — `otelhttp.NewTransport` on the coordinator client; Trino 483
  continues the trace when its own tracing is enabled
  (`tracing.enabled=true`, `tracing.exporter.endpoint`).
- **Object storage** — the same transport on the S3 client.
- **Postgres** — the `otelpgx` query tracer, fanned out with the pool metrics
  tracer (`internal/obs.PGXTracer`). SQL statement text is **not** recorded
  (`otelpgx.WithDisableSQLStatementInAttributes`).

### Spans inside the application (#380)

Between the edges, the application opens its own spans:

| Span | Where | Attributes |
|---|---|---|
| `engine.open_session`, `engine.execute` | every engine, through the Manager's wrapper (`internal/queryengine`) | `baleia.engine.kind`, `baleia.session.read_only`, `baleia.rows`, `baleia.result_sets`, `baleia.error.code` |
| `binding.create`, `binding.replace`, `binding.drop` | catalog DDL (`internal/bindings`) | `baleia.binding.action`, `baleia.catalog`, `baleia.connector` |
| `metadata.sync` | one catalog's metadata pass (`internal/metadata`) | `baleia.catalog.id`, `baleia.metadata.path`, `baleia.tables.{seen,changed,removed}` |
| `s3.put`, `s3.get` | result writes and reads (`internal/storage`) | `baleia.bytes` |
| `assistant.turn` | one assistant turn; the provider calls inside it are HTTP client spans (`internal/assistant`) | `baleia.assistant.data_sharing`, `baleia.assistant.tool_calls` |

The direct PostgreSQL engine's pool has the `otelpgx` tracer too, so a user's
statements show under `engine.execute`. The other direct engines
(`database/sql`) are covered by `engine.execute` only. A failed span carries a
short fixed status, or the engine error code; never the error text, which can
quote a statement, a catalog's properties or an object key.

### Never traced

SQL statements and result values are never span attributes (bodies are not
captured); credentials, headers and query strings of `/auth/*` are never logged
(ADR 0008). Ids (`request_id`, `workspace_id`, `execution_id`) are fine on
spans, not on metric labels.

### Local stack

```sh
docker compose --profile tracing up -d      # Jaeger UI on http://localhost:16686
BALEIA_TRACING_ENABLED=true \
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 make dev
```

## Query events (#406)

The Trino query list is polled every 30 seconds (`lens-query-poller`). With
`BALEIA_TRINO_EVENTS_TOKEN` set, Trino's HTTP event listener also pushes each
completed query to `POST /internal/trino/query-events`, and the API records it
at once, with figures the list leaves out (output rows). The poller keeps
running: the listener sends no mid-flight progress, so the running-queries
list still comes from it, and it repairs drift. A poll read before a query
ended never sets it back to running.

The endpoint is not under `/api/v1`: it takes the token as `Authorization:
Bearer`, not a user session. Catalog DDL and the poller's own statement are
dropped, as the poller drops them, and the event itself is never stored (its
plans can carry literals). In the chart, `trino.queryEvents.enabled` writes
the listener's configuration, with the token read from the secret through
`${ENV:BALEIA_TRINO_EVENTS_TOKEN}`.

## Logs (#379)

The API, the worker and `cmd/migrate` log JSON lines to stdout from
`BALEIA_LOG_LEVEL` up (`debug`, `info`, `warn`, `error`; default `info`). A
line logged during a traced request, activity or turn carries its `trace_id`
and `span_id`, so a log line leads to its trace. The Temporal SDK logs
through the same logger.

## Profiling (#380)

`BALEIA_PPROF_ADDR` (unset by default) makes the API and the worker serve the
Go runtime profiles under `/debug/pprof/` on their own listener. It has no
authentication: bind it to loopback and reach it with `kubectl
port-forward`, e.g.

```sh
kubectl -n baleia port-forward deploy/baleia-api 6060:6060   # with BALEIA_PPROF_ADDR=127.0.0.1:6060
go tool pprof http://localhost:6060/debug/pprof/heap
```
