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

Every request log line carries `trace_id` and `span_id` when tracing is on.

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
