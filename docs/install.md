---
sidebar_position: 3
---

# Install on Kubernetes

Each release publishes a container image (`ghcr.io/maltzsama/baleia`) and a Helm chart (`oci://ghcr.io/maltzsama/charts/baleia`). The chart runs the API, the worker and a migration job, and can run Trino with the catalog-store plugin.

## What you provide

The chart does not install Baleia's dependencies. Before installing, have:

| Dependency | Setting |
|---|---|
| PostgreSQL for Baleia's own data (CI and the dev stacks run 18) | `DATABASE_URL` (secret) |
| [Temporal](https://temporal.io) (self-hosted or Temporal Cloud) | `config.TEMPORAL_HOST` |
| S3-compatible object storage for query results | `config.S3_ENDPOINT`, `config.S3_BUCKET`, `config.S3_USE_SSL`; `S3_ACCESS_KEY` and `S3_SECRET_KEY` (secret) |
| SMTP, for invitations and password resets (optional) | `config.SMTP_*`; `SMTP_PASSWORD` (secret) |
| A way in: an Ingress or a load balancer in front of the `baleia-api` Service (ClusterIP, port 8080) | `config.PUBLIC_URL` |

## Secrets

Put the credentials in a Secret and name it in `existingSecret`. These keys are required:

| Key | |
|---|---|
| `JWT_SECRET` | Signs sessions |
| `BALEIA_ENCRYPTION_KEY` | Seals stored connection credentials; at least 32 characters, different from `JWT_SECRET` |
| `DATABASE_URL` | Baleia's PostgreSQL |
| `S3_ACCESS_KEY`, `S3_SECRET_KEY` | Result storage |

With the in-chart Trino, add `BALEIA_TRINO_DB_PASSWORD`, which the catalog-store plugin uses to read the catalog registry. See [Secrets](secrets.md) for key rotation and the optional keys.

```bash
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

```yaml
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

The in-chart Trino has no authentication. Read the catalog guard notes in [Architecture](architecture.md#trino-security-model) before setting `trino.catalogGuard.allowUnauthenticatedAdmin`.

## Install

```bash
helm install baleia oci://ghcr.io/maltzsama/charts/baleia \
  -n baleia -f values.yaml \
  --version 0.4.0 # x-release-please-version
```

The migration job runs first. Open `PUBLIC_URL`: on the first visit, a setup wizard creates the admin account.

Every setting is listed in [Configuration](configuration.md). [Operations](operations.md) covers metrics, alerts, the audit trail, runbooks and the [upgrade notes](operations.md#upgrading-to-040).
