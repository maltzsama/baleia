---
sidebar_position: 7
description: "Administer Baleia: workspaces, teams and roles, OIDC or LDAP sign-in, API keys, service accounts, platform health and the audit trail of operations."
---

# Administration

**Settings** holds the administration pages, grouped as Platform, Access and Activity. The rail shows Settings only to someone who can open at least one of them. Your own profile, password and API keys are under your avatar → **Profile**.

## Platform

- **System:** the state of every platform component, each probed on its own, so a slow dependency shows as unknown rather than failing the page:
  - **Infrastructure:** the database, Temporal, object storage and mail.
  - **Engines and catalogs:** the engines, the bindings, the catalog-store plugin, metadata syncs and reconciliation.
  - **Background work:** the recurring schedules and the audit export.
- **Assistant:** the optional [assistant](../assistant.md): provider, model and limits.
- **Workload:** each engine's queues and capacities, and the rules that classify runs ([workload management](workload.md)).

## Access

- **Identity:** OIDC and [LDAP](../ldap.md) providers. Users sign in locally until a provider is added. Provider secrets are written once and never shown again. Removing a provider suspends and detaches its users; it never deletes them. Each provider maps directory groups, at every sign-in, to the platform role, to teams and to **workspace roles** (`workspace_mapping`: group → workspace and admin/editor/viewer). A workspace role from a group needs team membership for a team's workspace, and a role a workspace admin set wins over it.
- **Users:** invite people, change their [role](../concepts.md#roles), and suspend or reinstate accounts.
  - A role change takes effect at the next sign-in; existing sessions are revoked.
  - Suspension signs the user out everywhere and revokes their API keys. It is reversible: users are never deleted here.
- **Teams:** a team owns shared workspaces. Its owners are admins of those workspaces by default and its members editors. Removing someone from a team removes their access to its workspaces.
- **Workspaces** (workspace menu in the header → **Workspace settings**): each workspace's members, its catalog grants and its default target.
  - **Roles** ([ADR 0016](../adr/0016-access-layers-and-workspace-roles.md)): a **viewer** sees content and runs queries (saved, Explore and ad-hoc SQL); an **editor** also creates and edits saved queries and scripts, metrics, semantic models and docs, and retrievers; an **admin** also manages members, catalogs, the default target, spaces and settings. A global admin is an admin of every workspace.
  - **Members:** a shared workspace's members are its team's members; team owners are admins and members editors until a workspace admin sets another role. A personal workspace belongs to its owner, who can share it with anyone by e-mail, with a role. Space roles can only narrow a member's role inside a space, never raise it.
  - **Grants:** granting a catalog links it to the workspace, so only the workspace's members reach it. Inside the catalog, what each member may read or write is decided by the catalog manager, or by the connection's service account; what they may do in Baleia depends on their role. Revoking the catalog used as the default target clears it.
  - **Deletion:** deleting a shared workspace removes its spaces, content and grants. Personal workspaces cannot be deleted.
- **Service accounts** (**Settings → Service accounts**): non-human principals for agents and integrations ([ADR 0016](../adr/0016-access-layers-and-workspace-roles.md)). A global admin creates them anywhere; a workspace admin creates them in their workspace, with a role. They have workspace roles like people (add one to more workspaces from **Members** with its e-mail) and never the global admin role. They sign in with a Baleia token (header `X-Api-Key`, shown once) or, linked to an identity provider client, with that provider's client-credentials access token as `Authorization: Bearer`; that token also reaches catalogs set to per-user identity.
- **Agents:** clients of the [MCP server](../mcp.md), bound to workspaces and following their owner's role in each: a viewer's agent only reads, an editor's or admin's may also write; the built-in assistant only reads. Each agent has tokens, shown once and stored only as a hash. Revoking a token takes effect at once.

## Attention

The bell in the header lists what needs attention, errors first. It is computed from the current state each time, so an item disappears once fixed; there is no read state and no e-mail. Each item links to the page that fixes it, and everyone sees only what they can act on:

| Item | Meaning |
|---|---|
| Binding error | A catalog binding failed to apply on its engine |
| Metadata sync failed / stale | A catalog's metadata could not be refreshed, or is older than expected |
| Engine unreachable | An engine does not answer |
| Retriever error | A [retriever](content.md#retrievers) refresh failed |
| Broken lineage | A model or metric points at a table or column that no longer exists ([lineage](data.md#lineage)) |
| Reconcile drift | An engine's catalogs differ from what Baleia manages ([reconciliation](../operations.md#binding-reconciliation)) |
| Schedule missing | A recurring job is not scheduled in Temporal |
| Audit export lagging | The audit export is behind |
| Agent token expiring | An [agent](#access)'s token expires soon |

## Activity

**Audit** is the trail of every security and data operation: who did what, on which resource, and with what outcome. Administrators see everything; other users see their own events and those of workspaces they own. Results are filtered by a time window, actor and resource. [Operations](../operations.md#audit-trail) covers retention and export to external systems.

## Connections, catalogs and engines

These live in the **Connections** area, not in Settings:

- **Connections:** what Baleia reaches. The **New connection** wizard asks how you will use a source that supports both ways, **Direct** or **Through a query engine** (neither is preselected, and a source with only one way gets it), then tests the connection, adds catalogs and grants them. Direct creates the source's own engine with it; Through a query engine lists the federated engines that can bind the source and creates none. The choice cannot be changed later. Saving changed settings or credentials applies the connection's managed catalogs to their engines again; one that cannot be applied shows as a binding error in Attention. By default only admins create and edit connections (`BALEIA_CONNECTIONS_CREATE`), and connections may not reach private address ranges unless the operator allows them (`BALEIA_CONNECTIONS_ALLOW_CIDRS`, [configuration](../configuration.md)).
- **Catalogs:** each catalog's bindings, metadata sync, access, [maintenance](monitoring.md#table-maintenance) and activity.
- **Engines:** the engines that run SQL, with their bindings and reconciliation. **Register engine** adds a federated engine (Trino today) on its own: pick the kind, name it and fill in its endpoint, with a login and a CA certificate when it needs HTTPS. It creates the connection and the engine together, and the engine starts with no catalogs. BI tools never reach an engine directly: they go through the BI gateway, which the operator enables ([Install](../install.md#bi-gateway)) and which is gated per workspace. See [Open in a BI tool](data.md#open-in-a-bi-tool).

Destructive actions in all of these ask for confirmation and say what will be affected first.

### Who the catalog manager sees

A catalog linked to a workspace is reachable by its members only. Inside it, the catalog manager decides what each one may read and write ([ADR 0015](../adr/0015-per-user-catalog-identity.md)):

- **Per-user identity.** On Polaris, Unity, Lakekeeper and Iceberg REST connections, turn on **Per-user identity** once the catalog manager trusts the identity provider Baleia signs users in with. Each user then reaches the manager with their own token, and the manager's grants apply to them. Users signed in without an identity provider (local or LDAP accounts), and background work such as retrievers and maintenance, act as the connection's own account. A user whose identity-provider session ended is refused until they sign in again; they never act as the connection's account.
- **Connection account for every user.** With the setting off, and on Glue, Hive Metastore and Nessie, which cannot authorize users, every user acts as the connection's account. The connection card says so.

For Polaris, the manager trusts the provider with `polaris.authentication.type=external` (or `mixed`) and `quarkus.oidc.*` pointing at the same issuer, maps the principal from a token claim (for example `polaris.oidc.principal-mapper.name-claim-path=preferred_username`), and needs a principal of that name with its grants on principal roles. Roles claimed in the token are not trusted.
