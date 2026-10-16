# Operations

How to monitor Baleia: what it exposes, how to scrape it, and what to do when an alert fires.
The metrics catalog and its rules come from #87.

## Endpoints

| Process | Endpoint | Notes |
|---|---|---|
| API | `GET /metrics` on the API port (`8080`) | Behind `BALEIA_METRICS_ENABLED` (default `true`) |
| Worker | `GET /metrics` and `GET /healthz` on `BALEIA_WORKER_METRICS_ADDR` (default `:9090`) | Its own listener, separate from the health port `8081` |

When `BALEIA_METRICS_TOKEN` is set, both endpoints require `Authorization: Bearer <token>`. In the chart, the token is the optional Secret key `BALEIA_METRICS_TOKEN` (`secrets.values` or `existingSecret`); the chart refuses it under `config`.
Metrics are never behind the user JWT, because Prometheus cannot log in.

Each process exports these families:
- `baleia_*`: Baleia's own catalog (`internal/metrics`).
- `temporal_*`: the Temporal SDK's own metrics, reported into the same registry. These cover workflow and activity counts, latencies and poller stats.
- The Go runtime (`go_*`) and process (`process_*`) collectors.

Label values are bounded:
- no catalog, table, user, workspace or execution identifiers;
- no SQL;
- `route` is the chi route template (`/api/v1/workspaces/{id}/executions`);
- requests no route served use `route="unmatched"`.

`TestLabelAllowList` and `TestNoForbiddenValues` enforce this.

## Scraping

The chart annotates both pods with `prometheus.io/scrape`, `prometheus.io/port` and `prometheus.io/path`.
The rules in [`deploy/prometheus/alerts.yml`](../deploy/prometheus/alerts.yml) expect two jobs:
- `baleia-api`;
- `baleia-worker`.

Name them after the pod's `app.kubernetes.io/component` label:

```yaml
scrape_configs:
  - job_name: baleia
    kubernetes_sd_configs:
      - role: pod
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name, __meta_kubernetes_pod_annotation_prometheus_io_scrape]
        regex: baleia;true
        action: keep
      - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
        regex: ([^:]+)(?::\d+)?;(\d+)
        replacement: $1:$2
        target_label: __address__
      - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
        target_label: __metrics_path__
      - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_component]
        replacement: baleia-$1
        target_label: job
    # With BALEIA_METRICS_TOKEN set:
    # authorization:
    #   credentials_file: /etc/prometheus/baleia-metrics-token
```

How to load the rules:
- **Prometheus:** list `deploy/prometheus/alerts.yml` under `rule_files`.
- **Prometheus Operator:** copy its `groups` into a `PrometheusRule`'s `spec.groups`.

CI runs `promtool check rules` and `promtool test rules deploy/prometheus/alerts_test.yml`.

## Metrics catalog

| Domain | Metrics | Labels |
|---|---|---|
| HTTP (API) | `baleia_http_requests_total`, `baleia_http_request_duration_seconds`, `baleia_http_inflight_requests` | `route`, `method`, `status_class` |
| Auth (API) | `baleia_auth_attempts_total` | `kind` (`password`, `oidc`, `refresh`), `outcome` (`success`, `invalid_credentials`, `rate_limited`, `error`) |
| Authz (API) | `baleia_authz_denials_total` | `resource`, `reason` (`not_found`, `forbidden`) |
| Engines (worker) | `baleia_engine_statements_total`, `baleia_engine_statement_duration_seconds`, `baleia_engine_errors_total`, `baleia_engine_rows_returned_total`, `baleia_engine_result_bytes_total` | `engine` (engine kind), `outcome`, `code` |
| Schedules (worker) | `baleia_schedule_last_success_timestamp_seconds` | `schedule` |
| Catalogs (API) | `baleia_catalog_ddl_total`, `baleia_catalogs_by_state` | `action` (`create`, `replace`, `drop`), `outcome`, `state` |
| Metadata (worker) | `baleia_metadata_sync_duration_seconds`, `baleia_metadata_staleness_seconds_max` (the stalest enabled catalog; no catalog label) | `outcome` |
| Storage | `baleia_storage_operations_total`, `baleia_storage_bytes_total` | `operation` (`put`, `get`), `outcome` |
| Database pool | `baleia_db_pool_acquired_conns`, `baleia_db_pool_idle_conns`, `baleia_db_pool_max_conns`, `baleia_db_pool_acquire_duration_seconds` | `process` (`api`, `worker`) |
| Build | `baleia_build_info` | `version`, `commit` |

A metric is registered only where something produces it. Binding reconciliation
(`baleia_catalog_reconcile_actions_total`) with #37.

## Metadata snapshots

The explorer, autocomplete and search read stored metadata (namespaces, tables, columns), not live
`SHOW`/`DESCRIBE` calls. A pass of a catalog (`MetadataSyncWorkflow`, workflow id
`metadata-sync-<catalog id>`) stores what the catalog's source reports:

- **Iceberg catalog managers** (Polaris, Nessie, Iceberg REST): namespaces recursively, then each
  table. The fingerprint is the table's `metadata-location`; Polaris answers an unchanged table
  with `304` to the stored ETag, so a pass over an unchanged catalog loads no table.
- **Databases** (PostgreSQL, MySQL/MariaDB, SQL Server, Oracle, SQLite, ClickHouse): one query per
  catalog for every table and column. The fingerprint is an md5 of the columns.
- **Through an engine** (catalogs adopted from Trino, Flight SQL, kinds Baleia cannot read
  directly): the dialect's one-query catalog listing, in a read-only session as the engine's
  service identity, through the catalog's first active binding.

Every enabled catalog is refreshed every `BALEIA_METADATA_SYNC_INTERVAL` (default 15 min, at least
5 min) by the `metadata-sync` schedule, and right after it is created or changed (a new direct
engine's native catalogs too). Each scheduled pass first refreshes every enabled engine's catalogs:
new databases of a direct engine become native catalogs (missing ones are disabled, never deleted)
and catalogs found in Trino are adopted. At most 4 catalogs sync at once.

Tables a pass does not see are tombstoned (`removed_at`) and purged after 7 days. One pass per
catalog runs at a time (an advisory lock, and the workflow id). `GET
/catalogs/{id}/metadata/sync-runs` lists the latest passes with an `error_code` when one failed;
the source's own message is in `metadata_sync_runs.error_message` and the worker's log. A pass left
`running` by a crashed worker is failed with `interrupted` by the next pass of the catalog.

## Runbooks

### BaleiaAPIDown

Prometheus has not scraped an API pod for 2 minutes.

1. Check the pod: `kubectl get pods -l app.kubernetes.io/component=api`. Then run `kubectl describe pod` and `kubectl logs --previous` on it.
2. If the pod is in a crash loop, read the last log lines:
   - `temporal at <host> unreachable for 30s` means Temporal stayed unreachable at boot;
   - other startup failures show the database or migration error.
3. If the pod is running but the target is down, the scrape cannot reach it. Possible causes:
   - a network policy;
   - a wrong `prometheus.io/port`;
   - `BALEIA_METRICS_ENABLED=false`;
   - a token mismatch, which shows as 401 in the target's last error.

### BaleiaWorkerDown

Prometheus has not scraped a worker pod for 2 minutes. While no worker runs, nothing on the task queue moves:
- query executions;
- the schedules;
- catalog jobs.

The worker's own gauges (`baleia_schedule_*`) vanish with it, so this alert, not [BaleiaScheduleStale](#baleiaschedulestale), is the one that fires.

1. Check the pod: `kubectl get pods -l app.kubernetes.io/component=worker`. Then run `kubectl describe pod` and `kubectl logs --previous` on it.
2. If the pod is in a crash loop, read the last log lines. `temporal at <host> unreachable` means Temporal is down. Database errors mean Postgres.
3. If the pod is running but the target is down, the scrape cannot reach `:9090`. Check the following:
   - `BALEIA_WORKER_METRICS_ADDR`;
   - the `metrics` port;
   - the token.

### Interrupted executions

All statements of an execution run in one worker activity holding one engine session. If the
worker dies mid-statement (crash, eviction, `kubectl delete pod`), Temporal notices the missing
heartbeat within 30 s and the workflow:
- marks the running statement `failed` with `error_code = interrupted`;
- asks the engine to stop that statement by its persisted handle (best effort);
- marks the remaining statements `skipped` and the execution `failed`.

The interrupted statement is not run again: its outcome on the engine is unknown (an `INSERT` may
have committed). `BALEIA_STATEMENT_TIMEOUT` bounds each statement; `BALEIA_EXECUTION_TIMEOUT`
(default `2h`) bounds the whole script.

### BaleiaHighErrorRate

More than 5% of API requests returned 5xx over 10 minutes.

1. Find the routes: `sum by (route) (rate(baleia_http_requests_total{status_class="5xx"}[10m]))`.
2. Read the API logs for those routes. Each line carries `request_id`, which the response's `X-Request-ID` header also returns.
3. Common causes:
   - the database is unreachable or the pool is saturated (see [BaleiaDBPoolSaturated](#baleiadbpoolsaturated));
   - Temporal is down, which fails execution routes;
   - the object store is failing (`baleia_storage_operations_total{outcome="error"}`).

### BaleiaSlowAPI

The p95 latency of a route has been above 2 s for 15 minutes. Result downloads and the execution stream are excluded.

1. Check `baleia_db_pool_acquire_duration_seconds`. Slow acquires mean the pool is too small for the load. Raise `BALEIA_DB_MAX_CONNS`, within Postgres's `max_connections`.
2. Routes that call an engine are bound by the engine:
   - catalog browsing;
   - connection tests;
   - binding create and sync.
   Check the engine's own health.
3. Check the database for slow queries (`pg_stat_statements`).

### BaleiaEngineUnavailable

Statements on an engine kind have failed with `engine_unavailable` for 5 minutes. Baleia already retried each one with backoff before it accepted a statement, so the engine itself cannot be reached.

1. `GET /api/v1/engines` lists the engines of that kind. Test their connection with `POST /api/v1/catalog-connections/{id}/test`.
2. Check the engine. For the bundled Trino, check the `baleia-trino` pod and its logs.
3. If the engine is up, the worker cannot reach it. Possible causes:
   - DNS;
   - a network policy;
   - the SSRF-safe dialer refusing a private address that the deployment did not allow.

### BaleiaScheduleStale

A recurring job has not succeeded in three of its intervals:

| Schedule | Interval |
|---|---|
| `lens-query-poller` | 30 s |
| `lens-cluster-poller` | 30 s |
| `retriever-scheduler` | 60 s |
| `lens-table-health` | 1 h |
| `metadata-sync` | `BALEIA_METADATA_SYNC_INTERVAL` (15 min) |

1. Check that a worker is running and polling. A worker that is down raises [BaleiaWorkerDown](#baleiaworkerdown). If `temporal_worker_task_slots_available` is flat or `temporal_*` is missing, no worker is polling.
2. In the Temporal UI, open the schedule (its ID is the alert's `schedule` label). Read the last runs' failures.
3. The gauge is set when a pass completes: for table health, retriever dispatch and metadata sync, after the whole pass, not its first activity. It restarts empty with the worker. Right after a worker restart, a schedule shows up once its next pass succeeds.

### BaleiaMetadataStale

The stalest enabled catalog has not finished a metadata pass for more than an hour (`baleia_metadata_staleness_seconds_max`; a catalog that never synced counts from its creation).

1. Find the catalog: `GET /api/v1/connections/{id}/catalogs` shows `metadata_sync_status`, `metadata_synced_at` and `metadata_sync_error_code`, and `GET /api/v1/catalogs/{id}/metadata/sync-runs` its latest passes.
2. If the `metadata-sync` schedule itself is not running, see [BaleiaScheduleStale](#baleiaschedulestale).
3. Test the catalog's connection with `POST /api/v1/catalog-connections/{id}/test`. For an `engine` source path, check its engine (see [BaleiaEngineUnavailable](#baleiaengineunavailable)).
4. Retry one catalog with `POST /api/v1/catalogs/{id}/metadata/sync`.

### BaleiaCatalogsInError

Managed catalog bindings have been in the `error` lifecycle state for 30 minutes.

1. `GET /api/v1/catalogs/{id}/bindings` shows each binding's `lifecycle_state`, `sync_status`, `last_error_code` and `last_error_message`.
2. Fix the cause, then sync the binding. Common causes:
   - the engine refused the DDL (the plugin is not loaded, or the properties are invalid);
   - the engine was unreachable when Baleia sent the DDL.
3. `baleia_catalog_ddl_total{outcome="error"}` shows whether the engine is still refusing DDL.

### BaleiaDBPoolSaturated

More than 90% of a process's database pool has been in use for 10 minutes. Requests and activities are now queuing for connections.

1. Compare the pool size (`baleia_db_pool_max_conns`) with the load. Raise `BALEIA_DB_MAX_CONNS` for that process (default: API 20, worker 10). Keep the total across replicas under Postgres's `max_connections`.
2. If usage stays high without load, connections may be held by long transactions. Look for `idle in transaction` sessions in `pg_stat_activity`.
