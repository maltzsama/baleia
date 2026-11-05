# Design: workspace-scoped catalog access (v3.2)

Status: **Draft for owner review.** No code is changed by this document.
Related: ADR 0002 (catalogs and bindings), ADR 0005 (Trino security), ADR 0015 (per-user catalog
identity), ADR 0016 (access layers), #16, #653, #654.

v2 changes from v1, all verified against `main` (7bffb3e) and `baleia-trino-catalog-store`:
the guard rule no longer keys on `u_<uuid>`; the scope is per engine; the extra-credential
encoding is fixed; coverage is enforced structurally instead of by a call-site list; the
personal auto-reach rule is removed; and the grant flow, API surface and screens are part of the
design instead of a non-goal, because they are the same problem seen from the UI.

v3: decisions settled (§9); grant flow is two-party with either side starting (§7.3, replaces v2's
owner-push); Trino facts verified against the 484 source and the plugin at `8d9f075` (§2.5, §5.1,
§5.4); `e2e_tpch` corrected (§2.5); #654 folded in (§4.4).
v3.2: everything ships in one release, 0.6.2 (§10); pending grants live in their own table so the
migration stays safe during the rolling update (§4.4); BI goes through Baleia or not at all (§9.5).

---

## 1. The problem

1. **Engine hole.** A query submitted from a workspace can reference any catalog the Trino
   coordinator hosts. The workspace → catalog grant (ADR 0016, layer 1) picks the query target
   but is not enforced on the statement.
2. **API hole.** Outside `/workspaces/{id}/…`, catalog and engine reads are authorized per
   *user*: a member of any workspace granted a catalog may read it through `/catalogs/{id}/…`,
   `/engines/{id}/…` and the legacy explorer, from no workspace at all.
3. **Model and UX.** "Which catalog is in which workspace" has two answers (grants plus a hidden
   personal auto-reach rule), the rule is copied into ten SQL queries that disagree, and granting
   needs one person who is both workspace admin and connection owner, so the two grant screens fail
   for almost everyone who sees them.

All three come from one missing concept: **the workspace catalog scope**, defined once and used by
the engine, the API and the UI.

## 2. As-is (verified)

### 2.1 Where the scope is computed

The "catalogs a workspace may use" rule is a CTE copied into:
`ListWorkspaceTargets`, `GetGrantedCatalogByName`, `ListMetadataCatalogs`,
`GetGrantedMetadataCatalog`, `GetGrantedMetadataTable`, `SearchMetadata`, `SearchSemanticDocs`,
`ListSemanticTags`, `ListVisibleSemanticModels`, `ListPopularTables`. Two queries disagree
with it: `ListRetrieverDestinations` (`retrievers.sql`) and `ListGrantedCatalogsOfWorkspace`
read grants only and ignore the personal rule.

### 2.2 Personal auto-reach is wider than it looks

`w.type = 'personal' AND cc.created_by = w.owner_user_id` makes **every** catalog of **every**
connection the owner creates, now or later, reachable from **every** personal workspace they own.
Since ADR 0016 a personal workspace can be shared (`workspace_members`), so its members inherit
that reach too. For a catalog read through the connection's account (no per-user catalog manager),
sharing a personal workspace silently delegates the owner's connection credentials. None of this is
visible in the workspace's Catalogs tab, which lists explicit grants only.

### 2.3 User-global checks (the API hole)

`authz.Catalog` / `authz.Engine` with `ActRead` fall back to `CatalogGrantedToUser` /
`EngineHostsCatalogGrantedToUser` ("granted to any workspace the user belongs to"). Same idea in
`ListVisibleCatalogs`, `ListCatalogsGrantedToUser`, `ListEnginesVisibleToUser`. Routes that rely on
it for non-owners: `GET /catalogs`, `/catalogs/{id}`, table health and maintenance reads,
`/engines/{id}…` (including the BI file and monitoring), and the legacy explorer
`/connections/{id}/catalogs/{name}/…`, which is mounted outside the OpenAPI contract
(`server.go:266`) and runs `SHOW …` live on the default Trino as the user.

The workspace metadata routes (`/workspaces/{id}/metadata/…`) are already workspace-scoped
(v1 §5.3 was stale on this).

### 2.4 Grant flow

`POST /workspaces/{id}/catalog-access` requires `Workspace(ActAdmin)` **and**
`Catalog(ActAdmin)` from the same caller. "Catalog admin" is the connection's `created_by` or a
global admin; system-owned connections (bundled Trino, `system_owned`) have no owner, so only a
global admin can grant their catalogs. In practice: platform team owns connections, product teams
own workspaces, nobody holds both keys except the global admin.

The same relationship has two screens: workspace settings → Catalogs and catalog → Access.
The workspace screen shows Grant/Revoke without checking `allowed_actions`.

### 2.5 Engine side

- User SQL runs as the identity from `trinoidentity.For`: `u_<uuid>` by default, **the user's
  e-mail** when `BALEIA_TRINO_USER_IDENTITY=email`.
- The guard (`BaleiaCatalogGuard`, plugin `8d9f075`, SPI 484) returns `true` from
  `canAccessCatalog` (L608), the input from `filterCatalogs` (L626) and every other `filter*`, and
  allows `checkCanViewQueryOwnedBy` (L523), `checkCanKillQueryOwnedBy` (L528) and
  `filterViewQueryOwnedBy` (L662) for everyone: any user can read every other user's SQL text
  through `system.runtime.queries`.
- `checkCanImpersonateUser` (L89) / `checkCanSetUser` protect `baleia.admin-users` only, not the
  service identity. The factory reads only `baleia.admin-users` and
  `baleia.allow-unauthenticated-admin`.
- In Trino 484 `canAccessCatalog` is the gate for reads: `AccessControlManager` calls it before
  `checkCanSelectFromColumns` (L1133) and `checkCanExecuteProcedure` (L1278).
- `e2e_tpch` **is** in the model: `cmd/e2e-seed` (`ensureTarget`) creates it as a native catalog on
  the default engine's (system-owned) connection, with a binding, and grants it to the member's and
  the admin's first personal workspaces. v1's "no `catalogs` row, no grant" was wrong. Catalogs
  that exist in Trino without the seed are adopted by reconciliation (`bindings.Lifecycle.adopt`)
  when the engine has a connection, and become grantable by a global admin the same way.

## 3. Goals / non-goals

Goals:

- **G1** A statement reaches exactly the catalogs in the scope of the workspace it runs in.
- **G2** Enforced in Trino (the guard), not by parsing SQL in Baleia.
- **G3** Allow-list: anything not in the scope is denied, modeled or not.
- **G4** Fail closed, on both sides: a user session without a scope reaches nothing, and Baleia
  refuses to open one.
- **G5** Cross-catalog queries inside the scope keep working.
- **G6** Per query, never the union of a user's workspaces.
- **G7** One definition of the scope, used by the engine, the API and the UI.
- **G8** Granting is something the people who own each side can actually do, from one place each.

Non-goals: changing layer 2 (catalog manager decides data) beyond the decision in §9; per-table
or per-schema scope.

## 4. The scope, defined once

### 4.1 Definition

`scope(workspace)` = the catalogs with a row in `workspace_catalog_grants` for that workspace,
enabled. Nothing else. **The personal auto-reach rule is removed** (§4.3).

`engine_scope(workspace, engine)` = the `engine_catalog_name` of every enabled, active binding on
`engine` of a catalog in `scope(workspace)`, on an enabled engine.

The engine scope is per engine on purpose: a catalog granted to W and bound as `sales` on engine A
must not allow an unrelated catalog bound as `sales` on engine B. (v1 sent every binding of every
visible catalog, which over-grants on name collisions.)

### 4.2 One SQL source of truth

```sql
-- The catalogs a workspace may use (ADR 0016 layer 1). The only reader of
-- workspace_catalog_grants outside grant CRUD.
CREATE FUNCTION baleia_workspace_catalog_ids(ws uuid) RETURNS SETOF uuid
LANGUAGE sql STABLE AS $$
    SELECT g.catalog_id
    FROM workspace_catalog_grants g
    JOIN catalogs c ON c.id = g.catalog_id
    WHERE g.workspace_id = ws AND c.enabled
$$;
```

Every query in §2.1 uses `IN (SELECT baleia_workspace_catalog_ids(@workspace_id))`.
`ListWorkspaceTargets` keeps returning disabled rows for diagnostics by joining grants directly,
the one documented exception. An arch test fails when a query names `workspace_catalog_grants`
outside grant CRUD, the function and `ListWorkspaceTargets` (same pattern as the existing
contract tests).

### 4.3 Removing personal auto-reach

- Creating a catalog (connection wizard, adoption by a user-owned engine) creates an active grant
  to the workspace the creator is working in, or their first personal workspace when there is
  none. Explicit, visible, revocable.
- Backfill: an active grant for every (personal workspace, catalog of the owner's connections)
  pair the rule reaches today (`INSERT … ON CONFLICT DO NOTHING`, idempotent), so nobody loses
  access. Shared personal workspaces keep today's reach, but it is now listed and revocable.
- The rule is removed in the same release (§10). The migrate Job is not a Helm hook and runs
  alongside the rolling update, so a 0.6.1 pod can still create a catalog after the backfill ran.
  That catalog is created without a grant: fail closed, and visible to its owner as "not shared
  with any workspace" on the Access screen, one click to share. There is no automatic re-grant
  afterwards, because a repeated backfill could not tell it apart from a catalog its owner
  deliberately stopped sharing.

Result: "which catalog is in which workspace" has one answer, the grants table, and the
Catalogs tab shows all of it.

### 4.4 Schema changes (all safe during the rolling update)

sqlc expands `*` into explicit column lists, so 0.6.1 pods name every column they read. Adding
tables and columns is invisible to them; dropping a column they name breaks their statements.

- `workspace_catalog_grants` keeps its shape and keeps meaning "active grant". Nothing is added to
  it, so `baleia_workspace_catalog_ids` reads it as is.
- New table `catalog_grant_requests` (`workspace_id`, `catalog_id`, `initiated_by_side`
  `catalog`|`workspace`, `requested_by`, `requested_at`, unique on the pair). Approval deletes the
  request and inserts the grant in one transaction.
- New column `catalogs.discoverable boolean NOT NULL DEFAULT false`.
- New function `baleia_workspace_catalog_ids`.
- #654 (`permission`, `default_readonly`) is dropped in the same migration. No 0.6.2 code names
  them; 0.6.1's `UpsertWorkspaceCatalogGrant` does (`RETURNING … permission …`), so a grant
  attempted on a 0.6.1 pod during the rollout fails with an error and succeeds on retry once the
  pod is replaced. Error, not exposure.

## 5. Engine enforcement

### 5.1 Carrying the scope

- `queryengine.SessionSpec` gets `AllowedCatalogs []string` and
  `AllowedCatalogsSource func(context.Context) ([]string, error)` (same shape as
  `CatalogToken`/`CatalogTokenSource`: re-resolved before each statement of a long script, so a
  revocation stops the next statement instead of the next run).
- The Trino session sends it as the extra credential `baleia.catalogs`.
- **Encoding.** `encodeSessionProperties` joins entries with `,` and does not escape values, and
  Trino splits `X-Trino-Extra-Credential` on `,`. A comma-separated catalog list would break the
  header. Fix in `encodeSessionProperties`: percent-encode every value, so the list
  `lake,sales` travels as `baleia.catalogs=lake%2Csales` and the guard splits the decoded value
  on `,`. Verified in Trino 484 (`HttpRequestSessionContextFactory`): the header is split on `,`,
  each entry on `=`, and the value goes through `URLDecoder.decode` (L351–358, L472–475). The same
  fix removes a latent bug for any existing value containing `,` or `=` (padded base64).
  Unit test for a multi-name value, and an e2e query that crosses two granted catalogs.

### 5.2 Coverage by construction, not by call-site list

v1 listed call sites; a missed one is a leak. Instead the invariant lives in `queryengine`:

> A `SessionSpec` whose `Identity.UserID` is set (any user, agent or service-account identity)
> must carry `AllowedCatalogs` or `AllowedCatalogsSource`. `OpenSession` fails with
> `catalog_scope_missing` otherwise. A spec with no user identity runs as the service identity.

Plus an arch test: no package outside `queryengine` sets `Identity.UserID` without going through a
helper that takes the workspace (`execscope.For(ctx, workspaceID, engineID)`).

Paths that today open a user-identity session, and the workspace each one uses:

| Path | Where | Workspace |
|---|---|---|
| Script execution | `temporal/activities/execute_script.go` `openSession` | the execution's; resolved in the activity, not at submit |
| Cost routing EXPLAIN | `workbench/executions` `costHeavy` | the run's |
| Semantic validation / `custom_sql` | `handlers_semantic.go` | the request's |
| Retriever refresh | `temporal/activities/retriever_activities.go` (owner identity) | the retriever's |
| Agents / MCP | `handlers_query_editor.go` (agent identity) | the bound workspace of the call |
| Legacy explorer | `handlers_catalog_connections.go` `exploreQuery` | none: route removed (§7.2) |

Service-identity paths (metadata sync, maintenance, Lens, probes) are unchanged. The table is for
review; the invariant is what guarantees coverage.

### 5.3 Guard changes (`baleia-trino-catalog-store`)

- **Trusted identities** = `baleia.admin-users` ∪ new `baleia.service-users`. The rule never
  pattern-matches `u_<uuid>`: with `BALEIA_TRINO_USER_IDENTITY=email` user identities are e-mails.
  **Every identity that is not trusted is a user.**
- `canAccessCatalog(ctx, catalog)`: trusted → `true`; user → `catalog ∈ baleia.catalogs`
  (missing credential → `false`).
- `filterCatalogs`: trusted → input; user → input ∩ `baleia.catalogs` (missing → empty).
- `system`: never in a user scope, so denied by the rule above. Baleia reads `system.*` only as a
  trusted identity.
- `checkCanViewQueryOwnedBy` / `filterViewQueryOwnedBy` / `checkCanKillQueryOwnedBy`: a user sees
  and kills only queries whose owner is themselves; trusted sees all.
- `checkCanImpersonateUser` / `checkCanSetUser`: protect every trusted identity, not only admins
  (a user impersonating the service identity would escape the scope).
- `BaleiaCatalogGuardTest.everySpiCheckIsOverridden` keeps passing; new tests per rule.

### 5.4 Views

Trino checks a DEFINER view's underlying tables as the view **owner**, and that identity carries
no extra credentials. Verified in Trino 484: `MetadataManager.getView` builds it with
`Identity.ofUser(owner)` (L1649–1651) and `StatementAnalyzer.analyzeView` uses
`Identity.from(owner).withGroups(…)` (L5730), even when the owner is the user running the query.
Consequences (the e2e test pins them, it no longer has to discover them):

- A DEFINER view owned by a user denies its underlying tables (owner has no scope), including for
  the owner. Acceptable; Baleia-created views use `SECURITY INVOKER`, and the failure is a clear
  permission error.
- A DEFINER view owned by a trusted identity exposes its underlying catalogs to whoever reaches the
  view's catalog. That is DEFINER semantics (deliberate delegation) and is documented, not fixed.

### 5.5 Data flow

```
Browser (workspace W, target T on engine E)
  → POST /workspaces/W/executions {sql}
    → authz: member of W
    → target := ResolveTarget(W) ; T ∈ engine_scope(W, E) else target_unavailable
    → enqueue execution (workspace W, target T)
      → activity: spec := execscope.For(W, E) + identity
          AllowedCatalogsSource re-reads engine_scope(W, E) before each statement
        → Trino: X-Trino-User: <user identity>
                 X-Trino-Extra-Credential: baleia.catalogs=lake%2Csales
          → guard.canAccessCatalog(c): c ∈ scope → allow ; else deny
```

## 6. Fail-closed rules and edge cases

- User session without scope: Baleia refuses to open it (§5.2); if one reaches Trino anyway, the
  guard denies every catalog.
- Empty scope: target resolution already fails; at the engine it reaches nothing.
- Revocation: takes effect at the next statement (§5.1). A statement already running finishes.
- Disabled catalog/binding/engine: not in the scope at the next statement.
- Direct engines: one database, no cross-catalog reach; the scope is sent but meaningless, no
  change.
- **Trino has one client: Baleia.** Catalogs are also reached directly by ingestion engines
  (Spark, Flink) through the catalog manager, never through Trino. Any identity that reaches Trino
  without coming through Baleia carries no `baleia.catalogs` and is not trusted, so the guard gives
  it no catalog: the invariant is enforced by the engine, not only by deployment. Today's BI file
  (`/engines/{id}/bi`, #560) violates it (it points the tool at the engine's `external_url`) and is
  removed (§9.5).
- **Cancel and monitoring** are unaffected by own-queries-only (§5.3): Baleia cancels with the
  admin client (`queryengine/trino/engine.go:152`, `system.runtime.kill_query`), and monitoring
  reads snapshots Baleia stores and filters by identity itself. Trino also never asks the access
  control when the viewer or killer is the query's owner (`AccessControlUtil`, L31 and L57).

## 7. API

### 7.1 Two planes

- **Admin plane**: `/connections`, `/catalogs`, `/engines`, `/bindings` and everything under
  them. Connection owner or global admin only. A non-owner gets 404, as today for non-granted
  users. `CatalogGrantedToUser`, `EngineHostsCatalogGrantedToUser`, `ListCatalogsGrantedToUser`,
  `ListEnginesVisibleToUser` and the `ListVisibleCatalogs` grant branch are deleted.
- **Data plane**: everything a member does is under `/workspaces/{id}/…` and checked against
  `scope(id)`. What members need from the admin plane today moves there:
  - `GET /workspaces/{id}/catalogs/{catalog_id}/table-health`
  - monitoring of the user's own queries keeps filtering by identity (unchanged).

### 7.2 One listing of a workspace's catalogs

Today four endpoints answer "catalogs of a workspace" with different shapes and rules:
`/targets`, `/catalog-access`, `/metadata/catalogs`, and the legacy tree behind
`ListWorkspaceCatalogs`. Replace with:

`GET /workspaces/{id}/catalogs` → for each catalog in the scope: id, name, connection, origin
(`managed`/`adopted`/`native`), granted by/at, bindings per engine with status, metadata freshness,
and `allowed_actions` (`unlink`, `set_default_target`, `query`).

`/targets` and `/metadata/catalogs` become views of it or are removed; the legacy explorer routes
(`/connections/{id}/catalogs/{name}/…`, outside OpenAPI) are removed — the metadata explorer
already covers them.

### 7.3 Granting: two parties, either one starts

A grant becomes active only when both sides agree, as today, but the two agreements can come
from two people:

| Action | Who | Result |
|---|---|---|
| Offer a catalog to a workspace | catalog owner or global admin | `pending`, waits for a workspace admin |
| Request a catalog for the workspace | workspace admin | `pending`, waits for a catalog owner |
| Approve the pending side | the other party | `active` |
| Grant directly | someone who holds both rights (or global admin) | `active` at once (today's path) |
| Decline, revoke or unlink | either party | row removed, effective at the next statement |

Endpoints, one resource for both sides:

- `GET /catalog-grants?workspace_id=…|catalog_id=…&state=active|pending`: grants and pending requests (`catalog_grant_requests`), filtered to what the caller manages.
- `POST /catalog-grants {workspace_id, catalog_id}`: offer, request or direct grant, decided by
  the caller's rights on each side.
- `POST /catalog-grants/{workspace_id}/{catalog_id}/approve`
- `DELETE /catalog-grants/{workspace_id}/{catalog_id}`

Pickers:

- Catalog owner: `GET /catalogs/{catalog_id}/grantable-workspaces` (workspace name and team only).
- Workspace admin: `GET /workspaces/{id}/requestable-catalogs`: name, description, connection
  name and owner of catalogs whose owner marked them `discoverable` (new flag, default off: a
  catalog name such as `hr_salaries` is itself information). Offers work for any catalog.

Pending items (`catalog_grant_requests`) go to the existing attention list for whoever can
approve them.

Why consent on both sides: the workspace admin's membership decisions are made for the catalogs
the workspace has. A catalog pushed in silently changes what every existing member, service account
and agent can do; under ADR 0016 an editor's agent may write (DDL/DML), so a pushed catalog read
through a read/write connection account becomes writable by agents nobody re-reviewed. The current
problem is not the two keys, it is that one person must hold both.

`POST /workspaces/{id}/catalog-access` and `DELETE …/catalog-access/{catalog_id}` stay for one
release as aliases of the direct-grant and revoke paths, then are removed.

Every grant change clears `catalogCache`, writes an audit event naming both sides, and (with §5.1)
takes effect at the next statement.

### 7.4 Catalog ownership

"Catalog admin" = `catalog_connections.created_by`: one person, and nobody for system-owned
connections. Out of scope here, but the grant endpoints should call a single
`authz.CatalogOwner` so that adding team ownership of connections later changes one function.

## 8. Screens

- **Workspace settings → Catalogs.** Lists the whole scope (§7.2): name, connection, engines (with
  engine-side names), granted by/at. Below it, **Pending**: offers waiting for this workspace
  (Approve / Decline) and its own requests (Withdraw). Workspace admins get **Request a catalog**
  (requestable-catalogs picker) and **Unlink**. Empty state: "No catalog is shared with this
  workspace yet. Request one, or ask a catalog owner to offer it."
- **Catalog → Access.** "Workspaces with access": workspace, team, member count, granted by/at,
  **Revoke**. **Pending**: requests from workspaces (Approve / Decline) and its own offers
  (Withdraw). **Offer to workspace** opens the grantable-workspaces picker. A **Discoverable**
  toggle controls whether workspace admins can find it.
- Someone with both rights sees **Add** on either screen and the grant is active at once.
- **Connection wizard.** Final step: "Share with" preselected to the current workspace (this
  replaces the hidden personal rule).
- **Every button follows `allowed_actions`** from the API (the workspace Catalogs tab ignores them
  today).
- **Editor.** A permission error naming a catalog outside the scope maps to a clear message:
  "`e2e_tpch` is not shared with this workspace" with a link to the Catalogs tab.

## 9. Decisions

1. **Role vs data: A for this delivery.** Layer 2 stays with the catalog manager. A catalog read
   through the connection's account is all-or-nothing (read and write) for the members of the
   workspaces it is shared with; `credentials_read_only` on the connection is the way to make it
   read-only. B (viewer → read-only session) is a separate issue: on a direct engine that cannot
   enforce read-only, a read-only session fails with `read_only_unsupported` unless the connection
   asserts read-only credentials (`SessionSpec.ReadOnly`), so B would break viewers there and
   needs its own design. It is also an existing inconsistency worth fixing: ADR 0016 already
   makes a viewer's agent read-only but not the viewer.
   For the demo, `e2e_tpch` uses Trino's `tpch` connector, which has no write support, so A adds
   no write risk there.
2. **`system` for user statements: denied**, and user identities see and kill only their own
   queries (§5.3). Cancel and monitoring are unaffected (§6).
3. **Personal auto-reach: removed**, with the backfill (§4.3).
4. **Grant flow: two parties, either one starts, active when both agree (§7.3).** A direct grant
   remains for someone who holds both rights.
5. **BI: through Baleia or not at all.** Trino's only client is Baleia (§6). The BI file of #560,
   which sends the tool straight to the engine's `external_url`, is a bypass and is removed in
   0.6.2: the endpoint, the UI entry and `engines.external_url` as a client-facing address. BI
   comes back as Baleia acting as the BI tool's endpoint: it authenticates a Baleia credential,
   resolves the workspace, sets the user identity and `baleia.catalogs`, and forwards to Trino.
   That reverses "no proxy" in ADR 0001 and the project's earlier decisions (no JDBC, no Flight
   SQL proxy), so it gets its own ADR; it is not needed to close the hole, removing the bypass is.
6. **Releases:** §10.

## 10. Rollout

**One release: 0.6.2, with the plugin release it requires.** The confusing grant model and
screens are part of the hole (an access model nobody can operate correctly is misconfigured by
construction), so the engine scope, the API planes, the grant flow, the screens and the removal of
the personal rule ship together. Nothing here is a feature.

Contents:

- **Plugin:** trusted identities (`baleia.admin-users` ∪ `baleia.service-users`), scope check in
  `canAccessCatalog` / `filterCatalogs`, `system` denied to users, own-queries-only visibility,
  impersonation of any trusted identity denied. Enforcement is on; no flag.
- **Migration** (§4.4): additive changes, backfill of the personal-rule pairs, #654 drop.
- **Engine:** per-engine scope as `baleia.catalogs`, re-resolved per statement; `OpenSession`
  refuses a user-identity spec without a scope; extra-credential encoding fix.
- **API:** admin/data planes (§7.1), `/workspaces/{id}/catalogs` (§7.2), `/catalog-grants` with
  the two-party flow (§7.3), table health under the workspace; removed: the "granted in any
  workspace" fallbacks, the legacy explorer, `catalog-access`, the BI file.
- **Screens** (§8), with every button on `allowed_actions`.

Rolling update, what happens in the window (minutes):

- 0.6.1 API pods send no scope → their queries are denied by the new guard. Fail closed; retries
  land on 0.6.2 pods.
- 0.6.1 pods can still create a catalog without a grant (§4.3) or fail a grant write (§4.4).
  Both are less access, never more.
- External Trino: the API logs a startup error when the guard is older than the scope-aware
  version; `BALEIA_REQUIRE_CATALOG_SCOPE=true` makes it refuse user queries instead.

Release notes: security advisory; a statement reaches only catalogs shared with its workspace;
member reads on `/catalogs/*` and `/engines/*` are gone; grants need both sides; personal
workspaces show their catalogs as explicit grants; the BI file is removed.

## 11. Tests

Baleia:

- `baleia_workspace_catalog_ids`: grants only; disabled catalog excluded; arch test on query
  usage.
- Migration: every personal-rule pair gets a grant; nothing else changes reach.
- `engine_scope` per engine: name collision across engines is not allowed through.
- `OpenSession` with user identity and no scope → `catalog_scope_missing`.
- Encoding: multi-catalog value round-trips.
- Revoke between two statements of one script → second statement denied.
- Admin plane: a member of a granted workspace gets 404 on `/catalogs/{id}`, table health,
  engine routes.
- Grant flow: offer → approve and request → approve reach `active`; one side alone stays
  `pending` and is not in the scope; a caller with both rights is `active` at once; either side
  revokes; `requestable-catalogs` hides non-discoverable catalogs; `allowed_actions` match.
- Migration: backfill keeps every current reach as an explicit grant; after it, a member of a
  shared personal workspace reaches only what the grants list; adding the new table and column
  leaves 0.6.1 queries working.
- BI file endpoint is gone; a direct Trino login with a user identity reaches no catalog.

Guard:

- Trusted vs user by configuration, with `u_<uuid>` and e-mail identities.
- Missing credential → no catalog, empty `filterCatalogs`.
- `system` denied for users; own-queries-only visibility; impersonation of the service identity
  denied.

End to end (minikube):

- A team workspace W shared `lake` only (not one of the personal workspaces the seed grants
  `e2e_tpch` to): `SELECT … FROM e2e_tpch.sf1.orders` → permission error;
  `SELECT … FROM lake…` works; a join of two shared catalogs works.
- Same user, workspace V shared `e2e_tpch`: the same statement works from V and not from W.
- DEFINER views per §5.4: user-owned denies its underlying catalogs even for the owner;
  trusted-owned reaches them.
- The seed already grants `e2e_tpch` to the member's and admin's personal workspaces; the smoke
  suite keeps working unchanged.

## 12. Alternatives considered

- **Baleia-side SQL parsing** as the boundary: rejected (ADR 0005); views, functions and
  `EXECUTE IMMEDIATE` bypass it. Stays as UX only.
- **Per-user allow-list** (union of grants): rejected, over-grants across workspaces.
- **Per-workspace Trino identity** (`u_<uuid>@ws_<id>`): works, but the guard would have to resolve
  workspace → scope (a DB call per check) and Trino attribution gets noisier. The extra credential
  carries the same information statelessly.
- **One catalog per session**: breaks cross-catalog queries inside the scope.
- **Keep personal auto-reach, fix it per workspace**: still invisible in the UI and still widens on
  every new connection and on sharing a personal workspace.
- **Owner pushes without the workspace's consent** (v2): simpler, but silently changes what a
  workspace's members, service accounts and write-capable agents can do (§7.3).
- **Keep the two-key grant held by one person**: keeps the screens failing for anyone who is not
  a global admin.

## 13. Risks

- **External Trino on an old guard**: the scope is sent but not enforced until the plugin is
  upgraded; the startup check makes it visible, `BALEIA_REQUIRE_CATALOG_SCOPE` makes it fail closed.
- **Behavior change for existing users**: queries that relied on catalogs not shared with the
  workspace start failing. Intended; the editor message points to the fix.
- **BI users** lose the direct path until Baleia serves BI itself (§9.5).
- **DEFINER views** owned by users stop working (§5.4).
- **Migration size**: the backfill can create many grants for users with many personal workspaces;
  they are all visible and revocable, which is the point.