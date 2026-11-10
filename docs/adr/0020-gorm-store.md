# 0020. GORM is the store ORM; no handwritten SQL survives in the data layer

- Status: Accepted; amended by [0024](0024-cold-storage-and-retention.md) (table partitioning behind a `PartitionManager` seam)
- Date: 2026-11-07
- Issues: #758, #754

## Context

Baleia's own store (users, workspaces, saved content, models, connections, catalogs, audit, snapshots) is SQL-first: 80 goose migrations, sqlc-generated queries and `pgx`. This ties the store to PostgreSQL and spreads SQL across the codebase: the schema in `.sql` files, the queries in `.sql` files, and a growing set of dialect-specific strings.

The owner's directive is stricter than "portable SQL": **SQL must not be authored manually in Baleia's relationship with its own database.**

The store adopts GORM as its ORM, following a model-centric, Active Record-inspired persistence approach. Models define the database schema through Go structs and GORM tags. GORM provides model lifecycle hooks, associations, callbacks, scopes, CRUD operations and automatic schema management. Persistence operations are performed through GORM's model-oriented API rather than handwritten SQL.

Schema changes are expressed as versioned Go migrations using GORM's migration and schema APIs. GORM resolves the database dialect and generates the corresponding DDL. The application does not write DDL.

The query-execution path (Trino and the direct engines) runs user SQL against external engines and is not part of this decision. It retains its own dialect layer.

## Decision

- **GORM is the ORM for Baleia's own store.** No handwritten SQL statements, SQL migration files or dialect-specific SQL are permitted in the store implementation.

- **Models define the schema.** Each database table has a corresponding Go struct with GORM tags defining its columns, types, primary keys, unique constraints, defaults, indexes, checks and relationships. Models are the authoritative definition of the current database schema.

- **Persistence follows GORM's model-oriented API.** CRUD operations use GORM's model-aware methods (`Create`, `First`, `Find`, `Updates`, `Save`, `Delete`), associations and lifecycle hooks. The application does not implement SQL queries or manually map database rows to domain objects.

- **Model lifecycle is ORM-managed.** GORM hooks (`BeforeCreate`, `AfterCreate`, `BeforeUpdate`, `AfterUpdate`, `BeforeDelete`, `AfterDelete`, `AfterFind`) provide lifecycle integration where appropriate. Business rules remain in the application and are not hidden inside persistence callbacks.

- **Associations are model-defined.** One-to-one, one-to-many, many-to-many and belongs-to relationships are represented through GORM associations and foreign-key declarations. Association loading uses GORM's `Preload`, `Joins` and association APIs.

- **Schema changes are versioned Go migrations.** Goose and SQL migration files are removed. Baleia maintains a versioned Go migration registry and runner. Each migration uses GORM's `AutoMigrate` or `Migrator` APIs to perform schema operations. The migration history remains explicit, ordered and reproducible.

- **AutoMigrate is not the migration history.** GORM's `AutoMigrate` creates missing tables, columns, indexes and constraints and performs supported schema alterations. It does not remove obsolete columns. Destructive changes, renames and other nontrivial transitions require explicit, versioned Go migrations using GORM's `Migrator` interface. Automatic schema synchronization is not a substitute for reviewing and versioning schema changes.

- **The models are the schema source of truth.** Migration code describes transitions between schema versions, not a second independent schema definition. Indexes, constraints and relationships belong in model declarations whenever GORM can represent them. Migration-specific operations exist only when necessary to evolve the schema.

- **What GORM cannot express without handwritten SQL does not survive.** PostgreSQL-specific functions, triggers and trigram search are removed or redesigned outside the store. **Table partitioning is the single carve-out**, re-introduced by [0024](0024-cold-storage-and-retention.md) behind a dedicated `PartitionManager` seam; the only code that authors dialect-specific partition DDL is that adapter, never a service or repository. Business validation and authorization remain in the application. Search-specific requirements belong to the search layer. No raw SQL, `Raw`, `Exec`, `gorm.Expr` or dialect-specific SQL fragments are permitted in the store implementation outside the `PartitionManager` adapter.

- **Database integrity remains important.** Primary keys, foreign keys, unique constraints, NOT NULL constraints, defaults, indexes and CHECK constraints are retained where supported by GORM's models and migration APIs. Application-level validation does not replace database-level integrity guarantees.

- **In-scope backends: PostgreSQL, MySQL and MariaDB.** These are the backends Baleia commits to testing and supporting, not the limit of the ORM: GORM also ships dialectors for SQLite, SQL Server, TiDB, ClickHouse, Oracle and GaussDB, which stay out of scope for this decision. GORM's PostgreSQL dialector handles PostgreSQL. Its MySQL dialector handles MySQL and MariaDB. The configured backend selects the appropriate dialector and driver; connection strings alone do not automatically select the GORM dialector.

- **Database-specific behavior is isolated.** Differences in type mapping and supported schema operations are handled by GORM's dialectors and drivers. Custom GORM data types may be introduced when necessary, provided they do not require handwritten SQL or create a second schema definition.

- **Migration order:** models first — identity & access, workspaces & content, connections/engines/catalogs, semantic & lineage, monitoring & ops, audit/agents/retrievers — then the queries and persistence operations of each domain over those models (#769–#774).

## Consequences

- **Easier:** model-oriented persistence; centralized schema declarations; built-in associations and lifecycle hooks; less handwritten persistence code; no sqlc-generated queries; no SQL migration files; dialect resolution delegated to GORM; a consistent persistence API across PostgreSQL, MySQL and MariaDB.

- **Harder:** GORM introduces conventions and lifecycle behavior that must be understood and deliberately controlled; model relationships and automatic association persistence require careful configuration; GORM's `AutoMigrate` has limitations and cannot replace explicit schema evolution; versioned Go migration infrastructure must be maintained by Baleia; backend differences still require integration testing.

- **Integrity:** constraints and indexes supported by GORM remain part of the schema. Features that cannot be represented without handwritten SQL are redesigned or removed. Application-level validation must not be treated as a replacement for database-enforced uniqueness or referential integrity.

- **Portability:** using the same ORM does not guarantee identical behavior across database engines. Types, indexes, defaults, transactions and migration capabilities differ. PostgreSQL, MySQL and MariaDB must all be covered by integration tests.

- **Must now be true:** no handwritten SQL in the store implementation; no sqlc, goose or direct `pgx` API usage in the store path; models are the authoritative schema definition; migrations are versioned Go code using GORM's schema APIs; persistence operations use GORM; an operation GORM cannot express without handwritten SQL is a design problem, not a reason to introduce raw SQL.

## Current compliance

Implemented. The store is GORM-only: the models are the schema, the versioned Go migrations in `internal/store` create and evolve it, and every persistence operation goes through the GORM store. `sqlc`, goose, `db/queries`, `db/migrations` and the direct `pgx` API are gone from the store path; `internal/db` keeps only hand-maintained DTO/contract types.

The query-execution path (Trino and the direct engines) still keeps its own dialect layer and is intentionally outside the store, as stated above.

This ADR was implemented by:

- **#759:** GORM models, database initialization, versioned Go migration infrastructure and model-driven schema creation.
- **#769–#774:** each domain's persistence operations migrated to GORM (accounts, service accounts, directory provisioning, content, workbench, connections/engines/catalogs, semantic/metadata/lineage, monitoring/ops, audit/agents/retrievers, assistant).
- **#761:** the cut: generated sqlc code, goose, SQL queries and SQL migrations removed; the runtime and DB-backed tests run on GORM only.

PostgreSQL, MySQL and MariaDB are supported through GORM's dialectors. Features GORM cannot express without handwritten SQL were removed or redesigned: PL/pgSQL functions and triggers (roles and effective membership computed in Go), `pg_trgm` (a portable Go search), and advisory locks (transaction/row locks instead). Table partitioning, originally removed here, was re-introduced by [0024](0024-cold-storage-and-retention.md) behind a `PartitionManager` seam; native range partitioning is supported on the engines that provide it, the PostgreSQL adapter ships first, SQLite has no native partitioning and is unsupported, and the remaining adapters report not-implemented rather than pretending parity.

## Alternatives considered

- **Keep sqlc/pgx and make the SQL portable.** Rejected: it keeps handwritten SQL in the relationship with the store, which the owner forbids.

- **Keep goose for migrations.** Rejected: migrations would remain SQL files and schema operations would bypass the ORM.

- **Bun.** Rejected: Bun provides an explicit SQL-first query builder and model mapping, but its persistence API is not aligned with the desired Active Record-inspired, model-centric approach. Baleia requires GORM's broader model lifecycle, associations, callbacks and persistence conventions.

- **Transpile existing DDL with sqlglot or a handwritten translator.** Rejected: it keeps SQL as the source of truth and turns the translation into a second schema to maintain.

- **Pop / Buffalo.** Rejected: adopting the Buffalo ecosystem and its associated tooling introduces framework-level dependencies and conventions outside the scope of Baleia's persistence requirements.

- **ent.** Rejected: excluded by the project owners on ethical grounds due to the maintainers' publicly expressed support for Zionism and Israel's genocidal actions in Gaza. The project will not adopt software maintained by this organization.

- **MongoDB.** Out of scope: a document store is a different repository and does not satisfy the relational store requirements.