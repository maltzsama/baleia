# 0015. Per-user identity to the catalog manager

- Status: Accepted
- Date: 2026-11-03
- Issues: #644, #648
- Supersedes: the Polaris part of [ADR 0005](0005-trino-security-model.md) ("Polaris sees one service
  principal per connection") and the identity modes of [ADR 0011](0011-direct-connections-security.md) ("`per_user` comes later")

## Context

Baleia's premise has always been that **the catalog manager decides what each user may read and
write**: the user signed in to Baleia reaches the catalog through Trino, and Polaris, Unity or
Lakekeeper authorize that user with their own grants. Until now every catalog Baleia bound talked to
its manager with one service principal per connection, so the manager never saw the user.

The spike on #648 (Polaris 1.6, Trino 484, an OIDC issuer) showed the way through:

- with `iceberg.rest-catalog.session=USER`, Trino builds the Iceberg REST session from the session's
  extra credentials, and the Iceberg client uses the credential `token` as the bearer token;
- a user's identity-provider access token sent that way is authorized by Polaris with its own grants
  (roles claimed in the token are not trusted); another user's token is refused, and the
  `X-Trino-User` header cannot change who the manager sees;
- without a token the session fails: Trino's own JWT is not trusted;
- the token of the connection's service account works the same way, so one catalog serves both
  cases;
- Trino does not expose extra credentials through its query API.

## Decision

- **Per-user identity is a connection setting** (`user_identity`) of the catalog managers that can
  authorize users: Polaris, Unity, Lakekeeper and generic Iceberg REST catalogs. The administrator
  turns it on once the manager trusts the identity provider Baleia signs users in with. Catalogs
  bound from such a connection get `iceberg.rest-catalog.session=USER`.
- **Every query session carries a bearer token** as the Trino extra credential `token`, filled when
  the session opens, in the process that runs it (`queryengine.CatalogCredentials`). It never
  travels in workflow input or in the API.
  - A user signed in through an **identity provider** carries their own access token.
  - A user signed in **without one** (local or LDAP accounts) and work **without a user** (retrievers,
    metadata, maintenance) carry the token of the connection behind the session's catalog: the
    manager decides as that account.
  - A user signed in through an identity provider whose token **expired and cannot be refreshed**
    carries none. The manager refuses until they sign in again. They never fall back to the
    connection's account, which may reach more than they do.
- **Baleia keeps each user's identity-provider tokens** from their latest OIDC sign-in
  (`user_idp_tokens`), sealed with the keyring and bound to the user, and refreshes the access token
  with the refresh token before it expires. Signing out, a suspended account and a disabled or
  removed provider delete them.
- **Managers that cannot authorize users** (Glue, Hive Metastore, Nessie) and per-user-capable
  managers with the setting off act as the connection's account for every user, and Baleia shows it
  on the connection ("Connection account for every user").
- Baleia's own controls stay: a catalog linked to a workspace is reachable by its members only, the
  catalog guard keeps catalog management to the admin and enforces read-only sessions, and Trino
  requires Baleia's login (#646, #647).

## Consequences

- The catalog manager is the enforcement point for data, also for traffic that does not go through
  Baleia: a direct client cannot forge a user's token.
- The manager must trust Baleia's identity provider, and its principals must exist under the names
  the token carries (Polaris: `polaris.authentication.type=external` or `mixed`, the principal
  mapped from a claim such as `preferred_username`, and grants on principal roles).
- A query that touches catalogs of two per-user connections with different accounts works for
  identity-provider users (one token for every catalog). Without one, the session carries the
  account of its own catalog's connection, and a manager that does not accept that account refuses.
- Tokens are secrets at rest: they need `BALEIA_ENCRYPTION_KEY`, like connection credentials.

## Current compliance

Compliant for Iceberg REST catalog managers bound to Trino. Agents and Baleia service accounts get
their identity with the roles of #653.

## Alternatives considered

- **Trino's own JWT with token exchange (RFC 8693).** Rejected: nothing the manager trusts signs it;
  Polaris refused it in the #9 spike.
- **Passing the connection's client secret per session (`credential`).** Rejected: the secret would
  travel through Trino on every query; a short-lived token does not.
- **One catalog per user.** Rejected: catalogs multiply with users, and the binder would manage them.
- **Falling back to the connection's account when a user's token expires.** Rejected: it can grant a
  user more than the manager gives them.
