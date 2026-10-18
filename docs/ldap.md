# LDAP and Active Directory

> **Experimental.** LDAP login, group mapping, the admin UI and scheduled
> directory reconciliation are complete (phase 4); the configuration format may
> still change without a deprecation cycle. OIDC is the more mature directory
> option.

Baleia authenticates a directory user with a **user bind**, then maps the
user's groups to a platform role and teams with the same engine OIDC uses
(`internal/identity/mapping.go`). Accounts are linked by `objectGUID` (Active
Directory) or `entryUUID` (OpenLDAP) in `users.external_id`; changing a DN or a
username does not break the link, and e-mail is never used to link accounts.

## Security requirements

- `ldaps://` is the default. Plaintext `ldap://` is accepted **only** with
  `start_tls: true`; any other plaintext configuration is rejected when the
  provider is created or opened.
- The service account (`bind_dn` / `bind_password`) needs read access to the
  user and group entries only. It is never used to verify a user password.
- An empty password is rejected before any bind: LDAP would otherwise treat it
  as an anonymous bind.
- Usernames are validated (`^[A-Za-z0-9._@-]{1,128}$`) and inserted into the
  search filter through `ldap.EscapeFilter`, never by concatenation.
- The whole provider config, including `bind_password`, is sealed with
  `BALEIA_ENCRYPTION_KEY` and bound to the provider id through AAD. A config
  that cannot be opened fails closed.
- Directory users are **not auto-created**. An account must already exist and
  be linked by its stable id (default deny). This mirrors the OIDC provisioning
  policy.

## Configuration

Create an `auth_providers` row with `type = 'ldap'` and a sealed JSON config.
Provider management reuses the OIDC admin tooling (the dedicated UI is a later
slice). The JSON shape is:

```json
{
  "url": "ldaps://ldap.example.org:636",
  "start_tls": false,
  "ca_pem": "-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----",
  "bind_dn": "cn=svc-baleia,ou=services,dc=example,dc=org",
  "bind_password": "…",
  "base_dn": "ou=people,dc=example,dc=org",
  "user_filter": "(uid={{username}})",
  "group_attribute": "memberOf",
  "email_attribute": "mail",
  "name_attribute": "cn",
  "nested_groups": true,
  "required_group": "cn=baleia-users,ou=groups,dc=example,dc=org",
  "timeout_seconds": 10,
  "role_mapping": [
    { "group": "cn=baleia-admins,ou=groups,dc=example,dc=org", "role": "admin" }
  ],
  "team_mapping": [
    { "group": "cn=data-team,ou=groups,dc=example,dc=org", "team_id": "…", "team_role": "member" }
  ],
  "default_role": "member"
}
```

| Field | Required | Notes |
|---|---|---|
| `url` | yes | `ldaps://` or `ldap://` with `start_tls: true`. Credentials in the URL are rejected. |
| `start_tls` | for `ldap://` | Upgrades a plaintext connection before the service bind. |
| `ca_pem` | no | PEM bundle for a private CA (or a development self-signed certificate). Empty uses the system roots. |
| `bind_dn`, `bind_password` | yes | Read-only service account; the config is sealed. |
| `base_dn` | yes | Subtree searched for users (and groups when nested groups are enabled). |
| `user_filter` | yes | Exactly one `{{username}}` placeholder; the value is escaped. |
| `group_attribute` | no | Defaults to `memberOf`. |
| `email_attribute`, `name_attribute` | no | Default to `mail` and `cn`. |
| `nested_groups` | no | Uses AD's recursive matching rule `1.2.840.113556.1.4.1941`. **AD-only**; OpenLDAP does not implement it, and a failed nested query fails the login closed. |
| `required_group` | no | Case-insensitive group DN/name the user must belong to. |
| `timeout_seconds` | no | 1–60; defaults to 10. |
| `role_mapping`, `team_mapping`, `default_role` | no | Same semantics and precedence as OIDC: the most privileged mapped role wins, `default_role` defaults to `member`. |

The login request sends `provider_id` and `username`:

```json
{ "provider_id": "…", "username": "alice", "password": "…" }
```

`email` is ignored when `provider_id` is present. Every failure is the uniform
`401 invalid_credentials`, and the progressive per-account delay is shared with
local logins.

## Active Directory example

```json
{
  "url": "ldaps://dc01.corp.example:636",
  "bind_dn": "CN=svc-baleia,OU=Services,DC=corp,DC=example",
  "bind_password": "…",
  "base_dn": "OU=People,DC=corp,DC=example",
  "user_filter": "(sAMAccountName={{username}})",
  "group_attribute": "memberOf",
  "nested_groups": true,
  "required_group": "CN=Baleia Users,OU=Groups,DC=corp,DC=example",
  "role_mapping": [
    { "group": "CN=Baleia Admins,OU=Groups,DC=corp,DC=example", "role": "admin" }
  ]
}
```

AD returns the binary `objectGUID`; Baleia renders it as a UUID for
`users.external_id`.

## OpenLDAP example

```json
{
  "url": "ldaps://ldap.example.org:636",
  "bind_dn": "cn=svc-baleia,dc=example,dc=org",
  "bind_password": "…",
  "base_dn": "ou=people,dc=example,dc=org",
  "user_filter": "(uid={{username}})",
  "group_attribute": "memberOf",
  "required_group": "cn=baleia-users,ou=groups,dc=example,dc=org",
  "role_mapping": [
    { "group": "cn=baleia-admins,ou=groups,dc=example,dc=org", "role": "admin" }
  ]
}
```

OpenLDAP must have the `memberof` overlay enabled for `memberOf` to be present
(the compose profile enables it). `nested_groups` must stay `false` unless the
server supports the AD matching rule.

## Local directory

The canonical place to run the directory tests is the **minikube dev cluster**,
because the worker that runs `directory-reconcile` lives there too (#54):

```bash
make k8s-up                 # the dev stack (skip if already running)
make k8s-source NAME=ldap   # start src-ldap, seeded from deploy/dev/ldap/50-baleia.ldif
make k8s-ldap-forward       # 1389:389 and 1636:636 on localhost

BALEIA_TEST_LDAP_URL=ldap://localhost:1389 \
  go test -tags integration ./internal/identity/ldap/
```

`make k8s-source NAME=ldap` also creates the `baleia-ldap-seed` ConfigMap from
the seed file, so the directory always matches the tracked LDIF. The profile
starts OpenLDAP with the seed LDIF in `deploy/dev/ldap/50-baleia.ldif`:

| | |
|---|---|
| Admin / service bind | `cn=admin,dc=example,dc=org` / `admin` |
| User | `alice` / `ldap-password` |
| Base DN | `ou=people,dc=example,dc=org` |
| Admin group | `cn=baleia-admins,ou=groups,dc=example,dc=org` |

These credentials are development-only. The seed does not enable TLS; the
integration test uses the forwarded `ldap://localhost:1389` (the production
config validator still refuses plaintext).

The integration test verifies the user bind and that `cn=baleia-admins` maps to
the `admin` role.

For the reconciliation test (#54), configure the provider with the **in-cluster**
address instead of `localhost`, because the worker dials it from its pod:

```
ldap://src-ldap.baleia-dev.svc:389
```

Then disable `alice` in the directory, wait one `directory-reconcile` run (the
schedule fires every 15 minutes) and verify `users.status = 'suspended'` with
the refresh tokens revoked.

For a quick local check outside the cluster, the compose profile also works
(`docker compose --profile ldap up -d ldap`). It binds `localhost:389` through
the Docker daemon, so point the test at that port instead.
