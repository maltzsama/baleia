package model

import (
	"time"

	"gorm.io/gorm"
)

// Space is the spaces table.
type Space struct {
	ID           UUID       `gorm:"column:id;primaryKey;not null;uniqueIndex:spaces_workspace_id_id_key,priority:2;check:spaces_name_check,(((length(name) >= 1) AND (length(name) <= 120)))"`
	WorkspaceID  UUID       `gorm:"column:workspace_id;not null;uniqueIndex:spaces_workspace_id_name_key,priority:1;uniqueIndex:spaces_workspace_id_id_key,priority:1;uniqueIndex:spaces_slug_uq,priority:1;uniqueIndex:spaces_default_guard_uq,priority:1;check:spaces_visibility_check,((visibility IN ('workspace', 'restricted')))"`
	Name         Text       `gorm:"column:name;not null;size:128;uniqueIndex:spaces_workspace_id_name_key,priority:2;check:spaces_default_role_check,((default_role IN ('viewer', 'editor')))"`
	Description  *string    `gorm:"column:description;check:spaces_check,(((NOT is_default) OR (visibility = 'workspace')))"`
	Visibility   string     `gorm:"column:visibility;not null"`
	DefaultRole  Text       `gorm:"column:default_role;not null;size:255;default:'viewer'"`
	IsDefault    bool       `gorm:"column:is_default;not null;default:false"`
	CreatedBy    *UUID      `gorm:"column:created_by"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Slug         Text       `gorm:"column:slug;not null;size:128;uniqueIndex:spaces_slug_uq,priority:2"`
	DefaultGuard *bool      `gorm:"column:default_guard;uniqueIndex:spaces_default_guard_uq,priority:2"`
	Workspace    *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	CreatedByRef *User      `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (Space) TableName() string { return "spaces" }

// BeforeCreate derives DefaultGuard from is_default: true on a workspace's
// default space, NULL on every other. spaces_default_guard_uq is a unique key over
// workspace_id and the guard, so a workspace holds one default space on every
// engine, because MySQL and MariaDB have no partial indexes and NULLs never
// collide in a unique key. It runs on Create only: a map update through
// Model(&Space{}) would run an update hook on a zero value.
func (s *Space) BeforeCreate(*gorm.DB) error {
	s.DefaultGuard = guard(s.IsDefault)
	return nil
}

// SpaceMember is the space_members table.
type SpaceMember struct {
	SpaceID       UUID      `gorm:"column:space_id;primaryKey;not null;check:space_members_principal_type_check,((principal_type IN ('user', 'team')))"`
	PrincipalType Text      `gorm:"column:principal_type;primaryKey;not null;size:128;index:space_members_principal,priority:1;check:space_members_role_check,((role IN ('viewer', 'editor', 'manager')))"`
	PrincipalID   UUID      `gorm:"column:principal_id;primaryKey;not null;index:space_members_principal,priority:2"`
	Role          string    `gorm:"column:role;not null"`
	GrantedBy     *UUID     `gorm:"column:granted_by"`
	GrantedAt     time.Time `gorm:"column:granted_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Space         *Space    `gorm:"foreignKey:SpaceID;references:ID;constraint:OnDelete:CASCADE"`
	GrantedByRef  *User     `gorm:"foreignKey:GrantedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (SpaceMember) TableName() string { return "space_members" }

// SavedQuery is the saved_queries table.
type SavedQuery struct {
	ID              UUID            `gorm:"column:id;primaryKey;not null"`
	WorkspaceID     UUID            `gorm:"column:workspace_id;not null;uniqueIndex:saved_queries_slug_uq,priority:1;index:idx_saved_queries_workspace,priority:1;uniqueIndex:idx_saved_queries_unique_name,priority:1"`
	FolderID        *UUID           `gorm:"column:folder_id;index:idx_saved_queries_folder,priority:1"`
	CreatedBy       UUID            `gorm:"column:created_by;not null;index:idx_saved_queries_created_by,priority:1"`
	Name            Text            `gorm:"column:name;not null;size:128;uniqueIndex:idx_saved_queries_unique_name,priority:2"`
	SQL             string          `gorm:"column:sql;not null"`
	CreatedAt       time.Time       `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt       time.Time       `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	SpaceID         UUID            `gorm:"column:space_id;not null;index:saved_queries_space,priority:1"`
	UpdatedBy       *UUID           `gorm:"column:updated_by"`
	TargetBindingID *UUID           `gorm:"column:target_binding_id"`
	TargetSchema    *string         `gorm:"column:target_schema"`
	Slug            Text            `gorm:"column:slug;not null;size:128;uniqueIndex:saved_queries_slug_uq,priority:2"`
	Workspace       *Workspace      `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	CreatedByRef    *User           `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:RESTRICT"`
	UpdatedByRef    *User           `gorm:"foreignKey:UpdatedBy;references:ID;constraint:OnDelete:SET NULL"`
	TargetBinding   *CatalogBinding `gorm:"foreignKey:TargetBindingID;references:ID;constraint:OnDelete:SET NULL"`
	SpaceRef        *Space          `gorm:"foreignKey:WorkspaceID,SpaceID;references:WorkspaceID,ID;constraint:OnDelete:CASCADE"`
}

func (SavedQuery) TableName() string { return "saved_queries" }

// QueryFolder is the query_folders table.
type QueryFolder struct {
	ID          UUID         `gorm:"column:id;primaryKey;not null;uniqueIndex:query_folders_space_id_uq,priority:2"`
	WorkspaceID UUID         `gorm:"column:workspace_id;not null;index:idx_query_folders_workspace,priority:1"`
	ParentID    *UUID        `gorm:"column:parent_id;index:idx_query_folders_parent,priority:1"`
	Name        string       `gorm:"column:name;not null"`
	CreatedAt   time.Time    `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	SpaceID     UUID         `gorm:"column:space_id;not null;uniqueIndex:query_folders_space_id_uq,priority:1;uniqueIndex:query_folders_slug_uq,priority:1;index:query_folders_space,priority:1"`
	Slug        Text         `gorm:"column:slug;not null;size:128;uniqueIndex:query_folders_slug_uq,priority:2"`
	Workspace   *Workspace   `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	SpaceRef    *Space       `gorm:"foreignKey:WorkspaceID,SpaceID;references:WorkspaceID,ID;constraint:OnDelete:CASCADE"`
	Parent      *QueryFolder `gorm:"foreignKey:SpaceID,ParentID;references:SpaceID,ID;constraint:OnDelete:CASCADE"`
}

func (QueryFolder) TableName() string { return "query_folders" }

// QueryExecution is the query_executions table.
type QueryExecution struct {
	ID              UUID            `gorm:"column:id;primaryKey;not null;index:query_executions_ws_started,priority:3;check:query_executions_status_check,((status IN ('running', 'finished', 'failed', 'cancelled', 'queued')))"`
	WorkspaceID     UUID            `gorm:"column:workspace_id;not null;index:idx_query_executions_workspace,priority:1;index:query_executions_ws_started,priority:1;index:idx_query_executions_ws_origin_status,priority:1;check:query_executions_queue_class_check,(((queue_class IS NULL) OR (queue_class IN ('interactive', 'heavy', 'background'))))"`
	UserID          UUID            `gorm:"column:user_id;not null;index:idx_query_executions_user,priority:1"`
	SavedQueryID    *UUID           `gorm:"column:saved_query_id"`
	Status          Text            `gorm:"column:status;not null;size:128;index:idx_query_executions_status,priority:1;index:idx_query_executions_bi_key_status,priority:2;index:idx_query_executions_ws_origin_status,priority:3"`
	Origin          Text            `gorm:"column:origin;not null;size:32;default:'editor';index:idx_query_executions_ws_origin_status,priority:2"`
	BIKeyID         *UUID           `gorm:"column:bi_key_id;index:idx_query_executions_bi_key_status,priority:1"`
	SQL             string          `gorm:"column:sql;not null"`
	EngineQueryID   *string         `gorm:"column:engine_query_id"`
	RowsReturned    *int64          `gorm:"column:rows_returned"`
	DurationMs      *int64          `gorm:"column:duration_ms"`
	StartedAt       time.Time       `gorm:"column:started_at;primaryKey;not null;default:CURRENT_TIMESTAMP(3);index:query_executions_ws_started,priority:2;index:idx_query_executions_agent_started,priority:2"`
	FinishedAt      *time.Time      `gorm:"column:finished_at"`
	TrinoCatalog    *string         `gorm:"column:trino_catalog"`
	ResultKey       *string         `gorm:"column:result_key"`
	RequestID       *string         `gorm:"column:request_id"`
	EngineID        *UUID           `gorm:"column:engine_id"`
	TargetBindingID *UUID           `gorm:"column:target_binding_id"`
	TargetCatalogID *UUID           `gorm:"column:target_catalog_id"`
	TargetSchema    *string         `gorm:"column:target_schema"`
	TargetSnapshot  *JSON           `gorm:"column:target_snapshot"`
	AgentID         *UUID           `gorm:"column:agent_id;index:idx_query_executions_agent,priority:1;index:idx_query_executions_agent_started,priority:1"`
	BIKey           *APIKey         `gorm:"foreignKey:BIKeyID;references:ID;constraint:OnDelete:SET NULL"`
	QueueClass      *string         `gorm:"column:queue_class"`
	QueueReason     *string         `gorm:"column:queue_reason"`
	FairnessKey     *string         `gorm:"column:fairness_key"`
	QueuedAt        *time.Time      `gorm:"column:queued_at"`
	AdmittedAt      *time.Time      `gorm:"column:admitted_at"`
	QueueWaitMs     *int64          `gorm:"column:queue_wait_ms"`
	ErrorCode       *string         `gorm:"column:error_code"`
	Workspace       *Workspace      `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	User            *User           `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:RESTRICT"`
	SavedQuery      *SavedQuery     `gorm:"foreignKey:SavedQueryID;references:ID;constraint:OnDelete:SET NULL"`
	Engine          *Engine         `gorm:"foreignKey:EngineID;references:ID;constraint:OnDelete:SET NULL"`
	TargetBinding   *CatalogBinding `gorm:"foreignKey:TargetBindingID;references:ID;constraint:OnDelete:SET NULL"`
	TargetCatalog   *Catalog        `gorm:"foreignKey:TargetCatalogID;references:ID;constraint:OnDelete:SET NULL"`
	Agent           *Agent          `gorm:"foreignKey:AgentID;references:ID;constraint:OnDelete:SET NULL"`
}

func (QueryExecution) TableName() string { return "query_executions" }

// QueryStatement is the query_statements table.
type QueryStatement struct {
	ID          UUID `gorm:"column:id;primaryKey;not null;check:query_statements_status_check,((status IN ('pending', 'running', 'finished', 'failed', 'cancelled', 'skipped')))"`
	ExecutionID UUID `gorm:"column:execution_id;not null;index:idx_query_statements_execution,priority:1"`
	// ExecutionStartedAt is the parent execution's started_at, denormalized so
	// the statement stays inside the parent's month partition (ADR 0024).
	ExecutionStartedAt time.Time       `gorm:"column:execution_started_at;primaryKey;not null"`
	Position           int32           `gorm:"column:position;not null"`
	SQL                string          `gorm:"column:sql;not null"`
	Status             string          `gorm:"column:status;not null"`
	EngineQueryID      *string         `gorm:"column:engine_query_id"`
	RowsReturned       *int64          `gorm:"column:rows_returned"`
	DurationMs         *int64          `gorm:"column:duration_ms"`
	ErrorMessage       *string         `gorm:"column:error_message"`
	StartedAt          time.Time       `gorm:"column:started_at;not null;default:CURRENT_TIMESTAMP(3)"`
	FinishedAt         *time.Time      `gorm:"column:finished_at"`
	ResultKey          *string         `gorm:"column:result_key"`
	ResultBytes        *int64          `gorm:"column:result_bytes"`
	Truncated          bool            `gorm:"column:truncated;not null;default:false"`
	QueryHandle        *JSON           `gorm:"column:query_handle"`
	ResultSets         JSON            `gorm:"column:result_sets;not null;default:('[]')"`
	ErrorCode          *string         `gorm:"column:error_code"`
	Execution          *QueryExecution `gorm:"foreignKey:ExecutionID,ExecutionStartedAt;references:ID,StartedAt;constraint:OnDelete:CASCADE"`
}

func (QueryStatement) TableName() string { return "query_statements" }

// ContentVersion is the content_versions table.
type ContentVersion struct {
	ID           int64      `gorm:"column:id;primaryKey;autoIncrement;check:content_versions_version_check,((version >= 1))"`
	WorkspaceID  UUID       `gorm:"column:workspace_id;not null;index:content_versions_ws_time,priority:1;index:content_versions_deleted,priority:1,where:change_kind = 'delete';check:content_versions_change_kind_check,((change_kind IN ('create', 'update', 'delete', 'restore', 'import')))"`
	SpaceID      UUID       `gorm:"column:space_id;not null;check:content_versions_entity_type_check,((entity_type IN ('saved_query', 'semantic_metric', 'semantic_model')))"`
	EntityType   Text       `gorm:"column:entity_type;not null;size:128;uniqueIndex:content_versions_entity_type_entity_id_version_key,priority:1;index:content_versions_entity,priority:1;index:content_versions_deleted,priority:2"`
	EntityID     UUID       `gorm:"column:entity_id;not null;uniqueIndex:content_versions_entity_type_entity_id_version_key,priority:2;index:content_versions_entity,priority:2"`
	Version      int32      `gorm:"column:version;not null;uniqueIndex:content_versions_entity_type_entity_id_version_key,priority:3;index:content_versions_entity,priority:3"`
	ChangeKind   string     `gorm:"column:change_kind;not null"`
	Snapshot     JSON       `gorm:"column:snapshot;not null"`
	AuthorID     *UUID      `gorm:"column:author_id"`
	RestoredFrom *int32     `gorm:"column:restored_from"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3);index:content_versions_ws_time,priority:2;index:content_versions_deleted,priority:3"`
	Workspace    *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	Author       *User      `gorm:"foreignKey:AuthorID;references:ID;constraint:OnDelete:SET NULL"`
}

func (ContentVersion) TableName() string { return "content_versions" }

// StatementReference is the statement_references table.
type StatementReference struct {
	StatementID UUID `gorm:"column:statement_id;primaryKey;not null;index:statement_references_statement,priority:1;check:statement_references_kind_check,((kind IN ('read', 'write')))"`
	// ExecutionStartedAt is the statement's execution_started_at, so the
	// composite foreign key to query_statements has its partition key (ADR 0024).
	ExecutionStartedAt time.Time       `gorm:"column:execution_started_at;not null"`
	WorkspaceID        UUID            `gorm:"column:workspace_id;not null;index:statement_references_workspace_table,priority:1"`
	CatalogID          *UUID           `gorm:"column:catalog_id"`
	Namespace          Text            `gorm:"column:namespace;primaryKey;not null;size:128;default:'';index:statement_references_workspace_table,priority:2"`
	TableNameValue     Text            `gorm:"column:table_name;primaryKey;not null;size:128;index:statement_references_workspace_table,priority:3"`
	Alias              Text            `gorm:"column:alias;primaryKey;not null;size:128;default:''"`
	Kind               Text            `gorm:"column:kind;not null;size:255;default:'read'"`
	Statement          *QueryStatement `gorm:"foreignKey:StatementID,ExecutionStartedAt;references:ID,ExecutionStartedAt;constraint:OnDelete:CASCADE"`
	Workspace          *Workspace      `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	Catalog            *Catalog        `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:SET NULL"`
}

func (StatementReference) TableName() string { return "statement_references" }

// StatementJoin is the statement_joins table.
type StatementJoin struct {
	StatementID UUID `gorm:"column:statement_id;primaryKey;not null"`
	// ExecutionStartedAt is the statement's execution_started_at (ADR 0024).
	ExecutionStartedAt time.Time       `gorm:"column:execution_started_at;not null"`
	WorkspaceID        UUID            `gorm:"column:workspace_id;not null;index:statement_joins_workspace,priority:1"`
	LeftAlias          Text            `gorm:"column:left_alias;primaryKey;not null;size:128"`
	LeftColumn         Text            `gorm:"column:left_column;primaryKey;not null;size:128"`
	RightAlias         Text            `gorm:"column:right_alias;primaryKey;not null;size:128"`
	RightColumn        Text            `gorm:"column:right_column;primaryKey;not null;size:128"`
	Statement          *QueryStatement `gorm:"foreignKey:StatementID,ExecutionStartedAt;references:ID,ExecutionStartedAt;constraint:OnDelete:CASCADE"`
	Workspace          *Workspace      `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
}

func (StatementJoin) TableName() string { return "statement_joins" }
