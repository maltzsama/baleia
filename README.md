# 🐋 Baleia

First-class UI for Trino + Apache Iceberg catalog management platform.

## Stack

- **Frontend:** TypeScript + Svelte 5 + Monaco Editor
- **Backend:** Go (Chi, nhooyr/websocket, sqlc)
- **Orchestration:** Temporal.io
- **Database:** CockroachDB
- **Catalog:** Apache Polaris
- **Query engine:** Trino

## Dev

```bash
# Start local infrastructure
make compose-up

# Run API
make dev

# Run Temporal worker
make worker

# Generate sqlc code
make sqlc
```

```mermaid
erDiagram

  auth_providers {
    uuid id PK
    string name
    string type
    text config_enc
    boolean enabled
    timestamp created_at
    timestamp updated_at
  }

  users {
    uuid id PK
    string name
    string email
    string password_hash
    string role
    string auth_provider
    uuid auth_provider_id FK
    string external_id
    uuid personal_workspace_id FK
    timestamp created_at
    timestamp updated_at
  }

  api_keys {
    uuid id PK
    uuid user_id FK
    string name
    string key_hash
    timestamp expires_at
    timestamp created_at
  }

  teams {
    uuid id PK
    string name
    string description
    uuid created_by FK
    timestamp created_at
    timestamp updated_at
  }

  team_members {
    uuid id PK
    uuid team_id FK
    uuid user_id FK
    string role
    timestamp joined_at
  }

  workspaces {
    uuid id PK
    string name
    string description
    string type
    string polaris_catalog_name
    uuid owner_user_id FK
    uuid owner_team_id FK
    timestamp created_at
    timestamp updated_at
  }

  catalog_connections {
    uuid id PK
    string name
    string type
    string url
    string credentials_enc
    boolean default_readonly
    timestamp created_at
    timestamp updated_at
  }

  workspace_catalog_access {
    uuid id PK
    uuid workspace_id FK
    uuid catalog_connection_id FK
    string permission
    uuid granted_by FK
    timestamp granted_at
  }

  query_folders {
    uuid id PK
    uuid workspace_id FK
    uuid parent_id FK
    string name
    timestamp created_at
  }

  saved_queries {
    uuid id PK
    uuid workspace_id FK
    uuid folder_id FK
    uuid created_by FK
    string name
    text sql
    timestamp created_at
    timestamp updated_at
  }

  query_executions {
    uuid id PK
    uuid workspace_id FK
    uuid user_id FK
    uuid saved_query_id FK
    string status
    text sql
    string trino_query_id
    bigint rows_returned
    bigint duration_ms
    timestamp started_at
    timestamp finished_at
  }

  query_statements {
    uuid id PK
    uuid execution_id FK
    int position
    text sql
    string status
    string trino_query_id
    bigint rows_returned
    bigint duration_ms
    text error_message
    timestamp started_at
    timestamp finished_at
  }

  lenses {
    uuid id PK
    uuid catalog_connection_id FK
    uuid created_by FK
    string name
    string iceberg_catalog
    string iceberg_namespace
    string iceberg_table
    string cron_schedule
    boolean expire_snapshots
    boolean remove_orphan_files
    boolean compact_data_files
    boolean rewrite_manifests
    boolean remove_old_metadata
    string status
    timestamp last_run_at
    timestamp next_run_at
    timestamp created_at
    timestamp updated_at
  }

  lens_run_history {
    uuid id PK
    uuid lens_id FK
    string temporal_workflow_id
    string status
    text operations_summary
    text error_message
    bigint duration_ms
    timestamp started_at
    timestamp finished_at
  }

  retrievers {
    uuid id PK
    uuid workspace_id FK
    uuid created_by FK
    string name
    text definition_sql
    string dest_catalog
    string dest_namespace
    string dest_table
    string cron_schedule
    string refresh_type
    string status
    timestamp last_refresh_at
    timestamp next_refresh_at
    timestamp created_at
    timestamp updated_at
  }

  retriever_refresh_history {
    uuid id PK
    uuid retriever_id FK
    string temporal_workflow_id
    string refresh_type
    string status
    bigint rows_processed
    bigint duration_ms
    text error_message
    timestamp started_at
    timestamp finished_at
  }

  migrations {
    uuid id PK
    uuid created_by FK
    string name
    string source_type
    string source_url
    string source_credentials_enc
    uuid target_catalog_connection_id FK
    string status
    int total_tables
    int migrated_tables
    int failed_tables
    string temporal_workflow_id
    timestamp started_at
    timestamp finished_at
    timestamp created_at
  }

  migration_items {
    uuid id PK
    uuid migration_id FK
    string source_namespace
    string source_table
    string target_namespace
    string target_table
    string status
    text error_message
    timestamp started_at
    timestamp finished_at
  }

  auth_providers ||--o{ users : authenticates
  users ||--|| workspaces : personal_workspace
  users ||--o{ api_keys : has
  users ||--o{ team_members : joins
  teams ||--o{ team_members : has
  teams ||--o{ workspaces : owns
  workspaces ||--o{ workspace_catalog_access : has
  workspaces ||--o{ query_folders : contains
  workspaces ||--o{ saved_queries : contains
  workspaces ||--o{ query_executions : has
  workspaces ||--o{ retrievers : owns
  catalog_connections ||--o{ workspace_catalog_access : granted_to
  catalog_connections ||--o{ lenses : targets
  catalog_connections ||--o{ migrations : target_of
  query_folders ||--o{ query_folders : parent_of
  query_folders ||--o{ saved_queries : contains
  saved_queries ||--o{ query_executions : triggers
  query_executions ||--o{ query_statements : has
  lenses ||--o{ lens_run_history : has
  retrievers ||--o{ retriever_refresh_history : has
  migrations ||--o{ migration_items : has
  users ||--o{ lenses : created_by
  users ||--o{ retrievers : created_by
  users ||--o{ migrations : created_by
  users ||--o{ workspace_catalog_access : grants
```
