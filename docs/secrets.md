# Secrets

## Connection credentials at rest

Connection credentials are sealed with `BALEIA_ENCRYPTION_KEY` (AEAD, bound to
the connection row) in `catalog_connections.credentials_enc`. `JWT_SECRET` never
encrypts them.

## External secret references

A connection credential **can instead hold a reference**, so the secret never
lives in Baleia's database:

```
@baleia-secret[env:VAR]
@baleia-secret[file:name]
```

- `env:` reads `VAR` from the process environment (upper-case names only).
- `file:` reads `BALEIA_SECRET_FILE_BASE_DIR/name`; the name must not contain a
  path separator or `..`.

Baleia stores the **reference** (not the value), resolves it for its own use
(browsing, connection tests) with the same rules, and projects the reference
into the Trino catalog registry. The Trino plugin (v0.9.2+) resolves it at boot
and on every `CREATE CATALOG` — so a secret rotation only needs the mounted file
or the environment variable to change, no DDL.

Operators mount **the same secret** into both Baleia and Trino:

```yaml
# both services
environment:
  - MY_SECRET_FILE_DIR=/run/secrets
volumes:
  - ./secrets:/run/secrets:ro
```

```sql
CREATE CATALOG lake USING iceberg WITH (
  "iceberg.rest-catalog.oauth2.token" = '@baleia-secret[file:polaris-token]'
);
```

The registry row keeps the placeholder; `trino_catalog_registry` never holds the
plaintext. `catalog_version` is hashed over the **resolved** values on both
sides, so Baleia and the plugin agree; a divergence surfaces as
`secret_mismatch`, never a silent drift.

`BALEIA_SECRET_FILE_BASE_DIR` is the directory `file:` references resolve
under. Without it, `file:` references are unavailable; `env:` still works.
