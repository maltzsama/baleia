# Operations

How to monitor Baleia: what it exposes, how to scrape it, and what to do when an alert fires.
The metrics catalog and its rules come from #87.

## Store migrations (ADR 0020)

Baleia's own schema is defined by GORM models and evolved by versioned Go migrations in `internal/store`; there are no SQL migration files.

- `make store-migrate` (or `/migrate -cmd=store-up` in the image) applies the pending migrations. The API also applies them on start; the worker does not wait for them, so its first jobs may fail against the old schema until the API is up, and Temporal retries them.
- `make store-migrate-status` (`-cmd=store-status`) prints the applied and pending versions; `make store-migrate-down` rolls back the last safe migration.
- A store created by the former goose migrations is adopted as the baseline on the first `store-up`: the existing schema is recorded and only newer Go migrations run.

`DATABASE_URL` selects the backend: a `postgres://` URL or the MySQL/MariaDB go-sql-driver DSN; see [Store backend](configuration.md#store-backend) for the DSNs and caveats. The query-execution path (Trino and the direct engines) is unaffected.

The migration numbers in the upgrade notes below (`00060`, `00062`–`00077`, …) are historical: they describe the goose schema at those releases. Upgrades from a goose-managed version run through the baseline adoption above; no manual SQL step is needed.

## Upgrading to 0.6.5

Read this before `helm upgrade` from 0.6.x. The connections dialer no longer
hardcodes its network policy (#782): the built-in denial of loopback,
link-local/cloud metadata (`169.254.169.254`), `0.0.0.0/8` and the private
ranges is gone, and the `system_owned`/managed-dialer split (#609/#689/#690/#694)
is removed. What a connection may or may not reach is now entirely the
operator's policy, set with `BALEIA_CONNECTIONS_DENY_CIDRS` and
`BALEIA_CONNECTIONS_ALLOW_CIDRS`.

The Helm chart ships these as **values**, not code:

| Setting | Chart default | Note |
|---|---|---|
| `BALEIA_CONNECTIONS_DENY_CIDRS` | `127.0.0.0/8,::1/128,0.0.0.0/8,::/128` | Deny wins over allow |
| `BALEIA_CONNECTIONS_ALLOW_CIDRS` | _(unset)_ | If set, only these may be dialed |

Cloud-metadata ranges are **not** in the chart default. If your deployment
relied on the old built-in metadata denial, add your cloud's metadata range to
`BALEIA_CONNECTIONS_DENY_CIDRS` before upgrading (e.g. `169.254.0.0/16` on
OKE/AWS). Deployments that reached their own internal databases or catalog
managers (Polaris, Postgres, …) through the managed dialer no longer need any
flag: with the defaults those ranges are reachable.

## Upgrading to 0.6.0

Read this before `helm upgrade` from 0.5.x. Migrations 00062 to 00072 add the
workload queues and rules, statement references (lineage, popular tables and
join suggestions), engine external URLs and the Trino resource-group tables;
migration 00074 adds the identity-provider tokens of OIDC sign-ins (#648),
00075 the workspace roles, 00076 service accounts and 00077 the source of a
workspace role (#653).
They apply as described for 0.4.0 below. Executions already running finish on
`QueryExecutionWorkflowV3`, which the worker still runs, so there is nothing
to drain.

### 1. Behavior changes

These take effect with their defaults:

| Change | Effect | To keep the old behavior |
|---|---|---|
| Private ranges denied (#609) | User-owned connections can no longer reach RFC1918, `100.64.0.0/10` or IPv6 ULA addresses. The default Trino cluster (`TRINO_URL`), the monitor and the binder are not affected | List the ranges in `BALEIA_CONNECTIONS_ALLOW_CIDRS` |
| Connections are admin-only (#610) | Only admins create, edit and test connections | `BALEIA_CONNECTIONS_CREATE=everyone` |
| Executions are scoped (#611) | Members see their own executions and results; workspace owners and admins see all | — |
| Space and retriever writes (#612, #613) | Creating spaces and changing retrievers need a workspace owner or admin | — |
| Grants carry no read/write level (#651) | A catalog linked to a workspace stays reachable by its members only, but no session is read-only because of the link; the catalog manager decides what each member may read or write. `permission` is gone from the grant API and from workspace targets; the unused column stays in the database until #654 | — |
| Connection and binding flags (#651) | `default_readonly`, stored but never enforced, is gone from the connection API; Unity bindings are no longer flagged as unable to write (`writes_supported`), since Unity decides | — |
| Authenticated bundled Trino (#647) | The in-chart and compose Trino require HTTPS and a password; Baleia logs in with `BALEIA_TRINO_PASSWORD`. The chart refuses to render without `secrets.values.BALEIA_TRINO_PASSWORD` (with `existingSecret`, add the key), and `TRINO_URL` defaults to the HTTPS port 8443. Clients other than Baleia, such as a BI tool or a scraper on port 8080, are refused | Set `trino.auth.enabled=false` (not recommended) |
| Per-user identity (#648) | Off by default: catalogs keep acting as the connection's account until an admin turns on **Per-user identity** on a Polaris, Unity, Lakekeeper or Iceberg REST connection. OIDC sign-ins now keep the identity provider's tokens, sealed with `BALEIA_ENCRYPTION_KEY` (migration 00074) | — |
| Workspace roles (#653) | Members have a workspace role: team owners are admins and team members editors, as before, and workspace admins can change them or share personal workspaces (migration 00075). A space entry no longer raises a member above their workspace role: an editor named manager of a space is an editor there. Editors now create retrievers, which needed a workspace owner (#613); documenting tables needs an editor | Give the member the admin role in the workspace |
| Service accounts and group roles (#653) | New and off until used: an admin creates service accounts (migration 00076), and a provider's `workspace_mapping` sets workspace roles at each sign-in (migration 00077). A role a workspace admin set by hand wins over one from a group | — |
| Agents follow roles (#653) | An external agent acts with its owner's workspace role. A viewer's agent stays read-only, but the agent of an editor or admin can now run write statements (DDL and DML), where agents were always read-only. Catalog management stays refused, the built-in assistant stays read-only, and the catalog manager still decides what the agent may write. A queued write is rechecked when its session opens | Make the owner a viewer of the workspace |
| Catalog names (#617) | `trino_catalog_name`, `connector` and `engine_catalog_name` must match `^[a-z][a-z0-9_]{0,62}$` on write | — |

Before upgrading, check that no connection users rely on points at a private
address, or allow its range first.

### 2. Workload management is opt-in

The queues, rules and classes of [workload management](guides/workload.md)
stay off until `BALEIA_WORKLOAD_ENABLED=true`. When you turn them on, each
class's runtime timeout replaces `BALEIA_STATEMENT_TIMEOUT` and
`BALEIA_EXECUTION_TIMEOUT` for that class (5 minutes for `interactive` by
default), so review **Settings → Workload** first.
[Managed Trino resource groups](#managed-trino-resource-groups-a-5) are a
separate, per-engine switch.

### 3. Index the existing history (optional)

Popular tables and join suggestions are built from query history, and lineage
from each statement's references. New runs are indexed as they finish. To
index the history from before the upgrade, run the one-off backfill from a
checkout of the release tag; it is idempotent and can be re-run:

```bash
go run ./cmd/backfill-references --database-url "$DATABASE_URL"
```

### 4. New settings

New and all optional: `BALEIA_WORKLOAD_ENABLED`,
the `BALEIA_WORKLOAD_ADAPTIVE_*` thresholds and
`BALEIA_TRINO_RESOURCE_GROUPS_ENVIRONMENT`. `BALEIA_CONNECTIONS_CREATE` now
defaults to `admins` (it was `everyone`), and `BALEIA_CONNECTIONS_ALLOW_CIDRS`
now also opens the internal ranges. See [configuration.md](configuration.md).


## Rotating the bundled Trino password or certificate

The in-chart Trino (#647) builds its password file and its keystore when its
pod starts; the API and the worker read `BALEIA_TRINO_PASSWORD` and the CA
when they start. Changing either one without restarting all three leaves
Trino refusing Baleia's login, or serving a certificate Baleia does not trust.
Schedule the change, since requests fail between the restarts:

1. Update the value: `secrets.values.BALEIA_TRINO_PASSWORD` (or that key in
   `existingSecret`), or the TLS Secret (`trino.auth.tls.existingSecret`, or
   delete the chart's `<release>-trino-tls` Secret so the next upgrade
   generates a new CA and certificate).
2. Apply it with `helm upgrade`. With `secrets.create`, a new password
   already restarts Trino.
3. Restart the three Deployments in one go:

   ```bash
   kubectl -n <ns> rollout restart deploy/<release>-trino deploy/<release>-api deploy/<release>-worker
   ```

The chart's TLS Secret is generated once and kept across upgrades
(`helm.sh/resource-policy: keep`). A render without access to the cluster,
such as `helm template`, generates a new one; apply such output only together
with the restart above.

## Upgrading to 0.4.0

Read this before `helm upgrade` from 0.3.x. Migrations 00060 and 00061 add
the maintenance parameters and the table health metrics. Both the `migrate`
job and the API apply pending migrations when they start; the worker does
not wait for them, so its first jobs can fail against the old schema until
the API is up, and Temporal retries them.

### 1. Drain running queries

0.3.x runs queries on `QueryExecutionWorkflowV2`; 0.4.0 only has its
successor. A query still running when the new worker starts cannot finish:
its execution stays "running". Drain them before upgrading:

1. Cancel the long queries you do not want to wait for, from **SQL →
   History**, while 0.3.x still runs.
2. Stop new submissions by scaling the API down. The 0.3.x worker keeps
   running and finishes what is left:

   ```bash
   kubectl -n baleia scale deployment/baleia-api --replicas=0
   ```

3. Wait until no query is running:

   ```bash
   temporal workflow count \
     --query 'WorkflowType="QueryExecutionWorkflowV2" AND ExecutionStatus="Running"'
   ```

4. Run `helm upgrade`, which brings the API back on 0.4.0.

#### Queries left behind

If the count was not zero when the new worker started, those queries are
stuck: the 0.4.0 worker cannot run them, and terminating a workflow skips
its cleanup. Close them by hand:

1. Terminate the workflows. The command starts a batch job; repeat the count
   above until it is 0 before going on:

   ```bash
   temporal workflow terminate --reason "upgrade to 0.4.0" \
     --query 'WorkflowType="QueryExecutionWorkflowV2" AND ExecutionStatus="Running"'
   ```

2. Stop what may still run on the engines. Each stuck statement keeps its
   engine handle; on Trino, kill it with
   `CALL system.runtime.kill_query(query_id => '<engine_query_id>', message => 'upgrade')`.
   Otherwise it ends at the engine's own statement timeout.

   ```sql
   SELECT s.execution_id, s.position, s.engine_query_id, s.query_handle
   FROM query_statements s JOIN query_executions e ON e.id = s.execution_id
   WHERE e.status = 'running' AND s.status = 'running';
   ```

3. Close the statements, then the executions (the workflow ID of an
   execution is `exec-<execution id>`):

   ```sql
   BEGIN;
   UPDATE query_statements s
   SET status = CASE s.status WHEN 'pending' THEN 'skipped' ELSE 'cancelled' END,
       finished_at = now()
   FROM query_executions e
   WHERE e.id = s.execution_id AND e.status = 'running'
     AND s.status IN ('pending', 'running');
   UPDATE query_executions SET status = 'cancelled', finished_at = now()
   WHERE status = 'running';
   COMMIT;
   ```

   Run this only after the drain, while no 0.4.0 query has started yet, or
   narrow it to the stuck executions with `AND e.id IN (…)`.

### 2. Trino 484 needs plugin 0.10.0

The chart's Trino image is now `trinodb/trino:484`. The catalog-store plugin
is built per Trino version, and 0.10.0 is the first release for 484: set
`trino.plugin.version: 0.10.0` (and `config.BALEIA_PLUGIN_VERSION`, whose
default is now 0.10.0). An older plugin does not load on 484.

### 3. Managed Iceberg catalogs are re-created once

Managed Iceberg catalogs gain `iceberg.expire-snapshots.min-retention` and
`iceberg.remove-orphan-files.min-retention` (`BALEIA_ICEBERG_MAINTENANCE_MIN_RETENTION`,
default `1h`). The new properties change each catalog's version, so a
reconcile re-creates every managed Iceberg catalog on its engine once;
queries on a catalog fail briefly while it is re-created. Until then the
catalog keeps Trino's 7-day floor, and table maintenance refuses shorter
retentions.

Reconciliation is not automatic unless `BALEIA_RECONCILE_INTERVAL` is set
(the worker's boot run is a dry run). Run it from **Engines → (the engine) →
Apply reconcile**, or `POST /api/v1/engines/{id}/reconcile?dry_run=false`.
A run applies at most `BALEIA_RECONCILE_MAX_ACTIONS` changes (20); repeat it
until the run reports nothing left. See [Binding
reconciliation](#binding-reconciliation).

### 4. API and UI changes

- `TableHealth.needs_orphan_cleanup` is removed: read `due_operations`
  (orphan cleanup is now due when its maximum interval passes).
- Profile and API keys moved to `/profile` and `/profile/api-keys`; the old
  `/settings/profile` and `/settings/api-keys` answer 308.
- Monitoring's table health lists source Iceberg catalogs only.

### 5. New settings

All are optional. One group changes behavior with its defaults: Baleia's own
database sessions are now bounded, with a 1-minute statement timeout
(`BALEIA_DB_STATEMENT_TIMEOUT`), a 30-second lock timeout
(`BALEIA_DB_LOCK_TIMEOUT`) and a 1-minute idle-in-transaction timeout
(`BALEIA_DB_IDLE_IN_TX_TIMEOUT`) for the api and worker (the migrate job is
not bounded); raise them if a large installation hits them. The rest keep the previous behavior when unset: `BALEIA_LOG_LEVEL`,
`BALEIA_MAINTENANCE_MAX_CONCURRENT_RUNS`,
`BALEIA_ICEBERG_MAINTENANCE_MIN_RETENTION`, the other pool and HTTP timeouts
(`BALEIA_DB_*`, `BALEIA_HTTP_*`), `BALEIA_LOB_MAX_BYTES`,
`BALEIA_SECRET_FILE_BASE_DIR`, `BALEIA_PPROF_ADDR`, `WORKER_HEALTH_PORT`,
`POLARIS_CLIENT_ID`/`POLARIS_CLIENT_SECRET` and, for query events,
`BALEIA_TRINO_EVENTS_TOKEN` (`trino.queryEvents.enabled`). See
[configuration.md](configuration.md).

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
| Audit (API, worker) | `baleia_audit_write_failures_total` | — |
| Metadata (worker) | `baleia_metadata_sync_duration_seconds`, `baleia_metadata_staleness_seconds_max` (the stalest enabled catalog; no catalog label) | `outcome` |
| Search (API) | `baleia_search_requests_total`; the zero-result rate is `sum by (kind) (rate(baleia_search_requests_total{outcome="empty"}[1h])) / sum by (kind) (rate(baleia_search_requests_total[1h]))` | `kind` (`metadata`, `docs`, `content`, `federated`), `outcome` (`hit`, `empty`); never the query text |
| Storage | `baleia_storage_operations_total`, `baleia_storage_bytes_total` | `operation` (`put`, `get`), `outcome` |
| Database pool | `baleia_db_pool_acquired_conns`, `baleia_db_pool_idle_conns`, `baleia_db_pool_max_conns`, `baleia_db_pool_acquire_duration_seconds` | `process` (`api`, `worker`) |
| Build | `baleia_build_info` | `version`, `commit` |

A metric is registered only where something produces it. Binding reconciliation
(`baleia_catalog_reconcile_actions_total`) with #37.

## Audit trail

Security and data operations are recorded in `audit_events` (#45): who (`actor_kind`,
`actor_id`, `auth_method`), what (`action`, `resource_type`, `resource_id`, `workspace_id`), how
it ended (`outcome`, `reason`) and from where (`request_id`, `client_ip`, `user_agent`). Field
changes are kept in `changes`, with secret values stored as `***`. `GET /api/v1/audit/events`
lists them, newest first: admins see every event, anyone else only their own.

- **Append-only:** enforced by convention (the recorder only inserts); ADR 0020 removed the
  trigger.
- **Best-effort:** a failed write never fails the user's request. It is logged with
  `error_code=audit_write_failed` and counted in `baleia_audit_write_failures_total`
  ([BaleiaAuditWriteFailures](#baleiaauditwritefailures)).
- **Partitions:** the table is partitioned by month (native `RANGE`, ADR 0024). The
  `audit-partitions` schedule creates the partitions three months ahead, daily, and never drops
  one. There is no default partition: an event whose month has no partition fails to write.
- **Retention:** `BALEIA_AUDIT_RETENTION` (400 days by default). The `audit-retention` schedule
  drops a month's partition only after the cold offload has sealed that month; it is fail-closed
  and never drops data that has not been offloaded.
- **Manual prune:** `migrate -cmd=audit-prune -before=YYYY-MM` drains the same gated path by hand
  (it refuses without a cold bucket and refuses the current month or later). `--dry-run` reports
  the partitions that would be dropped without dropping them. `-before` **overrides the
  hot-retention window** (including the 400-day default), so it is the cutoff to choose carefully.

## Audit export

Organisations can ship the audit trail to an OpenSearch/Elasticsearch-compatible `_bulk` endpoint
(#50). It is **off by default**: set `BALEIA_AUDIT_EXPORT_URL` to enable it. Application logs stay
on stdout and are not exported.

- **Configuration:** `BALEIA_AUDIT_EXPORT_URL`, `_INDEX` (default `baleia-audit`, monthly
  `baleia-audit-YYYY.MM`), `_AUTH` (`none`|`basic`|`bearer`|`api_key`), `_USER`/`_PASSWORD`/`_TOKEN`,
  `_BATCH` (500), `_INTERVAL` (30s), `_CA_FILE`.
- **Schedule:** the `audit-export` schedule runs `AuditExportWorkflow` every `_INTERVAL`; one pass
  loops batches until caught up (up to 5 minutes) and returns.
- **At-least-once, ordered, resumable:** the cursor (`audit_export_cursor`) stores the last
  accepted `(occurred_at, id)` and only moves forward, never past a failed item. Documents use a
  deterministic `_id` (`<occurred_at unix nanos>-<id>`), so re-sends overwrite.
- **Safety lag:** events newer than 10 s are not exported, so a concurrent transaction that
  commits an earlier `occurred_at` is not skipped.
- **`audit_events` is never modified.**
- **Index template:** install `deploy/opensearch/audit-template.json`.
- **Lag:** `baleia_audit_export_lag_seconds` is the age of the newest unexported event;
  `baleia_audit_export_events_total{outcome}` counts batches. A lag over 15 minutes for 15 minutes
  fires [BaleiaAuditExportLagging](#baleiaauditexportlagging). A local target runs under the
  compose `opensearch` profile.

## Cold offload and retention

The audit trail and the query history are offloaded to S3 and pruned after their hot windows
(ADR 0024). The OpenSearch export above is a search path; the cold sink is the durable copy that
retention is gated on.

- **Format:** gzip NDJSON, one object per month under the configured prefix, with a per-month
  `manifest.json` recording the object keys, row counts and checksums. The audit stream writes one
  object per month (`audit-YYYYMM.ndjson.gz`); query history writes the pair
  (`query_executions-YYYYMM.ndjson.gz` and `query_statements-YYYYMM.ndjson.gz`), so a statement is
  never offloaded without its execution and a month is not complete until both exist. The cursor
  advances past a month only after its manifest is written.
- **Configuration:** `BALEIA_AUDIT_COLD_BUCKET`/`_PREFIX` and
  `BALEIA_QUERY_HISTORY_COLD_BUCKET`/`_PREFIX` (both off by default), `BALEIA_AUDIT_RETENTION`
  (400d), `BALEIA_QUERY_HISTORY_RETENTION` (180d), `BALEIA_AUDIT_COLD_GRACE` /
  `BALEIA_QUERY_HISTORY_COLD_GRACE` (7d), and `BALEIA_PRUNE_MAX_PARTITIONS` (1).
- **Schedules:** `audit-offload` and `query-history-offload` (hourly), and `audit-retention` /
  `query-history-retention` (daily). Each pair is created only when that stream's cold bucket is
  configured.
- **Fail-closed:** a partition is dropped only if the cold cursor has advanced past it and its
  manifest exists. An unconfigured or unreachable cold sink grows the database (with alerts)
  rather than losing data.

## Binding reconciliation

`POST /api/v1/engines/{id}/reconcile` (admins; dry run unless `?dry_run=false`) checks every
managed binding of a federated engine against what the engine reports (#37):

| Situation | Action |
|---|---|
| active or in error, missing in the engine | `create` (sync) |
| active, loaded with an older version or not confirmed by the plugin | `recreate` (sync) |
| deprovisioning, still in the engine | `drop`, then disabled |
| deprovisioning, already gone | disabled |
| a name Baleia managed that no binding owns any more | `drop` |
| a catalog the engine has that Baleia does not manage | reported as `adopted`; **never dropped or changed** |

Every change goes through the binding lifecycle's guarded transitions, so a binding a user is
changing is skipped. At most `BALEIA_RECONCILE_MAX_ACTIONS` (20) changes per run; the rest are
reported. Each run is kept (the last 100 per engine, `GET /api/v1/engines/{id}/reconcile-runs`), and
each applied change is audited as `catalog.reconcile`. The worker logs one dry run at boot (counts
only). A schedule (`catalog-reconcile`) runs only when `BALEIA_RECONCILE_INTERVAL` is set (at least
5 min). `POST /api/v1/trino/reconcile` is a deprecated alias for the default Trino engine.

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

## Connection tests

`POST /api/v1/catalog-connections/test` checks connection settings before they are saved and
persists nothing (#12); `POST /api/v1/catalog-connections/{id}/test` runs the same checks on a
saved connection. The UI only enables *Create* after a test of the exact settings succeeded.
The checks are the connection's roles, or the `intents` asked for:

| Check | What it proves |
|---|---|
| `ping`, `list_catalogs` | Baleia can reach and read the source (`metadata`) |
| `direct_engine` | Baleia can run SQL on it, through a transient engine that is never registered |
| `bind:<engine>` | a federated engine can reach it, through a short-lived catalog `baleia_test_*` |

A failed check carries a bounded `error_code` (`unreachable`, `tls`, `auth_failed`, `forbidden`,
`not_found`, `timeout`, `provider_unsupported`, `connection_denied_network`, `engine_unavailable`,
`internal`) and a short `error_message`; the upstream body is only logged. Each user may run 10
tests a minute (`429` beyond). A test catalog is dropped as soon as its check ends; one left by a
crash expires after 2 min and is dropped by the `ephemeral-catalog-sweeper` schedule (every
minute).

## Workload tagging (Trino)

Baleia tags every Trino session with the run's workload class (#399, ADR 0014):
the client tag `baleia.class=<class>` and, when the session may write, the
`query_priority` session property. The classes are `interactive`, `heavy` and
`background`. Trino treats **larger numbers as higher priority**
(`trino-main` `SystemSessionProperties`), so Baleia sends `interactive=10`,
`heavy=5`, `background=1`.

The Baleia catalog guard denies every system session property except
`query_max_*` in a read-only session, so a read-only run carries the client tag
but not `query_priority`; a resource-group selector on `baleia.class` is the
reliable path until the guard allows `query_priority`.

The tag is sent whether or not [workload management](guides/workload.md) is
on. Without Baleia's own queues, or alongside them, a Trino resource-group
policy keyed on the client tag splits the classes inside Trino too:

```properties
resource-groups.configuration-manager=file
resource-groups.config-file=etc/resource-groups.json
```

```json
{
  "rootGroups": [
    {
      "name": "baleia",
      "softMemoryLimit": "80%",
      "hardConcurrencyLimit": 50,
      "maxQueued": 500,
      "subGroups": [
        { "name": "interactive", "hardConcurrencyLimit": 20, "maxQueued": 200 },
        { "name": "heavy", "hardConcurrencyLimit": 3, "maxQueued": 20 },
        { "name": "background", "hardConcurrencyLimit": 2, "maxQueued": 20 }
      ]
    }
  ],
  "selectors": [
    { "clientTags": ["baleia.class=interactive"], "group": "baleia.interactive" },
    { "clientTags": ["baleia.class=heavy"], "group": "baleia.heavy" },
    { "clientTags": ["baleia.class=background"], "group": "baleia.background" },
    { "group": "baleia.interactive" }
  ]
}
```

Selectors are evaluated in order and the first match wins; the last one is the
catch-all for a session with no class. Set the root group's `schedulingPolicy`
to `weighted` or `query_priority` to also honour the session property.

### Managed Trino resource groups (A-5)

Baleia can also generate this document itself and publish it into Trino's
**database** resource group manager, so the capacities an admin sets in
Settings › Workload become Trino's own queue (A-5, #598–#601). This is the
per-engine handover that avoids double queueing: when it is on, Baleia stops
holding Trino runs in its own admission queue and lets Trino's resource group
queue them instead.

To hand a Trino engine over:

0. Before you start, make sure Trino already limits Baleia's sessions, e.g.
   the file-based policy above. From step 1 until step 2 takes effect, Baleia
   no longer queues the engine's runs while Trino still uses its previous
   manager; if that manager has no limits, the runs reach the cluster
   unthrottled. Without such a policy, do the two steps in a quiet window.
1. Turn the engine's switch on: the `trino_resource_groups_enabled` field of
   its queues (`PUT /engines/{id}/queues`, or Settings › Workload). Saving
   publishes the groups in the same transaction as the switch and the
   capacities; if publishing fails, nothing changes and the save reports the
   error.
2. Point Trino's resource group manager at the same Postgres (the file
   `deploy/trino/resource-groups.properties` ships this). Do it after step 1:
   a `db` manager reading empty tables has no group to put a query in.

```properties
resource-groups.configuration-manager=db
resource-groups.config-db-url=jdbc:postgresql://postgres:5432/baleia
resource-groups.config-db-user=baleia_trino
resource-groups.config-db-password=${ENV:BALEIA_TRINO_DB_PASSWORD}
```

Each class gets a group under `baleia` and a selector on its `baleia.class`
tag. A closed class (max running 0) keeps its group with
`hardConcurrencyLimit` 0, so Trino holds its runs (up to its max queued) as
Baleia's own queue would, instead of routing them into an open class. Sessions
without the tag fall back to `interactive`, or to the first open class.

Baleia writes the generated `resource-groups.json` into the `resource_groups`
and `selectors` tables, and records each write as a revision in
`trino_resource_group_revisions`. Every save of the engine's queues while the
switch is on publishes a new revision. **Trino reloads those tables every
second**, so a capacity change applies without a coordinator restart.

The groups are written under `BALEIA_TRINO_RESOURCE_GROUPS_ENVIRONMENT`
(default `production`), which must match the cluster's `node.environment`. The
tables hold one set of groups per environment, so only one engine can own them
at a time: turning the switch on for a second engine is refused until the
first one's is off. Only a Trino engine has the switch.

Turning the switch off restores Baleia's own admission queue as the authority,
with no restart. Baleia leaves the tables as they are and publishes nothing
while the switch is off, so a capacity saved afterwards does not reach Trino:
as long as Trino's `db` manager still reads the tables, it applies the last
limits published while the switch was on, to Baleia's runs and to any other
session. To hand admission back for good, point Trino at its previous manager
(or remove the `db` manager) after turning the switch off.

**Rollback.** To undo a capacity or ceiling change, save the previous values
again: that publishes them as a new revision. `trino_resource_group_revisions`
keeps every written document, so the earlier values can be read from there.

### Direct engines

Where a direct engine has a native workload mechanism, Baleia sends the class
there too (#552):

| Engine | What Baleia sends | Where to see it |
|---|---|---|
| PostgreSQL | `application_name = baleia:<source>:class=<class>:<execution_id>` (truncated to 63 bytes) | `pg_stat_activity.application_name` |
| ClickHouse | the `priority` setting: `interactive=1`, `heavy=5`, `background=10` | `system.query_log` / `system.processes` |
| SQL Server | `Application Name=baleia-<class>` on the session's connection | Resource Governor classifier on `PROGRAM_NAME()` |
| Oracle | `DBMS_APPLICATION_INFO.SET_MODULE('baleia-<class>', <source>)` | `v$session.module` / Resource Manager consumer groups |
| MySQL/MariaDB, SQLite, Flight SQL | nothing native; Baleia's own queue is the control | — |

## Adaptive admission (Trino)

With `BALEIA_WORKLOAD_ENABLED=true`, the Lens cluster poller can throttle an
engine's admission queue (#551, #579). While the latest `cluster_snapshots`
row is above a threshold, only `interactive` runs are admitted and
`heavy`/`background` wait for the gate to lift. The gate is a signal to the
admission workflow, so it is reversible without a restart and clears on the
next snapshot below the threshold.

| Setting | Default | Trigger |
|---|---|---|
| `BALEIA_WORKLOAD_ADAPTIVE_QUEUED_THRESHOLD` | 20 | `queued_queries` above it |
| `BALEIA_WORKLOAD_ADAPTIVE_BLOCKED_THRESHOLD` | 10 | `blocked_queries` above it |
| `BALEIA_WORKLOAD_ADAPTIVE_MEMORY_HEADROOM_PCT` | 10 | free/total memory below it |

`0` disables a trigger. The queued-query gate is the primary one; the blocked
and memory gates catch saturation that the queue length does not show.

## BI gateway capacity

The BI gateway (ADR 0017) runs inside the API pods (`BALEIA_BI_GATEWAY_ADDR`,
rendered from `biGateway.enabled` in the chart), so **`api.replicas` is the
BI gateway's scale too**: every BI statement and every `nextUri` follow is an
HTTP request served by an API pod. The gateway keeps no per-query state, and a
rewritten URI carries a signature any replica can verify with the shared
`BALEIA_BI_GATEWAY_SECRET`, so scale out without session affinity.

- **Concurrency.** Each BI tool holds one statement in flight per open
  query/tab, plus short follow requests for its pages. Size `api.replicas` for
  the BI users you expect on top of the editor and API traffic, and keep the
  total (replicas × limit) inside what the coordinator and its resource groups
  can run. The gateway's own knobs are
  `BALEIA_BI_MAX_CONCURRENT_PER_KEY` (default 5) and
  `BALEIA_BI_MAX_CONCURRENT_PER_WORKSPACE` (default 20); over them the BI tool
  gets HTTP 429 with `Retry-After`. They bound a single key or workspace, not
  the cluster: the coordinator's queues still apply through the `baleia`/`bi`
  client tags.
- **Memory.** The gateway stores nothing and streams page by page: it holds at
  most one Trino page per in-flight request while it rewrites the URIs. Trino
  484 caps a page at 1 MiB (`TARGET_RESULT_SIZE`, fixed server-side, not
  requestable by a client), so a pod's added memory is roughly *in-flight BI
  requests × 1 MiB* plus the request/response copies. Results never go through
  Temporal or object storage.
- **Connections.** Each running BI statement keeps one HTTP connection from
  the gateway to the coordinator for its duration; the Trino `NetworkPolicy`
  and the connection pools see it like any other Baleia statement. Trino's
  query limits, resource groups and `BALEIA_STATEMENT_TIMEOUT` still apply.
- **Visibility.** Every BI statement is an execution with origin `bi` in the
  history (and in audit), so the usual history and monitoring views cover it;
  the per-key and per-workspace running counts come from the same table.
- **Revocation.** Turning the gateway off globally (admin, `PUT
  /admin/bi-gateway`), turning BI off for a workspace (`bi_enabled`), or
  revoking the key takes effect at the next request on any replica.

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
| `audit-partitions` | 24 h |

1. Check that a worker is running and polling. A worker that is down raises [BaleiaWorkerDown](#baleiaworkerdown). If `temporal_worker_task_slots_available` is flat or `temporal_*` is missing, no worker is polling.
2. In the Temporal UI, open the schedule (its ID is the alert's `schedule` label). Read the last runs' failures.
3. The gauge is set when a pass completes: for table health, retriever dispatch and metadata sync, after the whole pass, not its first activity. It restarts empty with the worker. Right after a worker restart, a schedule shows up once its next pass succeeds.

### BaleiaMetadataStale

The stalest enabled catalog has not finished a metadata pass for more than an hour (`baleia_metadata_staleness_seconds_max`; a catalog that never synced counts from its creation).

1. Find the catalog: `GET /api/v1/connections/{id}/catalogs` shows `metadata_sync_status`, `metadata_synced_at` and `metadata_sync_error_code`, and `GET /api/v1/catalogs/{id}/metadata/sync-runs` its latest passes.
2. If the `metadata-sync` schedule itself is not running, see [BaleiaScheduleStale](#baleiaschedulestale).
3. Test the catalog's connection with `POST /api/v1/catalog-connections/{id}/test`. For an `engine` source path, check its engine (see [BaleiaEngineUnavailable](#baleiaengineunavailable)).
4. Retry one catalog with `POST /api/v1/catalogs/{id}/metadata/sync`.

### BaleiaAuditWriteFailures

Audit events could not be written in the last 10 minutes, so the trail has gaps.

1. Read the API and worker logs for `error_code=audit_write_failed`.
2. If the error names a missing partition (`no partition of relation "audit_events" found for
   row`), the `audit-partitions` schedule is not running. Check
   [BaleiaScheduleStale](#baleiaschedulestale), then create the partitions by hand with
   `SELECT audit_ensure_partitions(3);`.
3. If the database is slow or down, see [BaleiaDBPoolSaturated](#baleiadbpoolsaturated).

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

### BaleiaAuditExportLagging

The newest unexported audit event is more than 15 minutes old for 15 minutes.

1. `baleia_audit_export_lag_seconds` is `now - occurred_at` at the cursor. Check the worker log for
   `audit export` errors and the OpenSearch endpoint's health.
2. A failing `_bulk` (auth, mapping, TLS) leaves the cursor in place: nothing is lost, it just waits.
   Fix the sink; the next pass resumes from the cursor.
3. A partial failure advances only to the last contiguous success. The failed item's index and
   reason are logged (never the document body).
4. Confirm the cursor moved: `SELECT * FROM audit_export_cursor WHERE sink = 'opensearch';`.

### BaleiaInteractiveQueueSlow

The p95 interactive queue wait has been above 30 seconds for 10 minutes (#399).
Interactive work — editor runs, table previews, Explore — is being starved by heavy or background
work on that engine.

1. Open Settings › Workload and check the engine's capacities and the live queue depth per class.
2. Raise the `interactive` capacity (or lower `heavy`/`background`), or add a rule that routes the
   heavy work away from the engine.
3. On Trino, confirm the class reaches the engine: `baleia.class` client tags and the resource-group
   policy in `docs/operations.md#workload-tagging-trino`.

### BaleiaQueueTimeouts

Runs timed out waiting in the queue over the last 15 minutes.

1. `sum by (engine, class) (increase(baleia_execution_queue_timeouts_total[15m]))` names the engine and
   class. Raise that class's `max_running`/`max_queued`, or its queue timeout, in Settings › Workload.
2. If the engine is slow (not just busy), check its own health: `baleia_engine_*` and the Lens
   cluster view. A slow engine makes every slot long-lived, so the queue backs up.
