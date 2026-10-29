---
sidebar_position: 6
---

# Administration

**Settings** holds the administration pages, grouped as Platform, Access and Activity. The rail shows Settings only to someone who can open at least one of them. Your own profile, password and API keys are under your avatar → **Profile**.

## Platform

- **System:** the state of every platform component, each probed on its own, so a slow dependency shows as unknown rather than failing the page:
  - **Infrastructure:** the database, Temporal, object storage and mail.
  - **Engines and catalogs:** the engines, the bindings, the catalog-store plugin, metadata syncs and reconciliation.
  - **Background work:** the recurring schedules and the audit export.
- **Assistant:** the optional [assistant](../assistant.md): provider, model and limits.

## Access

- **Identity:** OIDC and [LDAP](../ldap.md) providers. Users sign in locally until a provider is added. Provider secrets are written once and never shown again. Removing a provider suspends and detaches its users; it never deletes them.
- **Users:** invite people, change their [role](../concepts.md#roles), and suspend or reinstate accounts.
  - A role change takes effect at the next sign-in; existing sessions are revoked.
  - Suspension signs the user out everywhere and revokes their API keys. It is reversible: users are never deleted here.
- **Teams:** a team owns shared workspaces. Owners manage the team and its workspaces; members do not. Removing someone from a team removes their access to its workspaces.
- **Workspaces** (workspace menu in the header → **Workspace settings**): each workspace's members, its catalog grants and its default target.
  - **Grants:** a catalog is granted for read or write. Read grants run in read-only sessions where the engine enforces them. Revoking the catalog used as the default target clears it.
  - **Deletion:** deleting a shared workspace removes its spaces, content and grants. Personal workspaces cannot be deleted.
- **Agents:** read-only clients of the [MCP server](../mcp.md), bound to workspaces. Each agent has tokens, shown once and stored only as a hash. Revoking a token takes effect at once.

## Attention

The bell in the header lists what needs attention, errors first. It is computed from the current state each time, so an item disappears once fixed; there is no read state and no e-mail. Each item links to the page that fixes it, and everyone sees only what they can act on:

| Item | Meaning |
|---|---|
| Binding error | A catalog binding failed to apply on its engine |
| Metadata sync failed / stale | A catalog's metadata could not be refreshed, or is older than expected |
| Engine unreachable | An engine does not answer |
| Retriever error | A [retriever](content.md#retrievers) refresh failed |
| Reconcile drift | An engine's catalogs differ from what Baleia manages ([reconciliation](../operations.md#binding-reconciliation)) |
| Schedule missing | A recurring job is not scheduled in Temporal |
| Audit export lagging | The audit export is behind |
| Agent token expiring | An [agent](#access)'s token expires soon |

## Activity

**Audit** is the trail of every security and data operation: who did what, on which resource, and with what outcome. Administrators see everything; other users see their own events and those of workspaces they own. Results are filtered by a time window, actor and resource. [Operations](../operations.md#audit-trail) covers retention and export to external systems.

## Connections, catalogs and engines

These live in the **Connections** area, not in Settings:

- **Connections:** what Baleia reaches. The **New connection** wizard tests the connection, adds catalogs and grants them.
- **Catalogs:** each catalog's bindings, metadata sync, access, [maintenance](monitoring.md#table-maintenance) and activity.
- **Engines:** the engines that run SQL, with their bindings and reconciliation.

Destructive actions in all of these ask for confirmation and say what will be affected first.
