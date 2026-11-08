package model

import "time"

// AuditEvent is the audit_events table.
type AuditEvent struct {
	ID           int64     `gorm:"column:id;primaryKey;autoIncrement;check:audit_events_actor_kind_check,((actor_kind = ANY (ARRAY['user'::text, 'service'::text, 'agent'::text, 'anonymous'::text])))"`
	OccurredAt   time.Time `gorm:"column:occurred_at;not null;default:CURRENT_TIMESTAMP(3);index:audit_events_resource,priority:3;index:audit_events_actor,priority:2;index:audit_events_ws,priority:2;index:audit_events_on_behalf_of,priority:2;check:audit_events_action_check,((action ~ '^[a-z_]+\\.[a-z_]+$'::text))"`
	ActorKind    string    `gorm:"column:actor_kind;not null;check:audit_events_outcome_check,((outcome = ANY (ARRAY['allowed'::text, 'denied'::text, 'error'::text])))"`
	ActorID      *Text     `gorm:"column:actor_id;size:128;index:audit_events_actor,priority:1"`
	AuthMethod   *string   `gorm:"column:auth_method"`
	Action       string    `gorm:"column:action;not null"`
	ResourceType Text      `gorm:"column:resource_type;not null;size:128;index:audit_events_resource,priority:1"`
	ResourceID   *Text     `gorm:"column:resource_id;size:128;index:audit_events_resource,priority:2"`
	WorkspaceID  *UUID     `gorm:"column:workspace_id;index:audit_events_ws,priority:1"`
	Outcome      string    `gorm:"column:outcome;not null"`
	Reason       *string   `gorm:"column:reason"`
	RequestID    *Text     `gorm:"column:request_id;size:128;index:idx_audit_events_request_id,priority:1,where:request_id IS NOT NULL"`
	TraceID      *Text     `gorm:"column:trace_id;size:128;index:idx_audit_events_trace_id,priority:1,where:trace_id IS NOT NULL"`
	ClientIP     *Inet     `gorm:"column:client_ip"`
	UserAgent    *string   `gorm:"column:user_agent"`
	Changes      *JSON     `gorm:"column:changes"`
	OnBehalfOf   *UUID     `gorm:"column:on_behalf_of;index:audit_events_on_behalf_of,priority:1,where:on_behalf_of IS NOT NULL"`
}

func (AuditEvent) TableName() string { return "audit_events" }

// AuditExportCursor is the audit_export_cursor table.
type AuditExportCursor struct {
	Sink       Text      `gorm:"column:sink;primaryKey;not null;size:128"`
	OccurredAt time.Time `gorm:"column:occurred_at;not null"`
	ID         int64     `gorm:"column:id;not null"`
	UpdatedAt  time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
}

func (AuditExportCursor) TableName() string { return "audit_export_cursor" }

// Retriever is the retrievers table.
type Retriever struct {
	ID            UUID            `gorm:"column:id;primaryKey;not null;default:gen_random_uuid();check:retrievers_refresh_type_check,((refresh_type = ANY (ARRAY['incremental'::text, 'full'::text])))"`
	WorkspaceID   UUID            `gorm:"column:workspace_id;not null;uniqueIndex:retrievers_workspace_id_name_key,priority:1;index:idx_retrievers_workspace,priority:1;check:retrievers_status_check,((status = ANY (ARRAY['active'::text, 'paused'::text, 'error'::text])))"`
	CreatedBy     UUID            `gorm:"column:created_by;not null"`
	Name          Text            `gorm:"column:name;not null;size:128;uniqueIndex:retrievers_workspace_id_name_key,priority:2"`
	DefinitionSQL string          `gorm:"column:definition_sql;not null"`
	DestCatalog   string          `gorm:"column:dest_catalog;not null"`
	DestNamespace string          `gorm:"column:dest_namespace;not null"`
	DestTable     string          `gorm:"column:dest_table;not null"`
	CronSchedule  string          `gorm:"column:cron_schedule;not null"`
	RefreshType   Text            `gorm:"column:refresh_type;not null;size:255;default:'incremental'"`
	Status        Text            `gorm:"column:status;not null;size:128;default:'active';index:idx_retrievers_status,priority:1"`
	LastRefreshAt *time.Time      `gorm:"column:last_refresh_at"`
	NextRefreshAt *time.Time      `gorm:"column:next_refresh_at;index:idx_retrievers_next_refresh_at,priority:1"`
	CreatedAt     time.Time       `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt     time.Time       `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	DestBindingID *UUID           `gorm:"column:dest_binding_id;index:idx_retrievers_dest_binding,priority:1"`
	ErrorCode     *string         `gorm:"column:error_code"`
	Workspace     *Workspace      `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	CreatedByRef  *User           `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:RESTRICT"`
	DestBinding   *CatalogBinding `gorm:"foreignKey:DestBindingID;references:ID;constraint:OnDelete:SET NULL"`
}

func (Retriever) TableName() string { return "retrievers" }

// RetrieverRefreshHistory is the retriever_refresh_history table.
type RetrieverRefreshHistory struct {
	ID                 UUID       `gorm:"column:id;primaryKey;not null;default:gen_random_uuid();check:retriever_refresh_history_refresh_type_check,((refresh_type = ANY (ARRAY['incremental'::text, 'full'::text])))"`
	RetrieverID        UUID       `gorm:"column:retriever_id;not null;index:idx_retriever_refresh_retriever,priority:1;check:retriever_refresh_history_status_check,((status = ANY (ARRAY['running'::text, 'finished'::text, 'failed'::text])))"`
	TemporalWorkflowID string     `gorm:"column:temporal_workflow_id;not null"`
	RefreshType        string     `gorm:"column:refresh_type;not null"`
	Status             Text       `gorm:"column:status;not null;size:128;index:idx_retriever_refresh_status,priority:1"`
	RowsProcessed      *int64     `gorm:"column:rows_processed"`
	DurationMs         *int64     `gorm:"column:duration_ms"`
	ErrorMessage       *string    `gorm:"column:error_message"`
	StartedAt          time.Time  `gorm:"column:started_at;not null;default:CURRENT_TIMESTAMP(3)"`
	FinishedAt         *time.Time `gorm:"column:finished_at"`
	ErrorCode          *string    `gorm:"column:error_code"`
	Retriever          *Retriever `gorm:"foreignKey:RetrieverID;references:ID;constraint:OnDelete:CASCADE"`
}

func (RetrieverRefreshHistory) TableName() string { return "retriever_refresh_history" }

// Migration is the migrations table.
type Migration struct {
	ID                        UUID               `gorm:"column:id;primaryKey;not null;default:gen_random_uuid();check:migrations_source_type_check,((source_type = 'nessie'::text))"`
	CreatedBy                 UUID               `gorm:"column:created_by;not null;index:idx_migrations_created_by,priority:1;check:migrations_status_check,((status = ANY (ARRAY['pending'::text, 'running'::text, 'finished'::text, 'failed'::text, 'cancelled'::text])))"`
	Name                      string             `gorm:"column:name;not null"`
	SourceType                string             `gorm:"column:source_type;not null"`
	SourceURL                 string             `gorm:"column:source_url;not null"`
	SourceCredentialsEnc      *string            `gorm:"column:source_credentials_enc"`
	TargetCatalogConnectionID UUID               `gorm:"column:target_catalog_connection_id;not null"`
	Status                    Text               `gorm:"column:status;not null;size:128;default:'pending';index:idx_migrations_status,priority:1"`
	TotalTables               int32              `gorm:"column:total_tables;not null;default:0"`
	MigratedTables            int32              `gorm:"column:migrated_tables;not null;default:0"`
	FailedTables              int32              `gorm:"column:failed_tables;not null;default:0"`
	TemporalWorkflowID        *string            `gorm:"column:temporal_workflow_id"`
	StartedAt                 *time.Time         `gorm:"column:started_at"`
	FinishedAt                *time.Time         `gorm:"column:finished_at"`
	CreatedAt                 time.Time          `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	CreatedByRef              *User              `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:RESTRICT"`
	TargetCatalogConnection   *CatalogConnection `gorm:"foreignKey:TargetCatalogConnectionID;references:ID;constraint:OnDelete:RESTRICT"`
}

func (Migration) TableName() string { return "migrations" }

// MigrationItem is the migration_items table.
type MigrationItem struct {
	ID              UUID       `gorm:"column:id;primaryKey;not null;default:gen_random_uuid();check:migration_items_status_check,((status = ANY (ARRAY['pending'::text, 'running'::text, 'finished'::text, 'failed'::text, 'skipped'::text])))"`
	MigrationID     UUID       `gorm:"column:migration_id;not null;index:idx_migration_items_migration,priority:1"`
	SourceNamespace string     `gorm:"column:source_namespace;not null"`
	SourceTable     string     `gorm:"column:source_table;not null"`
	TargetNamespace string     `gorm:"column:target_namespace;not null"`
	TargetTable     string     `gorm:"column:target_table;not null"`
	Status          Text       `gorm:"column:status;not null;size:128;default:'pending';index:idx_migration_items_status,priority:1"`
	ErrorMessage    *string    `gorm:"column:error_message"`
	StartedAt       *time.Time `gorm:"column:started_at"`
	FinishedAt      *time.Time `gorm:"column:finished_at"`
	Migration       *Migration `gorm:"foreignKey:MigrationID;references:ID;constraint:OnDelete:CASCADE"`
}

func (MigrationItem) TableName() string { return "migration_items" }
