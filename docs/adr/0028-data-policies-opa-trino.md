# 0028. Row filters and column masks: authored in Baleia, decided by OPA, enforced by Trino

- Status: Proposed. Not to be accepted before the spike listed under "Current compliance".
- Date: 2026-11-13
- Issues: #994 (spike), #995 (implementation), #992 (chart hardening)
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
  │                          opa.policy.uri = http://localhost:8181/v1/data/baleia/allow
  └── container: opa         Rego from the init volume (read-only)
                             data bundle pulled from Baleia (GET /opa/bundles/baleia.tar.gz)
                             persist: true; readiness GET /health?bundles=true
  ```

  - **No network hop.** Trino calls OPA on `localhost` several times per statement.
  - **They fail together.** The two containers live and die as one pod, so "Trino up, OPA down"
    cannot happen inside it.
  - **One readiness for both.** The pod is ready only when both containers are.
  - **Coordinator only.** Access checks, filters and masks are resolved while the query is planned,
    so workers need no OPA. The spike confirms this.
- **For an external Trino**, the operator installs OPA and the plugin configuration. Baleia
  publishes the Rego package, the plugin properties and the bundle endpoint for them. It does not
  manage that deployment.

### Integrity of the policy code

- **The Rego comes from the plugin release.** It ships in the versioned tarball, under the same
  `.sha256` as the catalog-store plugin ([0004](0004-plugin-distribution.md)). The existing
  `install-plugin` init container verifies the checksum and copies the Rego into a volume that the
  OPA container mounts read-only.
  - The chart contains no Rego text.
  - Changing the policy code means changing a pinned release and checksum, not editing a
    ConfigMap.
- **Security ConfigMaps are immutable.** Every ConfigMap that carries Trino security configuration
  is created with `immutable: true`: `config.properties`, the base rules, the guard properties,
  `baleia-rules.json`, the authenticator, and OPA's configuration. A change needs a delete and a
  recreate, which takes a separate permission and shows in the cluster audit log. This applies to
  the existing files too, not only to OPA's.
- **Drift is detected.** The Rego package exposes its own digest (`data.baleia.policy_digest`), and
  OPA's status report carries it to Baleia. Baleia shows the engine as **enabled and unhealthy**
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
  (`deploy/opa/baleia/`) is versioned with the Trino plugin and tested in CI with `opa test` against
  the plugin's input documents. OPA loads it from the verified init volume (see "Integrity of the
  policy code"), **not from the bundle**. The bundle carries only data, under the root `baleia`.
  The policy logic is therefore always present, even when no bundle has ever been activated.
- **OPA is no second RBAC, but it denies when it has no data.** The `allow` rule is:
  - **true** for trusted identities (`baleia.admin-users`, `baleia.service-users`);
  - **true** for user identities once a bundle revision is active;
  - **false** for every user identity while `data.baleia.revision` is undefined: no bundle yet,
    an invalid bundle, or lost persistent storage.

  OPA never grants anything another layer denies: catalog and table access stay with the catalog
  guard ([0018](0018-workspace-catalog-scope.md)) and the catalog manager.
- **Filters and masks do not authorize writes.** Writes stay with the guard (read-only sessions)
  and the catalog manager.

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

- **Masks are usually presets.** Most masks pick one of `null`, `constant`, `hash`, `partial` (keep
  the first or last *n* characters) or `redact`, which generate a known-safe expression. A custom
  expression in the restricted language is allowed for advanced cases.
- **Type checks run in the engine.** For a row filter, the expression must be `boolean`. For a mask,
  its type must equal the column's type. The check is
  `EXPLAIN SELECT typeof(<expr>) FROM <table> WHERE false`, run as the admin identity.
- **Invalid drafts stay drafts.** A draft that fails parsing, the allow-list or the type check
  cannot be published (`data_policy_invalid`).

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
- **Rollback** moves the pointer to an earlier revision, which is immutable and still stored.

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
  bundle even when Baleia is down.

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

## Current compliance

Not implemented, and **not to be accepted** until a spike against Trino 484, the current
`baleia-catalog-guard` and a pinned OPA version shows all of the following, with automated tests:

1. **Composition.** `opa`, `file` and `baleia-catalog-guard` together in `access-control.config-files`
   keep every guarantee of [0005](0005-trino-security-model.md) and
   [0018](0018-workspace-catalog-scope.md): the existing guard test suite passes unchanged with OPA
   added.
2. **The real contract.** The plugin's input and output for allow, row filters and masks (batched
   and not). What happens when a column gets more than one mask. How row filters behave on
   `UPDATE`, `DELETE` and `MERGE`. The documented list of covered operations comes from this.
3. **Fail-closed cases.**
   - OPA unreachable;
   - OPA up with no bundle;
   - an invalid or unsigned bundle;
   - lost persistent storage;
   - a bundle older than the published revision.

   Each must deny or error for user identities, never return "no filter" or "no mask".
4. **Readiness.** The coordinator is not routable before activation.
5. **Latency.** Per query, with the batched endpoints and OPA as a sidecar.
6. **Coordinator only.** No access-control call reaches OPA from a worker.
7. **Integrity.**
   - A Rego file whose checksum does not match stops the init container.
   - A digest mismatch shows the engine as unhealthy.
   - The security ConfigMaps are created as immutable.

If the spike fails on 2 or 3, the fallback below is reconsidered before any implementation starts.

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
- **Polaris's external PDP.** Rejected: it authorizes catalog operations; it does not filter rows
  or mask columns.
