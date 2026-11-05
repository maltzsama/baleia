# 0018. Workspace-scoped catalog access

- Status: Proposed. Supersedes the "workspace → catalog" layer and the grant flow of
  [0016](0016-access-layers-and-workspace-roles.md); extends
  [0005](0005-trino-security-model.md).
- Date: 2026-11-05
- Issues: #700 (epic); #701 (guard), #702 (scope/migration), #703 (engine), #704 (api+ui)

## Context

The workspace → catalog grant (0016 layer 1) picks the query *target* but is not enforced on the
statement. Three holes follow from that one gap:

1. **Engine hole.** A statement can reference any catalog the Trino coordinator hosts; the engine
   runs it with a per-user identity and no read-side catalog restriction. `system.runtime.queries`
   also leaks every user's SQL text (the guard allows `checkCanViewQueryOwnedBy` for everyone).
2. **API hole.** Outside `/workspaces/{id}/…`, catalog and engine reads are authorized per *user*
   (`CatalogGrantedToUser` / `EngineHostsCatalogGrantedToUser`), so a member of any workspace
   granted a catalog can read it from no workspace at all.
3. **Model and UX.** "Which catalog is in which workspace" has two answers (grants plus a hidden
   personal auto-reach rule), the rule is copied into ten SQL queries that disagree, and granting
   needs one person who is both workspace admin and connection owner.

The deeper reason the holes exist: the grant and the role are per workspace, but the engine
identity is per user, so the engine cannot tell which workspace a query was submitted in.

## Decision

### The scope, defined once

- `scope(workspace)` = the enabled catalogs with a row in `workspace_catalog_grants` for that
  workspace. Nothing else; the personal auto-reach rule is removed.
- `engine_scope(workspace, engine)` = the `engine_catalog_name` of every enabled, active binding
  on `engine` of a catalog in `scope(workspace)`, on an enabled engine. Per engine on purpose, so a
  name collision across engines cannot widen reach.
- One SQL source of truth: `baleia_workspace_catalog_ids(ws uuid)` is the only reader of
  `workspace_catalog_grants` outside grant CRUD; every scope query uses it. An arch test fails
  when a query names the grants table elsewhere.

### Removing personal auto-reach

- A personal workspace no longer auto-reaches its owner's connections. Catalog creation (wizard,
  adoption) creates an active grant to the creator's current workspace, explicit and revocable.
- Migration backfills an active grant for every (personal workspace, catalog of the owner's
  connections) pair the rule reached, idempotently, so nobody loses access on upgrade.
- A catalog a 0.6.1 pod creates during the rolling update is created **without** a grant: fail
  closed, visible as "not shared", one click to share. No automatic re-grant afterwards.

### Grant table and requests

- `workspace_catalog_grants` keeps meaning "active grant". Pending requests live in a new
  `catalog_grant_requests` table (`workspace_id`, `catalog_id`, `initiated_by_side`, `requested_by`,
  `requested_at`, unique on the pair); approval deletes the request and inserts the grant in one
  transaction. `catalogs.discoverable` (default false) lets a workspace admin find a catalog to
  request. #654 (`permission`, `default_readonly`) is dropped in the same migration.

### Engine enforcement (the guard)

- Baleia carries the scope to Trino as the extra credential `baleia.catalogs`
  (`engine_scope(workspace, engine)`), re-resolved before each statement, percent-encoded.
- `queryengine.OpenSession` refuses a user-identity `SessionSpec` without a scope
  (`catalog_scope_missing`); a spec with no user identity runs as the service identity. An arch
  test enforces that only `execscope.For(ctx, workspaceID, engineID)` sets `Identity.UserID`.
- The guard: trusted identities = `baleia.admin-users` ∪ `baleia.service-users`; everything else
  is a user. `canAccessCatalog` / `filterCatalogs` allow only `baleia.catalogs` for a user;
  `system` is never in a user scope. A user sees and kills only their own queries;
  `checkCanImpersonateUser` / `checkCanSetUser` protect every trusted identity. Enforcement is on,
  no flag.

### API and UX

- Two planes: the admin plane (`/connections`, `/catalogs`, `/engines`, `/bindings`) is connection
  owner / global admin only; the data plane is everything under `/workspaces/{id}/…` checked against
  `scope(id)`. The "granted in any workspace" fallbacks are deleted; table health moves under the
  workspace.
- One listing: `GET /workspaces/{id}/catalogs`. `/targets` and `/metadata/catalogs` become views of
  it; the legacy explorer is removed.
- Two-party grants (`/catalog-grants`): offer (catalog owner) / request (workspace admin) / approve
  (the other party) / direct grant (someone holding both) / decline-revoke-unlink (either). Consent
  on both sides because a pushed catalog changes what every member, service account and agent can do.
- The BI file, the legacy explorer and `catalog-access` are removed. BI returns through Baleia only
  (ADR 0017).

### Rollout

One release (0.6.2 + plugin 0.10.2), everything together. The migrate Job runs alongside the
rolling update, so every schema step is additive or expand/contract; 0.6.1 pods keep working and
fail closed where they cannot write a grant. External Trino on an old guard: the API logs a startup
error and `BALEIA_REQUIRE_CATALOG_SCOPE=true` refuses user queries.

## Consequences

- A statement reaches exactly the catalogs in the scope of the workspace it runs in, enforced in
  Trino, per query, never the union of a user's workspaces.
- Revocation takes effect at the next statement; a running statement finishes.
- Cross-catalog queries within the scope keep working.
- DEFINER views owned by a user stop working (the owner has no scope); trusted-owned DEFINER views
  deliberately delegate (documented).
- The BI file is gone; BI is re-served only by the gateway (0017).
- The scope is fail-closed on both sides: Baleia refuses to open an unscoped session, and the guard
  denies any unscoped identity.

## Current compliance

Not implemented. Tracked in #701 (guard), #702 (scope/migration), #703 (engine), #704 (api+ui).

## Alternatives considered

- **Baleia-side SQL parsing** as the boundary: rejected (ADR 0005); views, functions and
  `EXECUTE IMMEDIATE` bypass it.
- **Per-user allow-list** (union of the user's grants): rejected, over-grants across workspaces.
- **Per-workspace Trino identity** (`u_<uuid>@ws_<id>`): rejected, a DB lookup per check and
  noisier attribution; the extra credential is stateless.
- **One catalog per session**: rejected, breaks cross-catalog queries inside the scope.
- **Keep personal auto-reach**: rejected, still invisible in the UI and still widens on every new
  connection and on sharing a personal workspace.
- **Owner pushes without the workspace's consent**: rejected, silently changes what members, service
  accounts and write-capable agents can do.
- **One person holds both grant keys**: rejected, keeps the screens failing for everyone who is not
  a global admin.
