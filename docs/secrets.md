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
  path separator or `..`. The read is confined to that directory: a symlink
  inside it may point within it through a **relative** target (as the
  `name -> ..data/name` links of a mounted Kubernetes Secret do). A symlink
  with an absolute target is refused even when it points inside the
  directory, so convert absolute links to relative ones; an unresolvable
  reference yields an empty secret.

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

## Catalog registry at rest (Phase B, #200 / D4)

`trino_catalog_registry` rows can still hold **catalog-origin** resolved
secrets (values Baleia decrypts from `catalog_connections` and projects). Phase
B seals those at rest with a key **dedicated** to the registry:

```bash
BALEIA_REGISTRY_KEY=<32+ random chars>            # required to enable Phase B
BALEIA_REGISTRY_KEY_PREVIOUS=<old key>            # only during a rotation
```

- The key is **never** `JWT_SECRET` and **never** `BALEIA_ENCRYPTION_KEY`;
  startup rejects either reuse.
- Derivation: `HKDF-SHA256(ikm=BALEIA_REGISTRY_KEY, salt=nil,
  info="baleia/registry/v1", length=32)` → AES-256-GCM key. The key id is the
  first 8 hex chars of `SHA-256(derived key)`.
- A sealed value is stored as `@baleia-enc[v1:<key-id>:<base64(nonce|ct)>]`.
  Baleia seals every property listed in the projection's secret keys; external
  `@baleia-secret[...]` references and already-sealed values are left alone.
- `catalog_version` is still hashed over the **resolved** values on both sides.
  Baleia hashes the plaintext before sealing; the plugin hashes the decrypted
  values it loads, so the hashes agree. A mismatch stays an explicit
  `secret_mismatch`, never silent drift.
- Empty `BALEIA_REGISTRY_KEY` keeps Phase A (plaintext rows). Enable Phase B
  only after upgrading to a plugin release that understands `@baleia-enc[...]`,
  then uncomment `baleia.registry-key=${ENV:BALEIA_REGISTRY_KEY}` in
  `catalog-store.properties`.

**Rotation.** Set the new key as `BALEIA_REGISTRY_KEY` and the old one as
`BALEIA_REGISTRY_KEY_PREVIOUS`. Both Baleia and the plugin read with either
key and seal with the current one, so existing rows keep opening while they are
rewritten. Once every row has been re-sealed, drop `_PREVIOUS`.

