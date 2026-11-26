# Data policies

Data policies are row filters and column masks. You write them in Baleia, OPA
decides them, and Trino enforces them
([ADR 0028](adr/0028-data-policies-opa-trino.md)). This page covers how to run
them: turning them on with the Helm chart, setting them up for a Trino that
Baleia does not install, what the deployment must guarantee, and the
`/governance` screens where policies are written and published.

## What is guaranteed

- Policies apply to every statement **Trino** runs on a policied table:
  - the editor, saved queries, the semantic layer, retrievers, agents and the
    BI gateway;
  - any other client that reaches the coordinator with a user identity.
- They do **not** apply to a path that skips Trino. Examples: Spark or
  PyIceberg reading the catalog manager directly, or a client reading the
  table files in object storage. Closing those paths is a deployment
  requirement (below).
- Policies are available only on catalogs served by Iceberg REST catalog
  managers.
- Baleia's own identities are **trusted** and never filtered: the admin
  (`BALEIA_TRINO_ADMIN_USER`) and the service user
  (`BALEIA_TRINO_SERVICE_USER`). Metadata sync, table health and maintenance
  run as them. ADR 0028 lists what each one reads.
- If OPA cannot be reached, every statement on that engine fails, including
  Baleia's own. Nothing falls back to "no policy".

## Turning them on with the Helm chart

The chart runs OPA as a sidecar of the in-chart coordinator.

```yaml
trino:
  enabled: true
  plugin:
    version: "0.11.0"        # or later: the release that ships the Rego
  dataPolicies:
    enabled: true
    publicKey: |
      -----BEGIN PUBLIC KEY-----
      ...
      -----END PUBLIC KEY-----
```

Make the signing key pair once and keep the private half in the release's
Secret:

```bash
openssl genrsa -out data-policy.pem 2048
openssl rsa -in data-policy.pem -pubout -out data-policy.pub
```

The Secret (`secrets.values`, or the keys of `existingSecret`) needs three keys:

| Key | What it is |
|---|---|
| `BALEIA_DATA_POLICY_SIGNING_KEY` | The PEM private key. The API and worker sign every bundle with it. |
| `BALEIA_OPA_TOKEN` | The bearer token OPA uses to fetch the bundle and report its status, 16 or more characters. `secrets.create` generates it once. |
| `BALEIA_DEFAULT_ENGINE_ID` | A UUID the coordinator's OPA reports its status under. Baleia records those reports on the default Trino engine, so it is not the engine's id and nobody needs to look one up. `secrets.create` generates it once; with `existingSecret`, put any fresh UUID there. It must not be the id of another engine. |

With data policies on, the chart:

- sets `BALEIA_DATA_POLICIES=opa` on the API and worker;
- copies the release's `opa/` directory (the Rego bundle and Trino's OPA
  properties) into a volume that Trino and OPA mount read-only. A plugin
  release without that directory stops the pod;
- adds OPA as the third entry of `access-control.config-files`, after the
  base rules and the catalog guard;
- runs OPA with its decision API on `127.0.0.1:8181` only, and `/health` and
  `/metrics` on the diagnostic port (`8282`);
- keeps OPA's last bundle on a small volume (`persistence`), so a restarted
  coordinator comes back with its policies while the API is down. The
  coordinator then rolls with `Recreate`.

### First start

The coordinator is ready only once OPA has activated a bundle
(`/health?bundles=true`), so no query reaches a coordinator without its
policies. When nothing has been published yet, the API publishes an initial
revision without policies at startup, and the worker publishes it too if that
first attempt fails. Until the first policy is published nobody is filtered,
as before data policies were on.

### Turning them on in an existing release

Nothing in the database changes: the default engine keeps its id, its
catalogs and its history.

Use `--reset-then-reuse-values` (Helm 3.14 or later): it takes the new chart's
defaults, which the OPA settings need, and keeps the release's own values.
`--reuse-values` would keep the old defaults, and the chart would fail to
render. The release keeps the plugin release it runs, so select one that ships
the OPA policy (0.11.0 or later). With `trino.plugin.image`, select the image
of that release too.

```bash
helm upgrade baleia ./deploy/helm/baleia --reset-then-reuse-values \
  --set-string trino.plugin.version=0.11.0 \
  --set trino.dataPolicies.enabled=true \
  --set-file trino.dataPolicies.publicKey=data-policy.pub \
  --set-file secrets.values.BALEIA_DATA_POLICY_SIGNING_KEY=data-policy.pem
```

With `existingSecret`, add the three keys to that Secret instead. The API and
the worker read the signing key and the OPA token at start: with
`secrets.create` a change to either rolls them; with `existingSecret`, restart
them after you change one.

### Marking the engine

Policies can only target catalogs bound to engines that state they enforce
them. After the upgrade, turn the flag on for the default engine:
`PATCH /api/v1/engines/{id}` with `{"data_policies": true}`, or the engine's
settings in the UI. The engine's data policy status then shows the revision
its OPA reports.

## Writing and publishing policies

Policies live under **Governance** (`/governance`), shown to global admins and
to whoever can create connections:

- **By object** lists one catalog's policies per table. You see this page
  only for catalogs whose connection you own, unless you are a global admin.
  - Add a row filter (permissive or restrictive) or a column mask, and edit
    or delete one. Saving checks the expression and keeps it as a draft.
  - **Publish** builds a signed revision. A refused publish lists every
    reason, such as an invalid policy, two masks with one priority, or a
    trusted-owned DEFINER view.
  - For each table, the page also shows what writes do (Trino 484's rules),
    the `SECURITY DEFINER` views over it and whose policies they apply, and
    the revision each engine runs.
  - Mask templates (null, constant, partial, `sha256`) only fill in the
    expression. An unsalted `sha256` of low-cardinality values can be
    reversed.
- **By subject** shows what is filtered and masked for one user, team,
  identity-provider group, service account or everyone.
  - It reads the saved, enabled policies of the catalogs you manage.
  - For a user, it includes their teams. Policies on the user's identity-provider
    groups are not included: choose the group to see them.
- **Revisions** (global admins) lists published revisions with their lease,
  and each engine's health and active revision. Use it to roll back to an
  earlier revision.

Each page also states the guarantee's scope, the side-door requirement, and
when each subject attribute reaches the engines.

## An external Trino

For a Trino that Baleia does not install, the operator deploys OPA and the
plugin configuration. Take the files from the `opa/` directory of the
`+trino484` plugin release tarball, which its `.sha256` covers:

| File | Use |
|---|---|
| `opa/baleia-opa-policy.tar.gz` | The Rego, as an OPA bundle. Load it as a local bundle. Its manifest revision is the digest Baleia expects (`opa/REVISION`). |
| `opa/opa-access-control.properties` | Trino's OPA access control with all five policy URIs. List it last in `access-control.config-files`. Trino reads an unset row-filter or mask URI as "no filter" or "no mask", so keep all five. |

OPA's configuration, with your engine's id from `GET /api/v1/engines`:

```yaml
services:
  baleia:
    url: https://baleia.example.com/opa/
    credentials:
      bearer:
        token_path: /var/run/baleia-opa/token   # BALEIA_OPA_TOKEN
labels:
  baleia_engine_id: <engine id>
bundles:
  baleia:
    service: baleia
    resource: bundles/baleia.tar.gz
    persist: true
    signing:
      keyid: baleia
  policy:
    resource: file:///policy/policy.tar.gz
keys:
  baleia:
    algorithm: RS256
    key: |
      <the PEM public key>
persistence_directory: /var/opa
status:
  service: baleia
```

Run OPA next to the coordinator:
`opa run --server --addr=127.0.0.1:8181 --diagnostic-addr=0.0.0.0:8282
--config-file=config.yaml trusted.json`. Here `trusted.json` holds the
identities that are never filtered, normally the guard's admin and service
users:

```json
{"baleia_deployment": {"trusted_identities": ["baleia_admin", "baleia"]}}
```

Provide the same readiness gate: no client traffic to the coordinator until
OPA's `GET /health?bundles=true` answers 200. Workers need no OPA, because
filters and masks are resolved while the query is planned on the coordinator.

An operator may also run their own Rego that reads the same bundle. In that
case, set `BALEIA_DATA_POLICY_REGO_DIGEST` to its manifest revision, so that
Baleia does not report the engine as unhealthy (`data_policy_digest_mismatch`).

## Deployment requirements

Data policies hold only if the deployment closes the paths around Trino:

- **No end-user path to the coordinator** except through Baleia (the editor
  and the BI gateway). The chart's NetworkPolicy admits only the API and the
  worker, on a CNI that enforces it.
- **No end-user path to the catalog manager or to the storage of restricted
  tables.** A user who can load a table from Polaris, or read its files from
  object storage, reads every row. With per-user catalog identity
  ([ADR 0015](adr/0015-per-user-catalog-identity.md)), the network is what
  keeps a user's own token away from the catalog manager.
- **The cluster's RBAC is the trust boundary.** Only the deployment pipeline
  and operators may write ConfigMaps, Secrets, Deployments and pods in the
  release's namespace. The Baleia and Trino service accounts may not. Anyone
  who can change the coordinator's pod spec can remove OPA from it.
- **Admission and audit.** Require images pinned by digest (Kyverno,
  Gatekeeper or similar), and keep the cluster audit log of writes to these
  objects. The security ConfigMaps are immutable and named by a content hash,
  so every change is a new object.

## Known limits

- **Writes.** Trino 484 refuses `UPDATE` and `MERGE` on a table with a row
  filter, and every write on a table with a mask. ADR 0028 lists what the
  `writes` field of a policy allows.
- **Views.** A `SECURITY INVOKER` view applies each reader's policies. A
  `SECURITY DEFINER` view applies its owner's: one owned by a trusted identity
  over a policied table makes the publish fail, and one owned by an ordinary
  user cannot be read at all, because Trino runs it without the reader's
  credentials. The SQL editor therefore creates views as `SECURITY INVOKER`
  unless the statement names a mode ([workbench](guides/workbench.md#run)).
- **Revision changes during a statement.** Trino asks OPA separately for the
  access check, the row filters and the masks of one statement. A statement
  analyzed while a new revision activates can take its filters from one
  revision and its masks from the other (#1149). Only a publish, a re-issue or
  a rollback activates a revision. To keep that mix from being looser than
  both revisions for any table, change either the row filters or the masks of
  a table in one publish, not both: when a change needs both, publish the
  narrowing part first (for example, add the mask) and the rest in a second
  publish.
  - Publish the second part only after every engine shows the first revision
    active. OPA fetches only the newest bundle when it polls, so two publishes
    inside one poll interval skip the revision in between.
  - Roll back the same way, one step at a time: from the second revision to
    the one in between, wait until every engine shows it active, then to the
    original. A rollback straight to the original changes the filters and the
    masks at once.
  - A statement that joins several tables can still take one table's filters
    from one revision and another's from the other. Each table then returns
    only rows one of the revisions shows it, the same a reader gets by reading
    each table before and after the change.
- **Other catalog types.** The Rego denies the metadata tables that carry
  values (`$files`, `$partitions` and others) and `table_changes` on policied
  tables. Allowing policies on another catalog type, such as one with
  materialized views (`$materialized_view_storage`), means reviewing those
  side doors again.
