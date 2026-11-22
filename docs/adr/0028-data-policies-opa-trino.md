# 0028. Row filters and column masks: authored in Baleia, decided by OPA, enforced by Trino

- Status: Accepted, amended by the spike in #994 (results under "Spike results").
- Date: 2026-11-13; amended 2026-11-22
- Issues: #994 (spike), #995 (implementation), #993 (catalog-manager RBAC), #992 (chart hardening)
- Extends: [0005](0005-trino-security-model.md) (a third access control next to the file rules and
  the catalog guard)

## Context

Catalog managers authorize on whole objects: a user can or cannot read a table. Deployments also
need **row filters** (a user sees only their own unit's rows) and **column masks** (a restricted
user gets `***` instead of an e-mail). Polaris does neither, and Baleia must not rewrite SQL to
provide them ([0005](0005-trino-security-model.md)).

Facts the design must respect:

- **Trino's OPA plugin.** Trino ships an OPA access control (`access-control.name=opa`). Besides
  allow/deny checks, it asks OPA for row filters and column masks. Trino applies what OPA returns
  inside the engine.
- **Trino already composes access controls.** `deploy/trino/config.properties` sets
  `access-control.config-files` to the file-based base rules and `baleia-catalog-guard`. An
  operation is allowed only if every access control allows it.
- **Trino has one client.** [0017](0017-bi-gateway.md) makes Baleia (editor, worker, BI gateway)
  Trino's only client.
- **Trino identities.** `trinoidentity.For` gives each user an identity: `u_<uuid>`, or the e-mail
  when `BALEIA_TRINO_USER_IDENTITY=email`. Trusted identities (admin, service) run maintenance,
  probes and jobs without a user, and carry no `baleia.catalogs`.
- **Sessions carry no attributes.** They carry only `baleia.mode`, the catalog `token` and
  `baleia.catalogs` as extra credentials: no teams, groups or roles. Trino has no group provider
  configured.
- **The Trino path is not the only path to the data.** Ingestion engines and any other client of the
  catalog manager reach the same tables through Polaris and object storage ([0017](0017-bi-gateway.md)).
  With per-user identity ([0015](0015-per-user-catalog-identity.md)), a user's own token is
  authorized by Polaris to read a table.
- **OPA does not keep durable state.** Policies pushed with `PUT /v1/policies` are lost on restart.
  The supported model is a **bundle** pulled from a service. With `persist: true`, OPA keeps the
  last activated bundle on disk, and its status API reports the active revision.
- **Baleia runs several API replicas** behind a load balancer ([0017](0017-bi-gateway.md)). It has
  object storage (`internal/storage`).
- **Engines may be external.** The chart can install Trino (`trino.enabled`), but an engine
  registered in Baleia may be operated by someone else.

## Decision

### Scope of the guarantee

- **The guarantee covers the Trino path only.** Data policies hold for every statement Trino runs,
  which is every SQL statement through Baleia. They are not lakehouse-wide policies. A client that
  reads through the catalog manager or object storage directly is not filtered or masked.
- **Restricted tables need closed side doors.** Deployments that use policies on a table must also
  close those paths:
  - no end-user network path to the catalog manager or to the table's storage;
  - credential vending limited to the principals Trino reads with.

  Baleia's policy screen shows this requirement next to every policy. The operator documentation
  lists it as a deployment requirement, not a Baleia guarantee.
- **With per-user identity, the user's own token reaches the catalog manager**
  ([0015](0015-per-user-catalog-identity.md)). The catalog manager must let that user load the whole
  table, so the same token used from Spark or PyIceberg bypasses Trino's filters. Two ways close it:
  - **Default mode:** the network requirement above. End users have no path to the catalog manager
    or storage.
  - **Polaris-OPA mode** (see "Who can read"): Polaris's own OPA policy refuses to load a policied
    table, or vend its credentials, unless the call comes from a Baleia session. The identity
    provider adds a session role (for example `baleia_session`) only to tokens issued to Baleia's
    confidential client, the tokens 0015 forwards. Polaris reads it through
    `quarkus.oidc.roles.role-claim-path` and passes it to OPA in `actor.roles`. The spike showed it
    on Polaris 1.9 and Keycloak: Baleia's token loads the policied table, the same user's token from
    another client gets 403, other tables are untouched.

### Who can read: two modes

Data policies decide rows and columns; whole-object access stays with the catalog manager. Baleia
supports two ways of deciding it, per Polaris connection:

- **Default: the manager's own RBAC.** Baleia administers Polaris's internal RBAC
  ([0027](0027-catalog-manager-rbac-administration.md), #993) and works with any Polaris.
- **Optional: Polaris with its OPA authorizer** (`polaris.authorization.type=opa`, Polaris 1.9+),
  reading the same Baleia bundle, so one source decides both layers.
  - Polaris's input is `actor` (principal, roles, attributes), `action`, `resource` (targets,
    secondaries) and `context`. It allows or denies whole objects only: Polaris has no row filters
    or masks (apache/polaris#137 and #5245 are open), so Trino and this ADR still own them.
  - This authorizer **replaces** Polaris's internal RBAC. In this mode #993's RBAC administration
    does not apply; the Polaris policy is an adapter of the same PEP-neutral Rego (see "What OPA
    decides").
  - Polaris 1.9 adds `polaris.authentication.credential-mode`. Baleia supports both values, declared
    on the connection like `auth_mode` (0027): `internal` (principals and grants in Polaris; the
    session role must be granted to each principal) and `external` (no principal entity; principal
    roles come from the token as-is).

### Optional, with three explicit states

- `BALEIA_DATA_POLICIES=off|opa` (default `off`), set per deployment. Each Trino engine also
  carries `data_policies: true|false`, which states that the engine is configured with the OPA
  access control and the Baleia Rego.
- Policies can target only catalogs bound to engines with `data_policies: true`. Binding a catalog
  that has policies to an engine without it is refused (`data_policies_unenforced`).
- Each engine's integration is shown as **disabled**, **enabled and healthy**, or **enabled and
  unhealthy** (no status report within the bundle interval, or an activation error). The state
  comes from configuration and status reports, never from a single failed call.

### Who deploys OPA

- **The Baleia chart** configures OPA only for the Trino it installs. OPA runs as a **sidecar**: a
  second container, using the official `openpolicyagent/opa` image pinned by digest, in the
  coordinator pod. The chart also adds OPA as the third entry in `access-control.config-files` and
  wires readiness (below).

  ```
  Pod: trino-coordinator
  ├── init: install-plugin   catalog-store plugin + Rego package, checksum verified (ADR 0004)
  ├── container: trino       access-control.config-files = base, catalog-guard, opa
  │                          opa.policy.*-uri = http://localhost:8181/v1/data/baleia/trino/...
  └── container: opa         --addr=127.0.0.1:8181, --diagnostic-addr=:8282
                             bundle "policy": file:///policy/policy.tar.gz (the verified Rego)
                             bundle "baleia": GET /opa/bundles/baleia.tar.gz, signed, persist: true
                             readiness GET :8282/health?bundles=true
  ```

  - **No network hop.** Trino calls OPA on `localhost` several times per statement.
  - **They fail together.** The two containers live and die as one pod, so "Trino up, OPA down"
    cannot happen inside it.
  - **One readiness for both.** The pod is ready only when both containers are.
  - **Coordinator only.** Access checks, filters and masks are resolved while the query is planned,
    so workers need no OPA. The spike confirmed it: a worker with its own OPA served no decision.
  - **OPA's API is loopback-only.** The data and policy API listens on `127.0.0.1`, so nothing on
    the cluster network can query OPA or `PUT` a policy; probes and metrics use the diagnostic port.
  - **The plugin properties ship with the Rego.** Trino treats an unset
    `opa.policy.row-filters-uri`, or an undefined `row_filters` result, as "no filter". The five
    URIs (`allow`, `batch`, `row_filters`, `column_mask`, `batch_column_masks`) are therefore part of
    the versioned plugin release, next to the Rego; the chart renders them and an external
    operator copies them.
- **For an external Trino**, the operator installs OPA and the plugin configuration. Baleia
  publishes the Rego package, the plugin properties and the bundle endpoint for them. It does not
  manage that deployment.

### Integrity of the policy code

- **The Rego comes from the plugin release.** It ships in the versioned tarball, under the same
  `.sha256` as the catalog-store plugin ([0004](0004-plugin-distribution.md)). The existing
  `install-plugin` init container verifies the checksum and copies the Rego into a volume that the
  OPA container mounts read-only. The Rego is packaged as an OPA bundle (roots `baleia/core`,
  `baleia/trino`) that OPA loads with `resource: file:///policy/policy.tar.gz`. A data bundle that
  claims an overlapping root is rejected, so the data bundle can never replace the Rego.
  - The chart contains no Rego text.
  - Changing the policy code means changing a pinned release and checksum, not editing a
    ConfigMap.
- **Security ConfigMaps are immutable.** Every ConfigMap that carries Trino security configuration
  is created with `immutable: true`: `config.properties`, the base rules, the guard properties,
  `baleia-rules.json`, the authenticator, and OPA's configuration. A change needs a delete and a
  recreate, which takes a separate permission and shows in the cluster audit log. This applies to
  the existing files too, not only to OPA's. Their names carry a hash of their content: the kubelet
  caches immutable ConfigMaps, and one deleted and recreated under the same name keeps serving the
  old content to new pods.
- **Drift is detected.** The Rego bundle's manifest revision is its digest (the SHA-256 of the
  sorted `<path> <sha256>` lines of its Rego files), and OPA's status report carries it to Baleia
  as `bundles.policy.active_revision`; every decision log carries it too. Baleia shows the engine as **enabled and unhealthy**
  (`data_policy_digest_mismatch`) when the digest differs from the one of the release the engine
  is pinned to.
  - This catches misconfiguration and careless tampering.
  - It is **not** a defense against someone who controls the pod: they can also forge the status.
- **The trust boundary is the cluster's RBAC.**
  - **Who may write.** Only the deployment pipeline and operators may write ConfigMaps, Secrets,
    Deployments and pods in the Trino namespace. The Baleia and Trino service accounts may not.
  - **What is checked.** Admission policy (Kyverno, Gatekeeper or similar) should require images
    pinned by digest, and the cluster audit log should record writes to these objects.

  Anyone who can write the pod spec controls Trino with or without OPA. This was already true of
  the guard ([0005](0005-trino-security-model.md)). The operator documentation states it as a
  requirement for turning data policies on.

### What OPA decides

- **Rego ships with the plugin release, data comes in the bundle.** The Rego package
  (`deploy/opa/baleia/`) is versioned with the Trino plugin and tested with `opa test` against the
  plugin's recorded input documents (`testdata/trino484`). OPA loads it from the verified init
  volume (see "Integrity of the policy code"), **not from the data bundle**. The data bundle carries
  only data, under the root `baleia/bundle`. The policy logic is therefore always present, even
  when no bundle has ever been activated.
- **The data flow.** The user fills the policy screens; Baleia saves the policies in its database;
  on publish it serializes them as-is into the bundle JSON; OPA loads Rego plus JSON; the Rego only
  reads the JSON. The Rego fixes only the semantics (below); every policy decision is the user's.
- **One PEP-neutral core, one thin adapter per enforcement point.**
  - `baleia.core` decides over Baleia's own terms: a subject (the engine identity), a table, a
    column. It answers `allow`, `row_filter` and `column_mask`.
  - `baleia.trino` translates the Trino OPA plugin's input into those terms. Another enforcement
    point (Polaris, Spark, Kyuubi) is another adapter over the same core and the same bundle.
- **The bundle JSON is a public, versioned contract.** `format_version` 1 has `revision`,
  `not_after` (the lease, below), `subjects` (Trino identity → user, service account, teams,
  identity-provider groups) and `tables` (catalog → schema → table → `row_policies` and
  `column_masks`). It is documented for operators. A bundle with another `format_version` is not
  active, so user identities are denied.
- **Bring your own Rego.** An operator may point OPA at their own Rego package that reads the same
  contract, without recompiling anything. The release package stays the default; the digest check
  then reports the operator's digest.
- **Trusted identities come from the deployment.** OPA reads them from a file the chart renders
  next to the guard's properties (`baleia.admin-users`, `baleia.service-users`), never from the
  data bundle.
- **OPA is no second RBAC, but it denies when it has no data.** The `allow` rule is:
  - **true** for trusted identities;
  - **true** for user identities while a bundle revision is active (supported `format_version`,
    lease not expired);
  - **false** for every user identity otherwise: no bundle yet, an invalid bundle, lost persistent
    storage, or an expired lease.

  OPA never grants anything another layer denies: catalog and table access stay with the catalog
  guard ([0018](0018-workspace-catalog-scope.md)) and the catalog manager.
- **Side doors are denied.** A policied table's values are reachable under other names, where no
  filter or mask applies: Iceberg's value-bearing metadata tables (`$files`, `$entries`,
  `$all_files`, `$all_entries`, `$partitions`, `$manifests`, `$all_manifests` expose column bounds
  and partition values) and the `table_changes` table function. The spike read real masked e-mails
  through `$files`. User identities are denied those for policied tables (and `table_changes` in a
  catalog with policies). `$history`, `$refs`, `$snapshots`, `$properties` and
  `$metadata_log_entries` stay readable, so time travel works; `$snapshots` reveals only the total
  row count. Trusted identities keep them all for table health and maintenance.
- **Filters and masks do not authorize writes.** Writes stay with the guard (read-only sessions)
  and the catalog manager.

### Writes on policied tables (Trino 484)

Trino decides what a write may do once a filter or a mask applies. The spike measured it:

| Policy on the table | INSERT | UPDATE | DELETE | MERGE |
|---|---|---|---|---|
| Row filter only | allowed when the new row matches the filter, else denied (PostgreSQL's `WITH CHECK`) | refused | removes only visible rows | refused |
| Column mask | refused | refused | refused | refused |

Nothing leaks, but a policied table is close to read-only for every filtered or masked identity.
The policy screen says so, and writes that need more go through a trusted job or an unpolicied
table.

### Policy model

- **Row policies follow PostgreSQL's model.**
  - **Permissive** policies grant visibility. A subject sees the **union** (`OR`) of the
    predicates of every permissive policy that matches them.
  - **Restrictive** policies narrow it. Every restrictive policy that matches is combined with
    `AND`.
  - **Once a table has any row policy, it is default-deny.** A user identity matched by no
    permissive policy gets the filter `false`.
  - The Rego builds one expression per table and subject, `(p1 OR p2 …) AND r1 AND r2 …`, and
    returns it as a single filter, so the plugin's own combination never changes the meaning.
- **Column masks are ordered.** Each mask has a priority that is unique within its column. For a
  subject, the matching mask with the highest priority applies, and only that one.
  - Group membership changes at runtime, so overlaps cannot be detected statically. A priority
    makes the outcome deterministic without detecting them.
  - A column with masks and no mask matching the subject is returned unmasked. "Everyone except" is
    how a mask is meant to cover the default case.
- **Subjects** are users, teams, identity-provider groups, service accounts and "everyone except
  <subjects>". Trusted identities are never subjects; filters and masks do not apply to them.

### Expressions are a restricted language

Policy expressions are not arbitrary SQL. Baleia parses them with its server-side parser
([0019](0019-sqlingo-editor-sql-intelligence.md)) and accepts only:

- **References:** the target table's own columns, unqualified, with no subqueries, other tables,
  lateral references or `system.*`.
- **Values and operators:** literals, comparison, boolean and arithmetic operators, `IN` with
  literal lists, `CASE`, `COALESCE`.
- **Functions:** an allow-list of deterministic scalar functions, kept in code and reviewed. It
  excludes UDFs, table functions, aggregates, window functions, `current_*`/`now()`, `random()`
  and anything that reads session state.

Then:

- **Masks follow Dremio.** A mask is a user-written expression in the same restricted language as
  filters, one mask applied per column. The UI offers templates that only fill in the expression:
  null, constant, partial (keep the first or last *n* characters) and `sha256`. There is no built-in
  "hash" type and no HMAC secret. The UI warns that an unsalted hash of low-cardinality values
  (CPF, e-mail) can be reversed. (Dremio attaches UDFs with `SET MASKING POLICY`; its "Hash" type
  exists only through its Ranger integration.)
- **Type checks run in the engine.** For a row filter, the expression must be `boolean`. For a mask,
  its type must equal the column's type. The check is
  `EXPLAIN SELECT typeof(<expr>) FROM <table> WHERE false`, run as the admin identity.
- **Invalid drafts stay drafts.** A draft that fails parsing, the allow-list or the type check
  cannot be published (`data_policy_invalid`). Two masks with the same priority on one column are
  refused at publish too: OPA cannot resolve them and Trino then fails every read of the table, not
  only of that column.

### Views

Trino 484 evaluates a `SECURITY INVOKER` view as the caller and a `SECURITY DEFINER` view as its
owner. It does not give OPA the invoker of a DEFINER view (`StatementAnalyzer.analyzeView` builds
the session with the owner's identity, and the plugin input carries no original identity), and the
core is not changed for it. Therefore:

- **An INVOKER view applies the caller's filters and masks.**
- **A DEFINER view applies its owner's.** A trusted owner is unfiltered, so publishing a policy is
  refused while a trusted-owned DEFINER view reads the policied table; the refusal lists those
  views, to be recreated as INVOKER or with an ordinary owner. `/governance` lists, for each
  policied table, the DEFINER views over it and whose policy they apply.
- **Today the catalog guard denies DEFINER views of ordinary owners.** The owner's identity carries
  no `baleia.catalogs`, so the guard's fail-closed scope (0018) refuses it, with or without OPA (the
  spike's finding). The guard changes to accept a DEFINER view owner's identity; that change has
  its own issue and security analysis in the plugin repository.

### Screens: `/governance`

Policies live in one top-level area, `/governance`, integrated with catalog RBAC:

- **Views:** By object, By subject, Realm, Revisions.
- **By subject** shows, for one subject, what it can read (catalog RBAC), what is filtered (row
  policies) and what is masked (column policies).
- The Polaris RBAC screens of #993 live here too, not under `/connections/{id}/rbac`.
- `/data`, `/catalogs` and `/engines` only link into it; OPA and bundle health may also show on
  `/engines/{id}`.

### Subjects and their freshness

- **Baleia builds the subject map with the same function as queries.** It maps each Trino identity,
  produced by `trinoidentity.For` with the deployment's `BALEIA_TRINO_USER_IDENTITY`, to its teams,
  identity-provider groups and kind. The policy system has no second implementation of the
  identity mapping.
- **Each attribute has a source, and its freshness is stated:**

  | Attribute | Source | Reaches the bundle |
  |---|---|---|
  | Teams, service accounts, user status | Baleia | At the next bundle build after the change |
  | Identity-provider groups | The user's latest sign-in or token refresh (the same source [0016](0016-access-layers-and-workspace-roles.md) uses for group roles) | Only after that user signs in or refreshes again |

  A group removed in the identity provider is not seen until then. The UI says this, and the
  documentation does not claim a fixed revocation time.
- **Suspension takes effect at once.** A suspended or deleted user is removed from the map and from
  Baleia sessions immediately ([0016](0016-access-layers-and-workspace-roles.md)), so they run no
  new statements whatever the bundle says.

### Publication: immutable revisions in object storage

- **Each revision is built once.** Publishing builds the data bundle (policies and subject map),
  signs it with a keyring-held key and writes it to object storage under its revision id. It is
  never rewritten. One transaction then moves the published-revision pointer.
- **Every replica serves the same bytes.** `GET /opa/bundles/baleia.tar.gz` reads the pointer and
  streams that object, with `ETag` = revision id. No replica builds or caches a bundle locally.
- **Subject changes create revisions too.** A debounced job builds a new revision from the current
  published policies whenever subjects change (team membership, sign-in group changes).
- **Three states are shown separately.** OPA's status plugin reports to `POST /opa/status`.
  Baleia shows **draft** (saved), **published** (the pointer) and **active** (the revision each
  OPA reports), per engine. A policy is never shown as active because it was saved or published.
- **Every revision has a lease.** The signed bundle carries `not_after`; past it, the Rego treats
  the bundle as inactive and denies user identities. OPA keeps its last bundle while it cannot
  reach Baleia, and cannot know that a newer revision exists, so without a lease a revoked policy
  could outlive its revocation indefinitely. The lease bounds that.
  - Baleia re-issues a revision with the same policies and a fresh lease before the current one
    ends. The re-issued revision is a new immutable object; nothing is rewritten.
  - The lease length is a deployment setting (its name and default are #995's); it is the longest a
    revocation can take to apply while OPA cannot reach Baleia, and the longest OPA keeps serving
    users while Baleia is down.
- **Rollback** re-issues an earlier revision's content with a fresh lease and moves the pointer to
  it. The earlier revision stays stored and immutable.

### Availability

- **Readiness.** The OPA sidecar's readiness probe is `GET /health?bundles=true`. The coordinator
  pod is not ready, and the Trino Service sends it no traffic, until a bundle is activated. Trino
  therefore never takes user queries with incomplete policy configuration. An external Trino's
  operator must provide the same gate.
- **Fail closed, no bypass.** If OPA is unreachable, Trino's checks fail and so do queries. This
  includes Baleia's maintenance, probes and reconciliation on that engine, because trusted
  identities also go through the plugin. No fallback skips OPA.
- **Recovery does not depend on Trino.** It needs Baleia's bundle endpoint and object storage, and
  neither runs SQL through Trino. With `persist: true`, a restarted OPA comes up with its last
  bundle even when Baleia is down, and serves users until that bundle's lease ends; trusted
  identities go on after it, so maintenance keeps running.

### Who may administer

Data policies are on the admin plane ([0018](0018-workspace-catalog-scope.md)): the owner of the
catalog's connection or a global admin (`allowed_actions`: `manage_data_policies`). Every publish,
rollback and failed publish is audited with the actor, the revision and the affected targets.

## Consequences

- Row filters and column masks apply to every statement Trino runs: editor, retrievers, agents and
  the BI gateway. Baleia rewrites no SQL.
- They protect only the Trino path. Their value depends on the deployment closing direct access to
  the catalog manager and storage for restricted tables.
- Turning policies on makes OPA a hard dependency of each engine that has them, maintenance
  included. It is opt-in per deployment and per engine for that reason.
- Baleia stores policy definitions and builds a projection of user attributes. This is deliberate:
  OPA cannot be the durable store. It does not duplicate the catalog manager, which never held row
  or column rules.
- Group revocation is only as fresh as a user's last sign-in. Deployments that need faster
  revocation must suspend the user in Baleia.
- The expression allow-list is security-relevant code. Adding a function needs review.
- The Rego and the Trino plugin contract move together. A Trino upgrade that changes the OPA input
  is a plugin release.
- **Changing security configuration takes a redeploy.** Making the security ConfigMaps immutable
  also hardens the existing guard and file-rule configuration. In exchange, editing them in place
  stops working: the chart recreates them, and the pod restarts.
- **Cluster RBAC is part of the guarantee.** A deployment that lets broad identities write to the
  Trino namespace has no data-policy guarantee, and no guard guarantee either.
- **The lease trades availability for revocation.** If Baleia stays unreachable longer than the
  lease, user queries on engines with data policies stop until it is back; a revocation applies at
  the latest when the lease ends.
- **Policied tables are close to read-only for filtered identities** (Trino 484's write rules), and
  their value-bearing metadata tables are closed to them.
- **The bundle JSON is a public contract.** Changing it is a `format_version` bump, released with
  the Rego that reads it; operators with their own Rego follow it.

## Spike results (#994)

Trino 484, `baleia-catalog-guard` 0.10.2 and OPA 1.22.4 (pinned by digest) on the minikube dev
cluster; Polaris 1.9.0 and Keycloak 26.8.2 for the Polaris-OPA mode. The suite is kept:
`go test -tags opa -count=1 -timeout 60m ./tests/opa/` (harness in `deploy/dev/opa-spike` and
`deploy/dev/opa-spike-polaris`) and `opa test` over `deploy/opa/baleia`, which replays the recorded
plugin inputs.

| # | Question | Result |
|---|---|---|
| 1 | Composition | Pass. 17 guard guarantees (catalog DDL admin-only, no impersonation by another login, read-only sessions, fail-closed catalog scope) behave the same with and without OPA. |
| 2 | Contract | Pass. Filters and masks apply to SELECT, joins, aggregates, predicates and time travel, batched and not; writes as in "Writes on policied tables"; side doors denied; conflicting masks fail the table. Inputs of every operation seen are recorded under `testdata/trino484`. |
| 3 | Fail closed | Pass. OPA unreachable: every statement fails, trusted ones too. No bundle, lost storage, unsigned, wrongly signed, tampered, out-of-root and corrupt bundles: user identities denied, or the previous revision kept with the error in the status report. Unknown `format_version`: denied. A stale bundle is enforced only until its lease ends. |
| 4 | Readiness | Pass. The client Service has no endpoint until a bundle is active. |
| 5 | Latency | Single node, p50 per query on policied tables: +20-30 ms over no OPA with the non-batched endpoints (two runs); OPA's handler time 2-11 ms per query; the batched endpoints halve the decisions (9→5, 13→7, 23→11). Measured with console decision logging on, so an upper bound. |
| 6 | Coordinator only | Pass. With splits only on the worker, the worker's OPA served no decision. |
| 7 | Integrity | Pass. A wrong checksum stops the init container; the digest is in the status report; patching a security ConfigMap is refused; OPA's data API is unreachable from the cluster network. |
| 8 | Polaris claims | Pass (see "Scope of the guarantee"). |
| 9 | Views | INVOKER: the caller's policy. Trusted-owned DEFINER: unfiltered. Ordinary-owner DEFINER: denied by the guard (see "Views"). |

**Identities that run SQL unfiltered.** Every user-facing path (editor, saved queries, agents,
semantic layer, retrievers, BI gateway) runs as the user's own identity. The trusted ones:

| Feature | Identity | Reads | With policies |
|---|---|---|---|
| Metadata sync, introspection, connection test, engine ping | service | schemas, tables, columns, functions, `SELECT 1` | metadata only |
| Table health | service | Iceberg `$files`, `$manifests`, `$snapshots`, `$refs`, `$properties` | row counts and file statistics of policied tables reach the monitoring screens; documented |
| Maintenance (`optimize`, `expire_snapshots`, Lens) | service | Iceberg procedures | must stay unfiltered: a filtered rewrite would drop rows |
| Catalog DDL (binder), query cancel and info | admin | `CREATE/DROP CATALOG`, `system.runtime` | no table data |

## Current compliance

Not implemented. #995 implements this ADR; the Rego package and its tests exist in
`deploy/opa/baleia/`, and the spike suite above stays as the regression test.

## Alternatives considered

- **Trino file-based rules generated by Baleia** (`security.config-file` over HTTP with a refresh
  period). Table rules support `filter` and `mask`, and no new component is needed. Rejected as the
  first choice:
  - subjects match only by user or group regex, so Baleia would have to expand every team into user
    lists;
  - one file owns every rule of that access control;
  - there is no activation status.

  **This is the fallback** if the spike rejects OPA.
- **Rego shipped inside the bundle.** Rejected: with no bundle there would be no policy at all, and
  the fail-closed rule would itself be missing.
- **Rego inline in a chart ConfigMap.** Rejected: anyone who edits the chart values or the
  ConfigMap rewrites the policy code, and nothing ties it to a release.
- **Rego as a separately signed OPA bundle.** Rejected: the verification key would live in the same
  pod configuration that an attacker would have to change anyway, so it adds a moving part without
  moving the trust boundary. The plugin checksum gives the same release binding.
- **A standalone OPA Deployment and Service.** Rejected for the chart: a network hop per check,
  failures independent of the coordinator, and no shared readiness. External Trino operators may
  still choose it.
- **Raw Rego authored in Baleia.** Rejected for this version: hard to validate, review and check
  for conflicts.
- **Arbitrary SQL expressions checked by `EXPLAIN` alone.** Rejected: `EXPLAIN` proves only that an
  expression compiles, not that it is safe or that its type fits the column.
- **Filters combined only with `AND`.** Rejected: a user in two groups would see the intersection of
  what each group grants instead of the union.
- **Static detection of conflicting masks.** Rejected: group membership is dynamic, so overlaps
  cannot be known when a policy is published. Priorities make the outcome deterministic instead.
- **Bundles built on demand by each replica.** Rejected: replicas could serve different bytes for
  the same revision.
- **OPA's REST policy API instead of bundles.** Rejected: not durable across restarts, and it gives
  no activation status.
- **Filters and masks in `baleia-catalog-guard`, read from Baleia's database.** Rejected: a database
  lookup per check, and more custom security code in Java to maintain.
- **SQL rewriting in Baleia.** Rejected by [0005](0005-trino-security-model.md).
- **Polaris's external PDP as the only layer.** Rejected: it authorizes whole objects; it does not
  filter rows or mask columns. It is kept as the optional second enforcement point ("Who can read:
  two modes").
- **Bundles without a lease.** Rejected after the spike: OPA keeps its last bundle while Baleia is
  unreachable, so a revocation would have no bound.
- **Masks or filters on metadata tables instead of denying them.** Rejected: masking the bounds of
  `$files` still leaks per-file and per-partition row counts, and the columns change across Trino
  and Iceberg versions.
