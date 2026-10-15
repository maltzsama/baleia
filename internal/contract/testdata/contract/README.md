# Contract fixtures shared with Baleia

The registry contract between this plugin and Baleia has parts each side
implements on its own. These files are its single source of truth:

| File | Plugin side | Baleia side |
|---|---|---|
| `catalog_version_vectors.json` | `ComputeCatalogVersionTest` | `catalog_version` (`internal/bindings/trino`) |
| `reserved_catalog_names.json` | `CatalogRow.RESERVED` (`ContractTest`) | binder reserved names and the database CHECKs |
| `placeholder_cases.json` | `SecretResolver` (`ContractTest`) | `@baleia-secret[...]` placeholders in catalog DDL |
| `registry_schema.sql` | `Database` statements (`ContractTest`) | prepared as `baleia_trino` against the migrated schema |

Every release ships this directory in its tarball; Baleia's contract tests
read it from the release it pins. To change the contract: change these files
and the plugin code together, release, then bump Baleia's pin.
