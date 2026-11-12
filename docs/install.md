---
sidebar_position: 3
description: "Install Baleia on Kubernetes with the official Helm chart: API, worker and migrations, plus optional Trino with the catalog-store plugin and its dependencies."
---

# Install on Kubernetes

Each release publishes a container image (`ghcr.io/maltzsama/baleia`) and a Helm chart (`oci://ghcr.io/maltzsama/charts/baleia`). The chart runs the API, the worker and a migration job, and can run Trino with the catalog-store plugin.

## What you provide

The chart does not install Baleia's dependencies. Before installing, have:

| Dependency | Setting |
|---|---|
| A database for Baleia's own data: PostgreSQL (CI and the dev stacks run 18), or MySQL 8 / MariaDB (the `DATABASE_URL` DSN selects the dialector) | `DATABASE_URL` (secret) |
| [Temporal](https://temporal.io) (self-hosted or Temporal Cloud) | `config.TEMPORAL_HOST` |
| S3-compatible object storage for query results | `config.S3_ENDPOINT`, `config.S3_BUCKET`, `config.S3_REGION`, `config.S3_USE_SSL`; `S3_ACCESS_KEY` and `S3_SECRET_KEY` (secret) |
| SMTP, for invitations and password resets (optional) | `config.SMTP_*`; `SMTP_PASSWORD` (secret) |
| A way in: an Ingress or a load balancer in front of the `baleia-api` Service (ClusterIP, port 8080) | `config.PUBLIC_URL` |

## Secrets

Put the credentials in a Secret and name it in `existingSecret`. These keys are required:

| Key | |
|---|---|
| `JWT_SECRET` | Signs sessions |
| `BALEIA_ENCRYPTION_KEY` | Seals stored connection credentials; at least 32 characters, different from `JWT_SECRET` |
| `DATABASE_URL` | Baleia's store; the DSN selects PostgreSQL or the MySQL/MariaDB go-sql-driver DSN |
| `S3_ACCESS_KEY`, `S3_SECRET_KEY` | Result storage |

With the in-chart Trino, add `BALEIA_TRINO_DB_PASSWORD`, which the catalog-store plugin uses to read the catalog registry. See [Secrets](secrets.md) for key rotation and the optional keys.

```bash title="Create the secret"
kubectl create namespace baleia
kubectl -n baleia create secret generic baleia-secrets \
  --from-literal=JWT_SECRET="$(openssl rand -hex 32)" \
  --from-literal=BALEIA_ENCRYPTION_KEY="$(openssl rand -hex 32)" \
  --from-literal=DATABASE_URL='postgres://baleia:…@postgres:5432/baleia?sslmode=require' \
  --from-literal=S3_ACCESS_KEY='…' \
  --from-literal=S3_SECRET_KEY='…'
```

Keep `BALEIA_ENCRYPTION_KEY` safe: without it, stored connection credentials cannot be read.

## Values

A minimal `values.yaml`, here with the in-chart Trino:

```yaml title="values.yaml"
existingSecret: baleia-secrets

config:
  PUBLIC_URL: https://baleia.example.com
  TEMPORAL_HOST: temporal-frontend.temporal.svc:7233
  S3_ENDPOINT: s3.example.com
  S3_BUCKET: baleia-results
  S3_USE_SSL: "true"

trino:
  enabled: true
  plugin:
    version: 0.10.0   # the catalog-store plugin built for the chart's Trino (484)
```

To use a Trino you run yourself instead, leave `trino.enabled` false and set `config.TRINO_URL`. That Trino needs the [catalog-store plugin](https://github.com/maltzsama/baleia-trino-catalog-store) installed for Baleia to manage its catalogs.

**Memory of the in-chart Trino.** Set `trino.resources` to size the pod. The JVM's heap defaults to 80% of the memory limit (of the node's memory when there is no limit), and the code cache, metaspace, direct buffers and Parquet readers need room above the heap, so a limit that is too small gets the pod OOMKilled while it reads a table. To fix the heap, set `trino.jvmHeap` (for example `4G`), which becomes `-Xmx`; keep it at most about 75% of the limit. Empty, the default, keeps the image's percentage.

The in-chart Trino requires authentication (#647): it serves clients over HTTPS only and logs in Baleia with `secrets.values.BALEIA_TRINO_PASSWORD` (or that key in `existingSecret`), the password of `config.BALEIA_TRINO_ADMIN_USER`, which is required. The chart generates a CA and a server certificate, kept across upgrades, and the API and worker trust that CA; to use your own, set `trino.auth.tls.existingSecret` to a Secret with `tls.crt`, `tls.key` and `ca.crt`. Only Baleia's login may impersonate other identities. Trino writes its password file and keystore when its pod starts, and the API and worker read the password and the CA when they start: after changing the password or the TLS Secret, restart the three together (see [Operations](operations.md#rotating-the-bundled-trino-password-or-certificate)). The chart also restricts the coordinator to the API and worker pods with a NetworkPolicy (`trino.networkPolicy.enabled`, on by default; add peers with `trino.networkPolicy.extraIngressFrom`), which takes effect when your CNI enforces NetworkPolicy. See [Architecture](architecture.md#trino-security-model).

## BI gateway

The BI gateway (ADR 0017) serves Trino's HTTP client protocol from the API pods, so Tableau, Power BI and other Trino clients reach the engines only through Baleia, with the key's workspace scope and read-only access. It is **on by default** (`biGateway.enabled: false` turns it off). It renders a dedicated `baleia-bi-gateway` Service (the protocol lives at the server root, so the Ingress cannot share the API's `/api/v1` one) and, optionally, an Ingress:

```yaml title="values.yaml"
biGateway:
  enabled: true
  publicURL: https://bi.example.com   # the address the BI tools reach
  ingress:
    enabled: true
    className: nginx
    hosts: ["bi.example.com"]
    tls:
      - secretName: bi-example-tls
        hosts: ["bi.example.com"]
```

Without `biGateway.publicURL` the gateway rewrites URIs to its in-cluster Service address, which only BI tools inside the cluster reach; set it for tools outside.

`BALEIA_BI_GATEWAY_SECRET` is at least 32 characters, the same on every API replica. It signs the Trino URIs the gateway hands back, so any replica can serve the next page of a query. With `secrets.create` the chart generates it on the first install and keeps it across upgrades; set `secrets.values.BALEIA_BI_GATEWAY_SECRET` to choose your own. With `existingSecret`, add the key to that Secret yourself: the chart cannot check an external Secret, and the API pod does not start until the key exists.

BI tools sign in with HTTP Basic: the key owner's e-mail as the user and a workspace-bound BI key as the password (`POST /workspaces/{id}/bi-keys`), never a Trino credential. One key names one workspace and one engine; its statements are read-only and recorded in the history with origin `bi`. Only Trino engines are served: a direct engine (PostgreSQL, MySQL, …) has no gateway path.

`biGateway.publicURL` must be the URL the BI tools use, because the gateway rewrites every `nextUri` to it. With the Ingress enabled it must be `https://`: the key travels as the Basic-auth password, and the chart refuses a cleartext public URL. Capacity and scaling are covered in [Operations](operations.md#bi-gateway-capacity).

With the gateway configured, a Trino table's detail offers **Open in Tableau** and **Open in Power BI** when BI is on for the workspace; the download comes from `GET /workspaces/{id}/bi/{tool}` and points at `biGateway.publicURL` with the target's catalog and schema. It carries no credentials.

## Install

```bash title="Install the chart"
helm install baleia oci://ghcr.io/maltzsama/charts/baleia \
  -n baleia -f values.yaml \
  --version 0.8.0 # x-release-please-version
```

The migration job runs first. Open `PUBLIC_URL`: on the first visit, a setup wizard creates the admin account.

Every setting is listed in [Configuration](configuration.md). [Operations](operations.md) covers metrics, alerts, the audit trail, runbooks and the [upgrade notes](operations.md#upgrading-to-040).
