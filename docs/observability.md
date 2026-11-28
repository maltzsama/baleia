# Observability

## Metrics

`/metrics` is served by the API (gated by the optional `BALEIA_METRICS_TOKEN`)
and by the worker on `BALEIA_WORKER_METRICS_ADDR`. See ADR 0009 and the
Prometheus rules in `deploy/prometheus`.

Every name is prefixed `baleia_`. Label names come from a fixed allow-list
(`AllowedLabels` in `internal/metrics`). Label values are bounded, except the
`engine` label on the workload metrics, which carries the engine ID. The alert rules in
`deploy/prometheus/alerts.yml` read the series below; their unit tests
(`alerts_test.yml`) run them against fixed series.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `baleia_build_info` | gauge | `version`, `commit` | Always 1; the build that runs |
| `baleia_http_requests_total` | counter | `route`, `method`, `status_class` | HTTP requests by route template |
| `baleia_http_request_duration_seconds` | histogram | `route`, `method` | Request duration by route template |
| `baleia_http_inflight_requests` | gauge | | Requests being served |
| `baleia_auth_attempts_total` | counter | `kind`, `outcome` | Sign-in attempts by kind and outcome |
| `baleia_authz_denials_total` | counter | `resource`, `reason` | Denied operations by resource |
| `baleia_users_suspended_total` | counter | `reason` | Users suspended, by reason |
| `baleia_directory_reconcile_runs_total` | counter | `outcome` | Directory reconciliation passes |
| `baleia_engine_statements_total` | counter | `engine`, `outcome` | Statements by engine kind and outcome |
| `baleia_engine_statement_duration_seconds` | histogram | `engine` | Statement duration by engine kind |
| `baleia_engine_errors_total` | counter | `engine`, `code` | Statement errors by engine kind and error code |
| `baleia_engine_rows_returned_total` | counter | `engine` | Rows returned by engine kind |
| `baleia_engine_result_bytes_total` | counter | `engine` | Result bytes stored by engine kind |
| `baleia_cluster_active_workers` | gauge | `engine` | Active workers in the engine's latest cluster snapshot |
| `baleia_cluster_running_queries` | gauge | `engine` | Running queries in the latest snapshot |
| `baleia_cluster_queued_queries` | gauge | `engine` | Queued queries in the latest snapshot |
| `baleia_cluster_blocked_queries` | gauge | `engine` | Blocked queries in the latest snapshot |
| `baleia_cluster_memory_used_ratio` | gauge | `engine` | Share of the engine's memory in use (0 to 1) |
| `baleia_execution_queue_depth` | gauge | `engine`, `class` | Runs waiting in an engine's queue, by workload class |
| `baleia_execution_queue_running` | gauge | `engine`, `class` | Runs holding a slot on an engine, by class |
| `baleia_execution_queue_wait_seconds` | histogram | `engine`, `class` | How long an admitted run waited in the queue |
| `baleia_execution_admissions_total` | counter | `engine`, `class` | Runs admitted from an engine's queue |
| `baleia_execution_queue_timeouts_total` | counter | `engine`, `class` | Runs that timed out waiting in the queue |
| `baleia_db_pool_acquired_conns` | gauge | `process` | Store connections in use (`BaleiaDBPoolSaturated` divides it by the max) |
| `baleia_db_pool_idle_conns` | gauge | `process` | Idle store connections |
| `baleia_db_pool_max_conns` | gauge | `process` | Store connection limit (0 is unlimited) |
| `baleia_catalog_ddl_total` | counter | `action`, `outcome` | Catalog DDL sent to engines |
| `baleia_catalogs_by_state` | gauge | `state` | Managed catalog bindings by lifecycle state |
| `baleia_metadata_sync_duration_seconds` | histogram | `outcome` | Duration of one catalog metadata pass |
| `baleia_metadata_staleness_seconds_max` | gauge | | Age of the stalest catalog's metadata |
| `baleia_schedule_last_success_timestamp_seconds` | gauge | `schedule` | Unix time of a schedule's last successful pass |
| `baleia_retention_oldest_partition_start_timestamp_seconds` | gauge | `stream` | Start of the oldest partition still in the database |
| `baleia_retention_allowed_age_seconds` | gauge | `stream` | How old the oldest partition may get before retention is behind |
| `baleia_audit_write_failures_total` | counter | | Audit events that could not be written |
| `baleia_audit_export_events_total` | counter | `outcome` | Audit events exported |
| `baleia_audit_export_lag_seconds` | gauge | | How far the audit export is behind |
| `baleia_storage_operations_total` | counter | `operation`, `outcome` | Object storage operations |
| `baleia_storage_bytes_total` | counter | `operation` | Bytes moved to or from object storage |
| `baleia_search_requests_total` | counter | `kind`, `outcome` | In-product search requests, and whether they found anything |
| `baleia_agent_gate_decisions_total` | counter | `tool`, `outcome`, `reason` | Agent capability gate decisions |
| `baleia_webhook_deliveries_total` | counter | `outcome` | Webhook delivery attempts (`delivered`, `failed`, `dead`) |

The Go runtime, process and Temporal SDK metrics are served on the same
endpoints.

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
- **Trino HTTP** — `otelhttp.NewTransport` on the coordinator client; Trino
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
