# 0027. Catalog-manager RBAC is administered through Baleia and owned by the manager

- Status: Proposed
- Date: 2026-11-13
- Issues: #993
- Builds on: [0015](0015-per-user-catalog-identity.md), [0016](0016-access-layers-and-workspace-roles.md)
  layer 2, [0018](0018-workspace-catalog-scope.md) (admin plane)

## Context

Layer 2 of [0016](0016-access-layers-and-workspace-roles.md) says the catalog manager decides what
each user may read or write inside a catalog, and that Baleia never decides it. Baleia gives
administrators no way to see or change those decisions, so they have to use the Polaris CLI or the
raw management API, outside Baleia, with no audit trail and no view of which Baleia user maps to
which principal.

Facts the design must respect:

- **The Polaris model.** Privileges are granted to *catalog roles*, on a catalog, namespace, table
  or view. Catalog roles are granted to *principal roles*, and principal roles to *principals*.
  - **Principals and principal roles are realm-wide.** Every connection to the same Polaris realm
    sees them.
  - **Catalog roles and grants belong to one catalog.**
  - All of it is reachable through the management API (`/api/management/v1/...`).
- **What the code does today.** `internal/connections/polaris` builds its management client from
  the connection's only credential (`client_id`/`client_secret`, plus `realm`), and calls that API
  only to list catalogs.
- **Who Polaris sees** ([0015](0015-per-user-catalog-identity.md)):
  - `catalogidentity.Resolver.CatalogToken` sends a user's own access token only when
    `AuthProvider == "oidc"`, on a connection with `user_identity` on.
  - Local and LDAP users, work without a user, and every user of a connection with `user_identity`
    off reach Polaris as the **connection's account**.

  Whether a grant reaches a user depends on both the connection setting and how the user signs in.
- **Baleia does not compute the principal.** It hands the token to Trino, and Polaris maps it to a
  principal by a claim set in Polaris's configuration (`preferred_username`, `email`, `sub`, …).
  Roles claimed in the token are not trusted (spike on #648).
- **Baleia cannot learn the authentication setup from the API.** Polaris's authentication mode
  (`internal`, `external`, `mixed`) and its principal claim are server configuration. The
  management API does not expose them.
- **The connection credential is a data principal.** Giving it `SERVICE_MANAGE_ACCESS` or
  `CATALOG_MANAGE_ACCESS` would widen what every query run as that account can do.
- **Other managers.** Unity and Lakekeeper also authorize users (0015), but each has its own model.
  Glue, Hive Metastore and Nessie have no per-user RBAC that Baleia can administer.

## Decision

### Ownership

- The catalog manager is the only store of its RBAC. Baleia keeps no table of principals, roles,
  assignments or grants, and no copy that any decision reads. Every RBAC view reads the manager
  live.
- A mutation is shown as done only after the manager confirms it, and the view then reads the
  state again.
- Baleia never writes to a manager's database. It uses only the manager's public management API.

### An optional admin interface, not a data capability

- `internal/catalogrbac` defines an `Admin` interface. A provider that can be administered
  implements an optional interface that returns an `Admin`. Only the wiring sites construct it
  ([0007](0007-dispatch-and-wiring.md)), and every outbound call goes through `connections.Dialer`.
- RBAC administration is **not** a `connections.Capability`. That map (per `AccessMode`) describes
  data operations ([0021](0021-unified-connection-model.md)). The API exposes RBAC administration
  as a separate field of the connection type (`rbac_admin: true`).
- Polaris is the only implementation in this ADR. Views and payloads use the manager's own terms
  (principal role, catalog role, privilege names such as `TABLE_READ_DATA`). Privileges are the
  strings the deployed version accepts, not a Baleia enum.

### The management settings of a connection

A Polaris connection gains an optional **management** section. Every field is declared by the
administrator; none is inferred from a failed call.

| Field | Meaning |
|---|---|
| `mgmt_client_id`, `mgmt_client_secret` | The credential used for administration. Sealed by the keyring, decrypted fail-closed, never returned by the API. |
| `auth_mode` | `internal`, `external` or `mixed`. Must match Polaris's `polaris.authentication.type`. |
| `principal_claim` | The token claim Polaris maps to a principal name. Must match Polaris's configuration. |

- **Mutations use only the management credential.** The service that performs mutations is built
  from the management credential alone. With no management credential there is no mutation
  service, and every mutation returns `catalog_rbac_unavailable`. This is enforced in the backend,
  not only by hiding buttons in the UI.
- **Reads without a management credential are a degraded mode.** Reads use the catalog credential,
  show only what Polaris allows it to see, and the screen says so.
- The management credential is never sent to Trino, never placed in a query session and never used
  to read data.

### Operations (Polaris)

- **Catalog roles:** list, create, inspect, delete, assign to a principal role, revoke.
- **Grants:** list, grant and revoke privileges on a catalog, namespace, table or view. The view
  shows the level each grant is attached to. Baleia does not compute inheritance; it shows what
  Polaris returns.
- **Principal roles:** list, create, inspect, delete, assign to a principal, revoke.
- **Principals:** list and inspect. Creation depends on `auth_mode`:
  - `internal` or `mixed`: Baleia can create a principal with credentials. The credentials are shown
    once (FD8) and Baleia never stores them.
  - `external`: Baleia creates a principal by name only, so an identity-provider user can be
    granted roles before their first query.

### Resolving targets on the server

- **The connection comes from the path.** Every request names a connection
  (`/connections/{id}/rbac/...`), and the backend loads that connection's settings. A client
  never supplies a URL, a realm or a credential.
- **Catalog names are checked.** A catalog name in a request must be in the catalog list that the
  same connection's management client returns. Any other name gets 404 before Polaris is called.

### Who may administer

Two levels, because of what each kind of Polaris entity reaches:

| Operation | Reaches | Who may do it |
|---|---|---|
| Catalog roles and grants | One catalog | The connection owner or a global admin (the [0018](0018-workspace-catalog-scope.md) admin plane) |
| Principals and principal roles | The whole realm, including other connections to it | A global admin only |

They are exposed as `allowed_actions` on the connection (`manage_catalog_rbac`,
`manage_realm_rbac`). There is no delegation per catalog in this version.

### What takes effect, and for whom

- **User → principal.** For a Baleia user, the principal they appear as in Polaris is:
  - the value of `principal_claim` read from that user's stored, validated identity-provider token
    (`user_idp_tokens`), when the user signs in with `AuthProvider == "oidc"` and the connection
    has `user_identity` on;
  - the connection's account, when the user signs in locally or through LDAP, or `user_identity`
    is off;
  - **unknown**, when `principal_claim` is not set, the user has no stored token, or the claim is
    missing from it. Baleia never guesses from the user's e-mail.
- Every grant view uses this mapping. It never presents a grant to an individual principal as
  effective for a Baleia user who does not reach Polaris as that principal.
- **The connection's mode is stated on every screen:**
  - "Per user": grants apply to identity-provider users;
  - "Connection account for every user": only the connection account's roles apply to Baleia
    users, and grants to other principals affect only clients outside Baleia.

### Errors and audit

- Manager failures map to new codes in the closed `ErrorCode` enum:
  - `catalog_rbac_unavailable`: the manager is unreachable, or the connection has no management
    credential;
  - `catalog_rbac_forbidden`: the manager refused the credential;
  - `catalog_rbac_conflict`: the entity exists, or is still referenced.

  A raw manager message is never the headline (FD7).
- **Audit.** Every mutation, successful or failed, is audited with the actor, the connection, the
  operation, the target entity and the result. The record never holds secrets or principal
  credentials.
- **Confirmation.** Deleting a role or a principal, and every revoke, needs a confirmation that names
  the consequences (FD4). Realm-wide operations also name the other connections to the same realm.

## Consequences

- Administrators manage data access to Polaris catalogs inside Baleia, with an audit trail, and
  each screen says which Baleia users a change actually reaches.
- Changes made outside Baleia (CLI, another tool) show up on the next read, because Baleia keeps
  no copy to drift.
- `auth_mode` and `principal_claim` repeat Polaris's configuration. If they disagree with the
  server, principal creation and the user mapping are wrong, so the connection test checks the
  claim: an identity-provider user's token must resolve to a principal that exists.
- Deployments where most users are local or LDAP, or with `user_identity` off, gain little from
  per-principal grants. The screens say so instead of hiding it.
- Baleia now holds a powerful secret. Supplying it is the operator's choice. The narrowest
  credential that works is a principal with `CATALOG_MANAGE_ACCESS` on the catalogs it
  administers; realm-wide operations need `SERVICE_MANAGE_ACCESS`.
- The Polaris provider's configuration schema and client construction change: there are two
  clients, data and management, built from different credentials.
- Unity and Lakekeeper need their own adapters and decisions. This ADR does not cover them.

## Current compliance

Not implemented. The Polaris provider (`internal/connections/polaris`) builds its management client
from the data credential and only lists catalogs.

## Alternatives considered

- **A Baleia RBAC table synced to the manager.** Rejected: two sources of truth that drift whenever
  someone uses the CLI, and an easy path to Baleia "deciding" access, which
  [0016](0016-access-layers-and-workspace-roles.md) forbids.
- **`rbac_admin` as a `connections.Capability`.** Rejected: that map describes data operations per
  access mode. Mixing administration into it changes what [0021](0021-unified-connection-model.md)
  means.
- **Infer `auth_mode` from whether creating a principal with credentials succeeds.** Rejected: it
  makes a security-relevant setting depend on a side effect, and it leaves principals behind.
- **Map users to principals by e-mail.** Rejected: Polaris maps by its own configured claim.
- **One manager-neutral RBAC model in the UI.** Rejected: Polaris, Unity and Lakekeeper differ in
  structure, and a common model would hide what each one enforces.
- **Reuse the catalog credential for administration.** Rejected: the account every query runs as
  would need manage privileges.
- **Link out to the manager's own console.** Rejected as the only option: Polaris ships no
  administration UI, and nothing outside Baleia knows the Baleia user ↔ principal mapping.
- **Polaris's external PDP (OPA inside Polaris).** Out of scope. It replaces Polaris's authorizer
  rather than administering it, and it does not filter rows or mask columns. See
  [0028](0028-data-policies-opa-trino.md).
