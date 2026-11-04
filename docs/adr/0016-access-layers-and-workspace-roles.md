# 0016. Three access layers and workspace roles

- Status: Accepted
- Date: 2026-11-03
- Issues: #653
- Supersedes: the workspace role matrix of #16 (D6), where a workspace member could only read and
  every write needed the workspace owner

## Context

Baleia had no role model for its own features. Global roles (`admin`, `member`), team roles (`owner`,
`member`) and space roles (`viewer`, `editor`, `manager`) existed, but nothing said who may edit
scripts or the semantic layer, run retrievers or manage a workspace. The catalog grant ended up
standing in for that, and access to data got mixed with access to Baleia.

## Decision

Three separate layers, each decided in one place:

1. **Workspace → catalog.** A catalog is linked to a workspace (`workspace_catalog_grants`), and
   only the workspace's members reach it.
2. **Catalog → data.** Inside the catalog, the catalog manager decides what each user may read or
   write, metadata included: per user, or as the connection's account
   ([ADR 0015](0015-per-user-catalog-identity.md)). Baleia never decides it.
3. **Baleia roles → Baleia features.** What a user may do in Baleia depends on their role, never on
   data permissions:

| Role | Scope | May do in Baleia |
|---|---|---|
| admin | global | The platform (connections, engines, catalogs, users, identity, workload, audit, system), and everything in every workspace |
| member | global | Enter the workspaces they belong to, with their workspace role |
| workspace admin | workspace | Members and their roles, linked catalogs, default target, spaces, settings, deletion; and everything an editor does |
| workspace editor | workspace | Create and edit saved queries and scripts, metrics, semantic models and docs, retrievers; run queries; Explore |
| workspace viewer | workspace | See content, run saved queries and Explore, run ad-hoc SQL; never create or edit content |

- **Membership.** A shared workspace's members are its team's members; a personal workspace's are
  its owner and the users it is shared with (`workspace_members`). A member's role is set per
  workspace; without one, a team owner is admin and a team member editor, as before. The owner of a
  personal workspace is always its admin.
- **Space roles only narrow.** A space role (`viewer`, `editor`, `manager`) can restrict content in a
  space, never raise it above the workspace role: viewer → viewer, editor → editor, admin → manager.
- **Agents act with their owner's role**, in the workspaces they are bound to, and never above it.
- The rule lives in the database (`baleia_workspace_member_role`, `baleia_effective_space_role`)
  and in `authz.WorkspaceRole`; the API derives `allowed_actions` (`edit_content`,
  `manage_members`, …) from it, and the UI follows those actions.

## Consequences

- A viewer cannot create content even in a space that names them its editor or manager.
- Team owners and members keep what they could do; workspace admins can now lower a member to
  viewer, or share a personal workspace.
- A user who leaves a team leaves its workspaces, whatever role was set for them.

## Current compliance

Workspace roles, personal sharing and the space ceiling are in place (#653), and so are Baleia
service accounts:

- A service account is a user of kind `service_account`: it has workspace roles like a person
  (directly, also in a team's workspace), never the global admin role, and no password.
- It signs in with a Baleia token (an API key) or, linked to an identity provider client, with that
  provider's client-credentials access token. Such a token is also its identity towards per-user
  catalogs ([ADR 0015](0015-per-user-catalog-identity.md)); without one it acts as the
  connection's account, like any user signed in without an identity provider.
- A global admin manages every service account; a workspace admin those created in their
  workspace. An agent can be linked to a service account and then acts with its roles.

- **Identity-provider groups → workspace roles.** An OIDC or LDAP provider's `workspace_mapping`
  binds a directory group to a role in a workspace; each sign-in applies it and drops the group
  roles the member no longer has. It sets the role only: a team's workspace still gets its members
  from the team mapping. A role a workspace admin set wins over a group.

MCP following roles follows under the same issue.

## Alternatives considered

- **Space roles may raise the workspace role** (as before). Rejected: a space entry would bypass the
  workspace role, and two places would decide the same thing.
- **Removing space roles.** Rejected: restricted spaces are still useful to narrow content.
- **Catalog grants with a read/write level** to stand in for roles. Rejected in #651: data access is
  the catalog manager's.
