// Package db keeps the hand-maintained data-transfer types the store and
// its callers share. They were extracted from the retired generated query
// code and are maintained here from now on.
//
//nolint:revive // DTO fields keep the SQL column spelling and the published JSON contract.
package db

import (
	"net/netip"
)

type AbandonRunningStatementsParams struct {
	ExecutionID  UUID    `json:"execution_id"`
	Status       string  `json:"status"`
	ErrorCode    *string `json:"error_code"`
	ErrorMessage *string `json:"error_message"`
}

type AbandonRunningStatementsRow struct {
	Position    int32  `json:"position"`
	QueryHandle []byte `json:"query_handle"`
}

type AddAgentWorkspaceParams struct {
	AgentID     UUID `json:"agent_id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type AddTeamMemberParams struct {
	TeamID UUID   `json:"team_id"`
	UserID UUID   `json:"user_id"`
	Role   string `json:"role"`
}

type AdvanceRetrieverScheduleParams struct {
	ID            UUID        `json:"id"`
	NextRefreshAt Timestamptz `json:"next_refresh_at"`
}

type AgentWorkspaceBoundParams struct {
	AgentID     UUID `json:"agent_id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type ApplyGroupWorkspaceRolesParams struct {
	UserID      UUID   `json:"user_id"`
	Assignments []byte `json:"assignments"`
}

type CancelQueryExecutionParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type CatalogGrantedToAgentParams struct {
	AgentID   UUID `json:"agent_id"`
	CatalogID UUID `json:"catalog_id"`
	UserID    UUID `json:"user_id"`
}

type CheckWorkspaceAccessParams struct {
	ID     UUID `json:"id"`
	UserID UUID `json:"user_id"`
}

type ClearWorkspaceDefaultForCatalogParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	CatalogID   UUID `json:"catalog_id"`
}

type ConsumeRefreshTokenRow struct {
	ID       UUID `json:"id"`
	UserID   UUID `json:"user_id"`
	FamilyID UUID `json:"family_id"`
}

type CountCatalogAccessImpactParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	CatalogID   UUID `json:"catalog_id"`
}

type CountCatalogAccessImpactRow struct {
	SavedQueries          int64 `json:"saved_queries"`
	Metrics               int64 `json:"metrics"`
	RunningExecutions     int64 `json:"running_executions"`
	DefaultTargetAffected bool  `json:"default_target_affected"`
}

type CountCatalogImpactRow struct {
	Bindings              int64 `json:"bindings"`
	Grants                int64 `json:"grants"`
	SavedQueriesTargeting int64 `json:"saved_queries_targeting"`
	Metrics               int64 `json:"metrics"`
	Docs                  int64 `json:"docs"`
	RunningExecutions     int64 `json:"running_executions"`
}

type CountConnectionImpactRow struct {
	Catalogs          int64 `json:"catalogs"`
	Bindings          int64 `json:"bindings"`
	RunningExecutions int64 `json:"running_executions"`
}

type CountEngineBlockersRow struct {
	ManagedBindings   int64 `json:"managed_bindings"`
	RunningExecutions int64 `json:"running_executions"`
}

type CountEngineImpactRow struct {
	ManagedBindings       int64 `json:"managed_bindings"`
	NativeCatalogs        int64 `json:"native_catalogs"`
	Grants                int64 `json:"grants"`
	Metrics               int64 `json:"metrics"`
	Docs                  int64 `json:"docs"`
	SavedQueriesTargeting int64 `json:"saved_queries_targeting"`
	RunningExecutions     int64 `json:"running_executions"`
}

type CountExecutionsByAgentSinceParams struct {
	AgentID   UUID        `json:"agent_id"`
	StartedAt Timestamptz `json:"started_at"`
}

type CountManagedBindingsByStateRow struct {
	LifecycleState string `json:"lifecycle_state"`
	Count          int64  `json:"count"`
}

type CountSpaceContentKindsRow struct {
	SavedQueries    int64 `json:"saved_queries"`
	SemanticMetrics int64 `json:"semantic_metrics"`
	SemanticModels  int64 `json:"semantic_models"`
	Folders         int64 `json:"folders"`
}

type CountWorkspaceImpactRow struct {
	SavedQueries          int64 `json:"saved_queries"`
	Metrics               int64 `json:"metrics"`
	Spaces                int64 `json:"spaces"`
	RunningExecutions     int64 `json:"running_executions"`
	DefaultTargetAffected bool  `json:"default_target_affected"`
}

type CreateAPIKeyParams struct {
	UserID      UUID        `json:"user_id"`
	Kind        string      `json:"kind"`
	WorkspaceID UUID        `json:"workspace_id"`
	EngineID    UUID        `json:"engine_id"`
	Name        string      `json:"name"`
	KeyHash     string      `json:"key_hash"`
	Prefix      string      `json:"prefix"`
	Scopes      []string    `json:"scopes"`
	ExpiresAt   Timestamptz `json:"expires_at"`
}

type CreateAgentParams struct {
	OwnerID     UUID   `json:"owner_id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type CreateAgentTokenParams struct {
	AgentID   UUID        `json:"agent_id"`
	Name      string      `json:"name"`
	TokenHash string      `json:"token_hash"`
	Prefix    string      `json:"prefix"`
	ExpiresAt Timestamptz `json:"expires_at"`
	CreatedBy UUID        `json:"created_by"`
}

type CreateAuthProviderParams struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	ConfigEnc string `json:"config_enc"`
	Enabled   bool   `json:"enabled"`
}

type CreateCatalogConnectionParams struct {
	ID                  UUID    `json:"id"`
	Name                string  `json:"name"`
	Type                string  `json:"type"`
	AccessMode          *string `json:"access_mode"`
	Url                 string  `json:"url"`
	Config              []byte  `json:"config"`
	CredentialsEnc      *string `json:"credentials_enc"`
	CredentialsReadOnly bool    `json:"credentials_read_only"`
	CreatedBy           UUID    `json:"created_by"`
}

type CreateCatalogGrantRequestParams struct {
	WorkspaceID     UUID   `json:"workspace_id"`
	CatalogID       UUID   `json:"catalog_id"`
	InitiatedBySide string `json:"initiated_by_side"`
	RequestedBy     UUID   `json:"requested_by"`
}

type CreateCatalogParams struct {
	ConnectionID     UUID    `json:"connection_id"`
	Name             string  `json:"name"`
	Description      *string `json:"description"`
	TrinoCatalogName *string `json:"trino_catalog_name"`
	ClusterID        UUID    `json:"cluster_id"`
	Warehouse        *string `json:"warehouse"`
	Connector        *string `json:"connector"`
	ExtraProps       []byte  `json:"extra_props"`
}

type CreateEngineParams struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Class        string `json:"class"`
	ConnectionID UUID   `json:"connection_id"`
	IdentityMode string `json:"identity_mode"`
	CreatedBy    UUID   `json:"created_by"`
}

type CreateMaintenanceRunParams struct {
	CatalogID  UUID     `json:"catalog_id"`
	BindingID  UUID     `json:"binding_id"`
	Namespace  string   `json:"namespace"`
	TableName  string   `json:"table_name"`
	Operations []string `json:"operations"`
	Retention  *string  `json:"retention"`
	StartedBy  UUID     `json:"started_by"`
	WorkflowID string   `json:"workflow_id"`
}

type CreateManagedBindingParams struct {
	CatalogID         UUID    `json:"catalog_id"`
	EngineID          UUID    `json:"engine_id"`
	EngineCatalogName string  `json:"engine_catalog_name"`
	Connector         *string `json:"connector"`
	ExtraProps        []byte  `json:"extra_props"`
	DesiredVersion    *string `json:"desired_version"`
}

type CreateMigrationItemParams struct {
	MigrationID     UUID   `json:"migration_id"`
	SourceNamespace string `json:"source_namespace"`
	SourceTable     string `json:"source_table"`
	TargetNamespace string `json:"target_namespace"`
	TargetTable     string `json:"target_table"`
	Status          string `json:"status"`
}

type CreateMigrationParams struct {
	CreatedBy                 UUID    `json:"created_by"`
	Name                      string  `json:"name"`
	SourceType                string  `json:"source_type"`
	SourceUrl                 string  `json:"source_url"`
	SourceCredentialsEnc      *string `json:"source_credentials_enc"`
	TargetCatalogConnectionID UUID    `json:"target_catalog_connection_id"`
}

type CreateOIDCLoginAttemptParams struct {
	State        string      `json:"state"`
	ProviderID   UUID        `json:"provider_id"`
	Nonce        *string     `json:"nonce"`
	CodeVerifier string      `json:"code_verifier"`
	Redirect     *string     `json:"redirect"`
	ExpiresAt    Timestamptz `json:"expires_at"`
}

type CreatePasswordResetTokenParams struct {
	TokenHash string      `json:"token_hash"`
	UserID    UUID        `json:"user_id"`
	ExpiresAt Timestamptz `json:"expires_at"`
}

type CreatePlannedMaintenanceRunParams struct {
	CatalogID      UUID     `json:"catalog_id"`
	BindingID      UUID     `json:"binding_id"`
	Namespace      string   `json:"namespace"`
	TableName      string   `json:"table_name"`
	Operations     []string `json:"operations"`
	Retention      *string  `json:"retention"`
	Trigger        string   `json:"trigger"`
	StartedBy      UUID     `json:"started_by"`
	WorkflowID     string   `json:"workflow_id"`
	Reason         []byte   `json:"reason"`
	Parameters     []byte   `json:"parameters"`
	PolicyRevision *int64   `json:"policy_revision"`
}

type CreateQueryExecutionParams struct {
	WorkspaceID     UUID    `json:"workspace_id"`
	UserID          UUID    `json:"user_id"`
	SavedQueryID    UUID    `json:"saved_query_id"`
	Status          string  `json:"status"`
	Origin          string  `json:"origin"`
	BIKeyID         UUID    `json:"bi_key_id"`
	Sql             string  `json:"sql"`
	TrinoCatalog    *string `json:"trino_catalog"`
	RequestID       *string `json:"request_id"`
	EngineID        UUID    `json:"engine_id"`
	TargetBindingID UUID    `json:"target_binding_id"`
	TargetCatalogID UUID    `json:"target_catalog_id"`
	TargetSchema    *string `json:"target_schema"`
	TargetSnapshot  []byte  `json:"target_snapshot"`
	AgentID         UUID    `json:"agent_id"`
	QueueClass      *string `json:"queue_class"`
	QueueReason     *string `json:"queue_reason"`
}

type CreateQueryFolderParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	SpaceID     UUID   `json:"space_id"`
	ParentID    UUID   `json:"parent_id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
}

type CreateQueryStatementParams struct {
	ExecutionID UUID   `json:"execution_id"`
	Position    int32  `json:"position"`
	Sql         string `json:"sql"`
	Status      string `json:"status"`
}

type CreateRefreshTokenParams struct {
	UserID    UUID        `json:"user_id"`
	TokenHash string      `json:"token_hash"`
	ExpiresAt Timestamptz `json:"expires_at"`
	FamilyID  UUID        `json:"family_id"`
}

type CreateRetrieverParams struct {
	WorkspaceID   UUID        `json:"workspace_id"`
	CreatedBy     UUID        `json:"created_by"`
	Name          string      `json:"name"`
	DefinitionSql string      `json:"definition_sql"`
	DestCatalog   string      `json:"dest_catalog"`
	DestNamespace string      `json:"dest_namespace"`
	DestTable     string      `json:"dest_table"`
	DestBindingID UUID        `json:"dest_binding_id"`
	CronSchedule  string      `json:"cron_schedule"`
	RefreshType   string      `json:"refresh_type"`
	NextRefreshAt Timestamptz `json:"next_refresh_at"`
}

type CreateRetrieverRefreshHistoryParams struct {
	RetrieverID        UUID   `json:"retriever_id"`
	TemporalWorkflowID string `json:"temporal_workflow_id"`
	RefreshType        string `json:"refresh_type"`
	Status             string `json:"status"`
}

type CreateSavedQueryParams struct {
	WorkspaceID     UUID    `json:"workspace_id"`
	SpaceID         UUID    `json:"space_id"`
	FolderID        UUID    `json:"folder_id"`
	CreatedBy       UUID    `json:"created_by"`
	UpdatedBy       UUID    `json:"updated_by"`
	Name            string  `json:"name"`
	Slug            string  `json:"slug"`
	Sql             string  `json:"sql"`
	TargetBindingID UUID    `json:"target_binding_id"`
	TargetSchema    *string `json:"target_schema"`
}

type CreateSemanticMetricParams struct {
	WorkspaceID   UUID     `json:"workspace_id"`
	SpaceID       UUID     `json:"space_id"`
	Slug          string   `json:"slug"`
	Label         string   `json:"label"`
	Description   string   `json:"description"`
	Certified     bool     `json:"certified"`
	CatalogID     UUID     `json:"catalog_id"`
	Namespace     []string `json:"namespace"`
	TableName     string   `json:"table_name"`
	BindingID     UUID     `json:"binding_id"`
	Definition    []byte   `json:"definition"`
	FormatVersion int32    `json:"format_version"`
	OwnerUserID   UUID     `json:"owner_user_id"`
	UpdatedBy     UUID     `json:"updated_by"`
}

type CreateSemanticModelParams struct {
	WorkspaceID   UUID   `json:"workspace_id"`
	SpaceID       UUID   `json:"space_id"`
	Slug          string `json:"slug"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	Certified     bool   `json:"certified"`
	CatalogID     UUID   `json:"catalog_id"`
	BindingID     UUID   `json:"binding_id"`
	Definition    []byte `json:"definition"`
	FormatVersion int32  `json:"format_version"`
	OwnerUserID   UUID   `json:"owner_user_id"`
	UpdatedBy     UUID   `json:"updated_by"`
}

type CreateServiceAccountParams struct {
	Name            string  `json:"name"`
	Email           string  `json:"email"`
	AuthProvider    string  `json:"auth_provider"`
	AuthProviderID  UUID    `json:"auth_provider_id"`
	ExternalID      *string `json:"external_id"`
	HomeWorkspaceID UUID    `json:"home_workspace_id"`
	Description     *string `json:"description"`
}

type CreateSpaceParams struct {
	WorkspaceID UUID    `json:"workspace_id"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
	Visibility  string  `json:"visibility"`
	DefaultRole string  `json:"default_role"`
	CreatedBy   UUID    `json:"created_by"`
}

type CreateTableMaintenancePolicyParams struct {
	CatalogID                   UUID   `json:"catalog_id"`
	Namespace                   string `json:"namespace"`
	TableName                   string `json:"table_name"`
	Mode                        string `json:"mode"`
	WriterCommitIntervalSeconds int32  `json:"writer_commit_interval_seconds"`
	Optimize                    []byte `json:"optimize"`
	OptimizeManifests           []byte `json:"optimize_manifests"`
	ExpireSnapshots             []byte `json:"expire_snapshots"`
	RemoveOrphanFiles           []byte `json:"remove_orphan_files"`
	Actor                       UUID   `json:"actor"`
}

type CreateTeamParams struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	CreatedBy   UUID    `json:"created_by"`
}

type CreateUserParams struct {
	Name           string  `json:"name"`
	Email          string  `json:"email"`
	PasswordHash   *string `json:"password_hash"`
	Role           string  `json:"role"`
	AuthProvider   string  `json:"auth_provider"`
	AuthProviderID UUID    `json:"auth_provider_id"`
	ExternalID     *string `json:"external_id"`
}

type CreateWorkloadRuleParams struct {
	Position  int32   `json:"position"`
	Enabled   bool    `json:"enabled"`
	Match     []byte  `json:"match"`
	Class     string  `json:"class"`
	MaxClass  *string `json:"max_class"`
	Weight    int32   `json:"weight"`
	CreatedBy UUID    `json:"created_by"`
}

type CreateWorkspaceParams struct {
	Name               string  `json:"name"`
	Description        *string `json:"description"`
	Type               string  `json:"type"`
	PolarisCatalogName string  `json:"polaris_catalog_name"`
	OwnerUserID        UUID    `json:"owner_user_id"`
	OwnerTeamID        UUID    `json:"owner_team_id"`
}

type DeleteAPIKeyParams struct {
	ID     UUID `json:"id"`
	UserID UUID `json:"user_id"`
}

type DeleteCatalogGrantRequestParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	CatalogID   UUID `json:"catalog_id"`
}

type DeleteEphemeralRegistryRowParams struct {
	ClusterID   UUID   `json:"cluster_id"`
	CatalogName string `json:"catalog_name"`
}

type DeleteQueryFolderParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type DeleteRetrieverParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type DeleteSavedQueryParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type DeleteSemanticDocParams struct {
	WorkspaceID UUID     `json:"workspace_id"`
	CatalogID   UUID     `json:"catalog_id"`
	Namespace   []string `json:"namespace"`
	TableName   string   `json:"table_name"`
	ColumnName  *string  `json:"column_name"`
}

type DeleteSemanticMetricParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	ID          UUID `json:"id"`
}

type DeleteSemanticModelParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	ID          UUID `json:"id"`
}

type DeleteSpaceMemberParams struct {
	SpaceID       UUID   `json:"space_id"`
	PrincipalType string `json:"principal_type"`
	PrincipalID   UUID   `json:"principal_id"`
}

type DeleteSpaceParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	ID          UUID `json:"id"`
}

type DeleteTableMaintenancePolicyParams struct {
	CatalogID UUID   `json:"catalog_id"`
	Namespace string `json:"namespace"`
	TableName string `json:"table_name"`
}

type DeleteWorkspaceCatalogGrantParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	CatalogID   UUID `json:"catalog_id"`
}

type DeleteWorkspaceMemberParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	UserID      UUID `json:"user_id"`
}

type DisableMissingNativeBindingsParams struct {
	EngineID UUID     `json:"engine_id"`
	Present  []string `json:"present"`
}

type FailQueryExecutionParams struct {
	ID            UUID    `json:"id"`
	ErrorCode     *string `json:"error_code"`
	EngineQueryID *string `json:"engine_query_id"`
}

type FinishDirectoryReconcileRunParams struct {
	ID         UUID    `json:"id"`
	Outcome    *string `json:"outcome"`
	Seen       int32   `json:"seen"`
	Suspended  int32   `json:"suspended"`
	Reinstated int32   `json:"reinstated"`
	Error      *string `json:"error"`
	Actions    []byte  `json:"actions"`
}

type FinishMetadataSyncRunParams struct {
	ID            UUID    `json:"id"`
	Status        string  `json:"status"`
	TablesSeen    *int32  `json:"tables_seen"`
	TablesChanged *int32  `json:"tables_changed"`
	TablesRemoved *int32  `json:"tables_removed"`
	ErrorCode     *string `json:"error_code"`
	ErrorMessage  *string `json:"error_message"`
}

type FolderSlugExistsParams struct {
	SpaceID UUID   `json:"space_id"`
	Slug    string `json:"slug"`
}

type GetAPIKeyByIDParams struct {
	ID     UUID `json:"id"`
	UserID UUID `json:"user_id"`
}

type GetAgentTokenByIDParams struct {
	ID      UUID `json:"id"`
	AgentID UUID `json:"agent_id"`
}

type GetAppliedCatalogRetentionParams struct {
	EngineID    UUID   `json:"engine_id"`
	CatalogName string `json:"catalog_name"`
}

type GetAppliedCatalogRetentionRow struct {
	Properties []byte `json:"properties"`
	UpdatedBy  string `json:"updated_by"`
}

type GetAuditExportCursorRow struct {
	Stream     string      `json:"stream"`
	OccurredAt Timestamptz `json:"occurred_at"`
	ID         int64       `json:"id"`
	CursorKey  string      `json:"cursor_key"`
}

type GetBindingForTargetRow struct {
	BindingID         UUID   `json:"binding_id"`
	EngineCatalogName string `json:"engine_catalog_name"`
	BindingEnabled    bool   `json:"binding_enabled"`
	Mode              string `json:"mode"`
	EngineID          UUID   `json:"engine_id"`
	EngineName        string `json:"engine_name"`
	EngineKind        string `json:"engine_kind"`
	EngineEnabled     bool   `json:"engine_enabled"`
	CatalogID         UUID   `json:"catalog_id"`
	CatalogName       string `json:"catalog_name"`
	CatalogEnabled    bool   `json:"catalog_enabled"`
}

type GetBindingTargetNamesRow struct {
	CatalogName string `json:"catalog_name"`
	EngineName  string `json:"engine_name"`
}

type GetCatalogBindingForDisplayParams struct {
	CatalogID UUID `json:"catalog_id"`
	BindingID UUID `json:"binding_id"`
}

type GetCatalogBindingForDisplayRow struct {
	ID                UUID   `json:"id"`
	EngineCatalogName string `json:"engine_catalog_name"`
	EngineKind        string `json:"engine_kind"`
}

type GetCatalogByTrinoNameParams struct {
	ClusterID        UUID   `json:"cluster_id"`
	TrinoCatalogName string `json:"trino_catalog_name"`
}

type GetCatalogForSyncRow struct {
	ID             UUID    `json:"id"`
	Name           string  `json:"name"`
	Origin         string  `json:"origin"`
	SourceRef      *string `json:"source_ref"`
	Warehouse      *string `json:"warehouse"`
	Enabled        bool    `json:"enabled"`
	ConnectionID   UUID    `json:"connection_id"`
	ConnectionType string  `json:"connection_type"`
}

type GetCatalogGrantRequestParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	CatalogID   UUID `json:"catalog_id"`
}

type GetCatalogSyncStateRow struct {
	LastStatus    string      `json:"last_status"`
	LastErrorCode string      `json:"last_error_code"`
	SyncedAt      Timestamptz `json:"synced_at"`
}

type GetContentVersionParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	EntityType  string `json:"entity_type"`
	EntityID    UUID   `json:"entity_id"`
	Version     int32  `json:"version"`
}

type GetContentVersionRow struct {
	ID           int64       `json:"id"`
	WorkspaceID  UUID        `json:"workspace_id"`
	SpaceID      UUID        `json:"space_id"`
	EntityType   string      `json:"entity_type"`
	EntityID     UUID        `json:"entity_id"`
	Version      int32       `json:"version"`
	ChangeKind   string      `json:"change_kind"`
	Snapshot     []byte      `json:"snapshot"`
	AuthorID     UUID        `json:"author_id"`
	RestoredFrom *int32      `json:"restored_from"`
	CreatedAt    Timestamptz `json:"created_at"`
	AuthorName   *string     `json:"author_name"`
}

type GetDefaultTrinoEngineRow struct {
	Engine            Engine            `json:"engine"`
	CatalogConnection CatalogConnection `json:"catalog_connection"`
}

type GetEffectiveSpaceRoleParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	SpaceID     UUID `json:"space_id"`
	UserID      UUID `json:"user_id"`
}

type GetEngineWithConnectionRow struct {
	Engine            Engine            `json:"engine"`
	CatalogConnection CatalogConnection `json:"catalog_connection"`
}

type GetFolderBySlugParams struct {
	SpaceID UUID   `json:"space_id"`
	Slug    string `json:"slug"`
}

type GetGrantedCatalogByNameParams struct {
	Name        string `json:"name"`
	WorkspaceID UUID   `json:"workspace_id"`
}

type GetGrantedMetadataCatalogParams struct {
	CatalogID   UUID `json:"catalog_id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type GetGrantedMetadataCatalogRow struct {
	ID               UUID        `json:"id"`
	MetadataSyncedAt Timestamptz `json:"metadata_synced_at"`
}

type GetGrantedMetadataTableParams struct {
	TableID     UUID `json:"table_id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type GetGrantedMetadataTableRow struct {
	ID               UUID        `json:"id"`
	CatalogID        UUID        `json:"catalog_id"`
	Namespace        []string    `json:"namespace"`
	Name             string      `json:"name"`
	Kind             string      `json:"kind"`
	Properties       []byte      `json:"properties"`
	Comment          *string     `json:"comment"`
	LastChangedAt    Timestamptz `json:"last_changed_at"`
	RemovedAt        Timestamptz `json:"removed_at"`
	MetadataSyncedAt Timestamptz `json:"metadata_synced_at"`
}

type GetManagedBindingOfCatalogParams struct {
	CatalogID UUID `json:"catalog_id"`
	EngineID  UUID `json:"engine_id"`
}

type GetManagedConnectionOfEngineCatalogParams struct {
	EngineID          UUID   `json:"engine_id"`
	EngineCatalogName string `json:"engine_catalog_name"`
}

type GetMetadataTableByNameParams struct {
	CatalogID UUID     `json:"catalog_id"`
	Namespace []string `json:"namespace"`
	Name      string   `json:"name"`
}

type GetMetadataTableByRefParams struct {
	CatalogID UUID     `json:"catalog_id"`
	Namespace []string `json:"namespace"`
	Name      string   `json:"name"`
}

type GetMetadataTableByRefRow struct {
	ID        UUID     `json:"id"`
	CatalogID UUID     `json:"catalog_id"`
	Namespace []string `json:"namespace"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
}

type GetQueryExecutionByIDAndWorkspaceParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type GetQueryFolderByIDParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type GetQueryStatementByPositionParams struct {
	ExecutionID UUID  `json:"execution_id"`
	WorkspaceID UUID  `json:"workspace_id"`
	Position    int32 `json:"position"`
}

type GetReconcileRunParams struct {
	EngineID UUID `json:"engine_id"`
	ID       UUID `json:"id"`
}

type GetRetrieverByIDParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type GetRunningMaintenanceRunParams struct {
	CatalogID UUID   `json:"catalog_id"`
	Namespace string `json:"namespace"`
	TableName string `json:"table_name"`
}

type GetSavedQueryByIDParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type GetSavedQueryBySlugParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	Slug        string `json:"slug"`
}

type GetSavedQueryForUpdateParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type GetSemanticDocParams struct {
	WorkspaceID UUID     `json:"workspace_id"`
	CatalogID   UUID     `json:"catalog_id"`
	Namespace   []string `json:"namespace"`
	TableName   string   `json:"table_name"`
	ColumnName  *string  `json:"column_name"`
}

type GetSemanticMetricBySlugParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	Slug        string `json:"slug"`
}

type GetSemanticMetricForUpdateParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type GetSemanticMetricParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	ID          UUID `json:"id"`
}

type GetSemanticModelBySlugParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	Slug        string `json:"slug"`
}

type GetSemanticModelForUpdateParams struct {
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type GetSemanticModelParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	ID          UUID `json:"id"`
}

type GetServiceAccountByClientParams struct {
	AuthProviderID UUID   `json:"auth_provider_id"`
	ExternalID     string `json:"external_id"`
}

type GetSpaceParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	ID          UUID `json:"id"`
}

type GetStatementReferenceTargetParams struct {
	ExecutionID UUID  `json:"execution_id"`
	Position    int32 `json:"position"`
}

type GetStatementReferenceTargetRow struct {
	StatementID UUID `json:"statement_id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type GetTableFingerprintsRow struct {
	Namespace   []string `json:"namespace"`
	Name        string   `json:"name"`
	Fingerprint string   `json:"fingerprint"`
	Properties  []byte   `json:"properties"`
}

type GetTableHealthByTableParams struct {
	CatalogID UUID   `json:"catalog_id"`
	Namespace string `json:"namespace"`
	TableName string `json:"table_name"`
}

type GetTableHealthSnapshotParams struct {
	CatalogConnectionID UUID   `json:"catalog_connection_id"`
	Catalog             string `json:"catalog"`
	Namespace           string `json:"namespace"`
	TableName           string `json:"table_name"`
}

type GetTableMaintenancePolicyParams struct {
	CatalogID UUID   `json:"catalog_id"`
	Namespace string `json:"namespace"`
	TableName string `json:"table_name"`
}

type GetTeamMemberRoleParams struct {
	TeamID UUID `json:"team_id"`
	UserID UUID `json:"user_id"`
}

type GetTrinoCatalogRegistryStateParams struct {
	ClusterID   UUID   `json:"cluster_id"`
	CatalogName string `json:"catalog_name"`
}

type GetTrinoCatalogRegistryStateRow struct {
	CatalogVersion *string `json:"catalog_version"`
	Enabled        bool    `json:"enabled"`
	SyncStatus     string  `json:"sync_status"`
	UpdatedBy      string  `json:"updated_by"`
}

type GetTrinoQuerySnapshotByEngineParams struct {
	EngineID     UUID   `json:"engine_id"`
	TrinoQueryID string `json:"trino_query_id"`
}

type GetTrinoResourceGroupRevisionParams struct {
	EngineID UUID  `json:"engine_id"`
	Revision int32 `json:"revision"`
}

type GetUserByExternalIDParams struct {
	AuthProviderID UUID    `json:"auth_provider_id"`
	ExternalID     *string `json:"external_id"`
}

type GetWorkspaceMemberRoleParams struct {
	ID     UUID `json:"id"`
	UserID UUID `json:"user_id"`
}

type InsertAuditEventParams struct {
	ActorKind    string      `json:"actor_kind"`
	ActorID      *string     `json:"actor_id"`
	AuthMethod   *string     `json:"auth_method"`
	Action       string      `json:"action"`
	ResourceType string      `json:"resource_type"`
	ResourceID   *string     `json:"resource_id"`
	WorkspaceID  UUID        `json:"workspace_id"`
	Outcome      string      `json:"outcome"`
	Reason       *string     `json:"reason"`
	RequestID    *string     `json:"request_id"`
	TraceID      *string     `json:"trace_id"`
	ClientIp     *netip.Addr `json:"client_ip"`
	UserAgent    *string     `json:"user_agent"`
	Changes      []byte      `json:"changes"`
	OnBehalfOf   UUID        `json:"on_behalf_of"`
}

type InsertClusterSnapshotParams struct {
	EngineID         UUID   `json:"engine_id"`
	ActiveWorkers    int32  `json:"active_workers"`
	RunningQueries   int32  `json:"running_queries"`
	QueuedQueries    int32  `json:"queued_queries"`
	BlockedQueries   int32  `json:"blocked_queries"`
	TotalMemoryBytes *int64 `json:"total_memory_bytes"`
	FreeMemoryBytes  *int64 `json:"free_memory_bytes"`
	RawPayload       []byte `json:"raw_payload"`
}

type InsertContentVersionParams struct {
	WorkspaceID  UUID   `json:"workspace_id"`
	SpaceID      UUID   `json:"space_id"`
	EntityType   string `json:"entity_type"`
	EntityID     UUID   `json:"entity_id"`
	Version      int32  `json:"version"`
	ChangeKind   string `json:"change_kind"`
	Snapshot     []byte `json:"snapshot"`
	AuthorID     UUID   `json:"author_id"`
	RestoredFrom *int32 `json:"restored_from"`
}

type InsertEphemeralRegistryRowParams struct {
	ClusterID      UUID     `json:"cluster_id"`
	CatalogName    string   `json:"catalog_name"`
	ConnectorName  string   `json:"connector_name"`
	Column4        []byte   `json:"column_4"`
	CatalogVersion *string  `json:"catalog_version"`
	Ttl            Interval `json:"ttl"`
}

type InsertMetadataColumnsParams struct {
	TableID     UUID     `json:"table_id"`
	TypeSystem  string   `json:"type_system"`
	Ordinals    []int32  `json:"ordinals"`
	Names       []string `json:"names"`
	NativeTypes []string `json:"native_types"`
	Nullables   []bool   `json:"nullables"`
	Comments    []string `json:"comments"`
}

type InsertMetadataForeignKeysParams struct {
	CatalogID   UUID   `json:"catalog_id"`
	TableID     UUID   `json:"table_id"`
	ForeignKeys []byte `json:"foreign_keys"`
}

type InsertReconcileRunParams struct {
	EngineID   UUID        `json:"engine_id"`
	StartedAt  Timestamptz `json:"started_at"`
	FinishedAt Timestamptz `json:"finished_at"`
	DryRun     bool        `json:"dry_run"`
	Actions    []byte      `json:"actions"`
	Adopted    []string    `json:"adopted"`
	Error      *string     `json:"error"`
}

type InsertReconcileRunRow struct {
	ID         UUID        `json:"id"`
	EngineID   UUID        `json:"engine_id"`
	StartedAt  Timestamptz `json:"started_at"`
	FinishedAt Timestamptz `json:"finished_at"`
	DryRun     bool        `json:"dry_run"`
	Actions    []byte      `json:"actions"`
	Adopted    []string    `json:"adopted"`
	Error      *string     `json:"error"`
}

type InsertStatementJoinParams struct {
	StatementID UUID   `json:"statement_id"`
	WorkspaceID UUID   `json:"workspace_id"`
	LeftAlias   string `json:"left_alias"`
	LeftColumn  string `json:"left_column"`
	RightAlias  string `json:"right_alias"`
	RightColumn string `json:"right_column"`
}

type InsertStatementReferenceParams struct {
	StatementID UUID   `json:"statement_id"`
	WorkspaceID UUID   `json:"workspace_id"`
	CatalogID   UUID   `json:"catalog_id"`
	Namespace   string `json:"namespace"`
	TableName   string `json:"table_name"`
	Alias       string `json:"alias"`
	Kind        string `json:"kind"`
}

type InsertTrinoResourceGroupParams struct {
	Name                      string  `json:"name"`
	SoftMemoryLimit           string  `json:"soft_memory_limit"`
	MaxQueued                 int32   `json:"max_queued"`
	SoftConcurrencyLimit      *int32  `json:"soft_concurrency_limit"`
	HardConcurrencyLimit      int32   `json:"hard_concurrency_limit"`
	SchedulingPolicy          *string `json:"scheduling_policy"`
	SchedulingWeight          *int32  `json:"scheduling_weight"`
	JmxExport                 *bool   `json:"jmx_export"`
	SoftCpuLimit              *string `json:"soft_cpu_limit"`
	HardCpuLimit              *string `json:"hard_cpu_limit"`
	HardPhysicalDataScanLimit *string `json:"hard_physical_data_scan_limit"`
	Parent                    *int64  `json:"parent"`
	Environment               *string `json:"environment"`
}

type InsertTrinoResourceGroupRevisionParams struct {
	EngineID UUID   `json:"engine_id"`
	Revision int32  `json:"revision"`
	Config   []byte `json:"config"`
}

type InsertTrinoSelectorParams struct {
	ResourceGroupID        int64   `json:"resource_group_id"`
	Priority               int64   `json:"priority"`
	UserRegex              *string `json:"user_regex"`
	OriginalUserRegex      *string `json:"original_user_regex"`
	AuthenticatedUserRegex *string `json:"authenticated_user_regex"`
	UserGroupRegex         *string `json:"user_group_regex"`
	SourceRegex            *string `json:"source_regex"`
	QueryType              *string `json:"query_type"`
	ClientTags             *string `json:"client_tags"`
}

type IsWorkspaceOwnerParams struct {
	ID     UUID `json:"id"`
	UserID UUID `json:"user_id"`
}

type IsWorkspaceStructuralOwnerParams struct {
	UserID UUID `json:"user_id"`
	ID     UUID `json:"id"`
}

type LastSuccessfulMaintenanceByOperationParams struct {
	CatalogID UUID   `json:"catalog_id"`
	Namespace string `json:"namespace"`
	TableName string `json:"table_name"`
}

type LastSuccessfulMaintenanceByOperationRow struct {
	Operation  string      `json:"operation"`
	FinishedAt Timestamptz `json:"finished_at"`
}

type LatestContentVersionParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	EntityType  string `json:"entity_type"`
	EntityID    UUID   `json:"entity_id"`
}

type LatestReconcileRunsRow struct {
	EngineID   UUID        `json:"engine_id"`
	StartedAt  Timestamptz `json:"started_at"`
	FinishedAt Timestamptz `json:"finished_at"`
	DryRun     bool        `json:"dry_run"`
	Error      *string     `json:"error"`
}

type LatestRetrieverRefreshRow struct {
	Status    string  `json:"status"`
	ErrorCode *string `json:"error_code"`
}

type LinkTrinoClusterEngineParams struct {
	ID       UUID `json:"id"`
	EngineID UUID `json:"engine_id"`
}

type ListAllVisibleQueryFoldersParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	UserID      UUID `json:"user_id"`
	SpaceID     UUID `json:"space_id"`
}

type ListAttentionBindingErrorsRow struct {
	ID             UUID        `json:"id"`
	CatalogID      UUID        `json:"catalog_id"`
	CatalogName    string      `json:"catalog_name"`
	EngineName     string      `json:"engine_name"`
	LifecycleState string      `json:"lifecycle_state"`
	StateChangedAt Timestamptz `json:"state_changed_at"`
}

type ListAttentionMetadataStaleRow struct {
	CatalogID   UUID        `json:"catalog_id"`
	CatalogName string      `json:"catalog_name"`
	Since       Timestamptz `json:"since"`
}

type ListAttentionMetadataSyncFailedRow struct {
	CatalogID   UUID        `json:"catalog_id"`
	CatalogName string      `json:"catalog_name"`
	FinishedAt  Timestamptz `json:"finished_at"`
	ErrorCode   *string     `json:"error_code"`
}

type ListAttentionReconcileDriftRow struct {
	EngineID    UUID        `json:"engine_id"`
	EngineName  string      `json:"engine_name"`
	FinishedAt  Timestamptz `json:"finished_at"`
	ActionCount int32       `json:"action_count"`
}

type ListAttentionRetrieverErrorsRow struct {
	ID            UUID        `json:"id"`
	Name          string      `json:"name"`
	WorkspaceID   UUID        `json:"workspace_id"`
	WorkspaceName string      `json:"workspace_name"`
	UpdatedAt     Timestamptz `json:"updated_at"`
}

type ListAuditEventsForExportParams struct {
	AfterAt Timestamptz `json:"after_at"`
	AfterID int64       `json:"after_id"`
	Before  Timestamptz `json:"before"`
	Lim     int32       `json:"lim"`
}

type ListAuditEventsForExportRow struct {
	OccurredAt   Timestamptz `json:"occurred_at"`
	ID           int64       `json:"id"`
	ActorKind    string      `json:"actor_kind"`
	ActorID      string      `json:"actor_id"`
	AuthMethod   string      `json:"auth_method"`
	Action       string      `json:"action"`
	ResourceType string      `json:"resource_type"`
	ResourceID   string      `json:"resource_id"`
	WorkspaceID  string      `json:"workspace_id"`
	Outcome      string      `json:"outcome"`
	Reason       string      `json:"reason"`
	RequestID    string      `json:"request_id"`
	TraceID      string      `json:"trace_id"`
	ClientIp     string      `json:"client_ip"`
	UserAgent    string      `json:"user_agent"`
	Changes      []byte      `json:"changes"`
	OnBehalfOf   string      `json:"on_behalf_of"`
}

type ListAuditEventsParams struct {
	ResourceType *string     `json:"resource_type"`
	ResourceID   *string     `json:"resource_id"`
	ActorID      *string     `json:"actor_id"`
	Action       *string     `json:"action"`
	Outcome      *string     `json:"outcome"`
	RequestID    *string     `json:"request_id"`
	TraceID      *string     `json:"trace_id"`
	ScopeActor   *string     `json:"scope_actor"`
	ScopeOwner   UUID        `json:"scope_owner"`
	FromTime     Timestamptz `json:"from_time"`
	ToTime       Timestamptz `json:"to_time"`
	CursorAt     Timestamptz `json:"cursor_at"`
	CursorID     *int64      `json:"cursor_id"`
	Lim          int32       `json:"lim"`
}

type ListAuditEventsRow struct {
	ID           int64       `json:"id"`
	OccurredAt   Timestamptz `json:"occurred_at"`
	ActorKind    string      `json:"actor_kind"`
	ActorID      *string     `json:"actor_id"`
	AuthMethod   *string     `json:"auth_method"`
	Action       string      `json:"action"`
	ResourceType string      `json:"resource_type"`
	ResourceID   *string     `json:"resource_id"`
	WorkspaceID  UUID        `json:"workspace_id"`
	Outcome      string      `json:"outcome"`
	Reason       *string     `json:"reason"`
	RequestID    *string     `json:"request_id"`
	TraceID      *string     `json:"trace_id"`
	ClientIp     *netip.Addr `json:"client_ip"`
	UserAgent    *string     `json:"user_agent"`
	Changes      []byte      `json:"changes"`
}

type ListCatalogGrantRequestsForCatalogRow struct {
	WorkspaceID     UUID        `json:"workspace_id"`
	CatalogID       UUID        `json:"catalog_id"`
	InitiatedBySide string      `json:"initiated_by_side"`
	RequestedBy     UUID        `json:"requested_by"`
	RequestedAt     Timestamptz `json:"requested_at"`
	WorkspaceName   string      `json:"workspace_name"`
	WorkspaceType   string      `json:"workspace_type"`
}

type ListCatalogGrantRequestsForWorkspaceRow struct {
	WorkspaceID       UUID        `json:"workspace_id"`
	CatalogID         UUID        `json:"catalog_id"`
	InitiatedBySide   string      `json:"initiated_by_side"`
	RequestedBy       UUID        `json:"requested_by"`
	RequestedAt       Timestamptz `json:"requested_at"`
	CatalogName       string      `json:"catalog_name"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	ConnectionName    string      `json:"connection_name"`
}

type ListCatalogGrantsRow struct {
	WorkspaceID   UUID        `json:"workspace_id"`
	WorkspaceName string      `json:"workspace_name"`
	WorkspaceType string      `json:"workspace_type"`
	GrantedBy     UUID        `json:"granted_by"`
	GrantedAt     Timestamptz `json:"granted_at"`
}

type ListCatalogLastSuccessfulMaintenanceRow struct {
	Namespace  string      `json:"namespace"`
	TableName  string      `json:"table_name"`
	Operation  string      `json:"operation"`
	FinishedAt Timestamptz `json:"finished_at"`
}

type ListCatalogRunningMaintenanceRow struct {
	Namespace string `json:"namespace"`
	TableName string `json:"table_name"`
}

type ListChildNamespacesParams struct {
	CatalogID UUID     `json:"catalog_id"`
	Parent    []string `json:"parent"`
}

type ListClusterSnapshotsByEngineParams struct {
	EngineID UUID        `json:"engine_id"`
	FromTime Timestamptz `json:"from_time"`
	Lim      int32       `json:"lim"`
}

type ListClusterSnapshotsParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

type ListContentVersionsParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	EntityType  string `json:"entity_type"`
	EntityID    UUID   `json:"entity_id"`
	Off         int32  `json:"off"`
	Lim         int32  `json:"lim"`
}

type ListContentVersionsRow struct {
	ID           int64       `json:"id"`
	WorkspaceID  UUID        `json:"workspace_id"`
	SpaceID      UUID        `json:"space_id"`
	EntityType   string      `json:"entity_type"`
	EntityID     UUID        `json:"entity_id"`
	Version      int32       `json:"version"`
	ChangeKind   string      `json:"change_kind"`
	Snapshot     []byte      `json:"snapshot"`
	AuthorID     UUID        `json:"author_id"`
	RestoredFrom *int32      `json:"restored_from"`
	CreatedAt    Timestamptz `json:"created_at"`
	AuthorName   *string     `json:"author_name"`
}

type ListDeletedContentParams struct {
	EntityType  *string `json:"entity_type"`
	Off         int32   `json:"off"`
	Lim         int32   `json:"lim"`
	WorkspaceID UUID    `json:"workspace_id"`
}

type ListDeletedContentRow struct {
	ID           int64       `json:"id"`
	WorkspaceID  UUID        `json:"workspace_id"`
	SpaceID      UUID        `json:"space_id"`
	EntityType   string      `json:"entity_type"`
	EntityID     UUID        `json:"entity_id"`
	Version      int32       `json:"version"`
	ChangeKind   string      `json:"change_kind"`
	Snapshot     []byte      `json:"snapshot"`
	AuthorID     UUID        `json:"author_id"`
	RestoredFrom *int32      `json:"restored_from"`
	CreatedAt    Timestamptz `json:"created_at"`
	AuthorName   *string     `json:"author_name"`
	SpaceName    *string     `json:"space_name"`
}

type ListDirectoryReconcileRunsParams struct {
	Column1 UUID  `json:"column_1"`
	Limit   int32 `json:"limit"`
}

type ListDiscoverableCatalogsRow struct {
	ID                UUID    `json:"id"`
	Name              string  `json:"name"`
	EngineCatalogName string  `json:"engine_catalog_name"`
	Description       *string `json:"description"`
	ConnectionName    string  `json:"connection_name"`
	CreatedBy         UUID    `json:"created_by"`
}

type ListEnabledEnginesWithConnectionRow struct {
	Engine            Engine            `json:"engine"`
	CatalogConnection CatalogConnection `json:"catalog_connection"`
	ClusterID         UUID              `json:"cluster_id"`
}

type ListEnabledIcebergCatalogsRow struct {
	ID               UUID    `json:"id"`
	ConnectionID     UUID    `json:"connection_id"`
	Name             string  `json:"name"`
	TrinoCatalogName string  `json:"trino_catalog_name"`
	SourceRef        *string `json:"source_ref"`
	Warehouse        *string `json:"warehouse"`
}

type ListExecutionHistoryParams struct {
	WorkspaceID UUID        `json:"workspace_id"`
	UserID      UUID        `json:"user_id"`
	Status      *string     `json:"status"`
	QueueClass  *string     `json:"queue_class"`
	FromTime    Timestamptz `json:"from_time"`
	ToTime      Timestamptz `json:"to_time"`
	Q           *string     `json:"q"`
	CatalogID   UUID        `json:"catalog_id"`
	EngineID    UUID        `json:"engine_id"`
	CursorAt    Timestamptz `json:"cursor_at"`
	CursorID    UUID        `json:"cursor_id"`
	Lim         int32       `json:"lim"`
}

type ListExecutionHistoryRow struct {
	ID              UUID        `json:"id"`
	WorkspaceID     UUID        `json:"workspace_id"`
	UserID          UUID        `json:"user_id"`
	UserName        string      `json:"user_name"`
	SavedQueryID    UUID        `json:"saved_query_id"`
	Status          string      `json:"status"`
	Origin          string      `json:"origin"`
	Sql             string      `json:"sql"`
	EngineQueryID   *string     `json:"engine_query_id"`
	RowsReturned    *int64      `json:"rows_returned"`
	DurationMs      *int64      `json:"duration_ms"`
	StartedAt       Timestamptz `json:"started_at"`
	FinishedAt      Timestamptz `json:"finished_at"`
	TrinoCatalog    *string     `json:"trino_catalog"`
	ResultKey       *string     `json:"result_key"`
	RequestID       *string     `json:"request_id"`
	EngineID        UUID        `json:"engine_id"`
	TargetBindingID UUID        `json:"target_binding_id"`
	TargetCatalogID UUID        `json:"target_catalog_id"`
	TargetSchema    *string     `json:"target_schema"`
	TargetSnapshot  []byte      `json:"target_snapshot"`
	QueueClass      *string     `json:"queue_class"`
	QueueReason     *string     `json:"queue_reason"`
	QueuedAt        Timestamptz `json:"queued_at"`
	AdmittedAt      Timestamptz `json:"admitted_at"`
	QueueWaitMs     *int64      `json:"queue_wait_ms"`
}

type ListExpiredEphemeralRegistryRowsRow struct {
	ClusterID   UUID   `json:"cluster_id"`
	CatalogName string `json:"catalog_name"`
}

type ListForeignKeyJoinSuggestionsParams struct {
	LeftTableID    UUID     `json:"left_table_id"`
	RightNamespace []string `json:"right_namespace"`
	RightTable     string   `json:"right_table"`
	RightTableID   UUID     `json:"right_table_id"`
	LeftNamespace  []string `json:"left_namespace"`
	LeftTable      string   `json:"left_table"`
}

type ListForeignKeyJoinSuggestionsRow struct {
	LeftColumn  string `json:"left_column"`
	RightColumn string `json:"right_column"`
}

type ListGrantableWorkspacesRow struct {
	ID       UUID    `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	TeamName *string `json:"team_name"`
}

type ListGrantedCatalogsOfWorkspaceRow struct {
	ID               UUID        `json:"id"`
	ConnectionID     UUID        `json:"connection_id"`
	Name             string      `json:"name"`
	Description      *string     `json:"description"`
	TrinoCatalogName *string     `json:"trino_catalog_name"`
	ClusterID        UUID        `json:"cluster_id"`
	Warehouse        *string     `json:"warehouse"`
	Connector        *string     `json:"connector"`
	ExtraProps       []byte      `json:"extra_props"`
	Enabled          bool        `json:"enabled"`
	CreatedAt        Timestamptz `json:"created_at"`
	UpdatedAt        Timestamptz `json:"updated_at"`
	Origin           string      `json:"origin"`
	SourceRef        *string     `json:"source_ref"`
	Discoverable     bool        `json:"discoverable"`
	ConnectionName   string      `json:"connection_name"`
	ConnectionType   string      `json:"connection_type"`
}

type ListJoinSuggestionsParams struct {
	Lim         int32  `json:"lim"`
	WorkspaceID UUID   `json:"workspace_id"`
	CatalogID   UUID   `json:"catalog_id"`
	LeftNs      string `json:"left_ns"`
	LeftTable   string `json:"left_table"`
	RightNs     string `json:"right_ns"`
	RightTable  string `json:"right_table"`
}

type ListJoinSuggestionsRow struct {
	LeftColumn  string      `json:"left_column"`
	RightColumn string      `json:"right_column"`
	UseCount    int64       `json:"use_count"`
	LastUsedAt  Timestamptz `json:"last_used_at"`
	Score       float64     `json:"score"`
}

type ListLastMaintenancePerOperationParams struct {
	CatalogID  UUID     `json:"catalog_id"`
	Namespaces []string `json:"namespaces"`
	Tables     []string `json:"tables"`
}

type ListLastMaintenancePerOperationRow struct {
	Namespace string      `json:"namespace"`
	TableName string      `json:"table_name"`
	Operation interface{} `json:"operation"`
	LastAt    Timestamptz `json:"last_at"`
}

type ListLastMaintenanceRunsParams struct {
	CatalogID UUID   `json:"catalog_id"`
	Namespace string `json:"namespace"`
	TableName string `json:"table_name"`
}

type ListLastMaintenanceRunsRow struct {
	Operation  interface{} `json:"operation"`
	ID         UUID        `json:"id"`
	Status     string      `json:"status"`
	FinishedAt Timestamptz `json:"finished_at"`
	WorkflowID string      `json:"workflow_id"`
}

type ListLineageBrokenMetricsRow struct {
	ID          UUID        `json:"id"`
	WorkspaceID UUID        `json:"workspace_id"`
	SpaceID     UUID        `json:"space_id"`
	Slug        string      `json:"slug"`
	Label       string      `json:"label"`
	CatalogID   UUID        `json:"catalog_id"`
	Namespace   []string    `json:"namespace"`
	TableName   string      `json:"table_name"`
	UpdatedAt   Timestamptz `json:"updated_at"`
}

type ListLineageBrokenModelsRow struct {
	ID          UUID        `json:"id"`
	WorkspaceID UUID        `json:"workspace_id"`
	SpaceID     UUID        `json:"space_id"`
	Slug        string      `json:"slug"`
	Label       string      `json:"label"`
	CatalogID   UUID        `json:"catalog_id"`
	Definition  []byte      `json:"definition"`
	UpdatedAt   Timestamptz `json:"updated_at"`
}

type ListLineageMetricsParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	UserID      UUID `json:"user_id"`
}

type ListLineageMetricsRow struct {
	ID          UUID        `json:"id"`
	WorkspaceID UUID        `json:"workspace_id"`
	SpaceID     UUID        `json:"space_id"`
	Slug        string      `json:"slug"`
	Label       string      `json:"label"`
	CatalogID   UUID        `json:"catalog_id"`
	Namespace   []string    `json:"namespace"`
	TableName   string      `json:"table_name"`
	UpdatedAt   Timestamptz `json:"updated_at"`
}

type ListLineageModelsParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	UserID      UUID `json:"user_id"`
}

type ListLineageModelsRow struct {
	ID          UUID        `json:"id"`
	WorkspaceID UUID        `json:"workspace_id"`
	SpaceID     UUID        `json:"space_id"`
	Slug        string      `json:"slug"`
	Label       string      `json:"label"`
	CatalogID   UUID        `json:"catalog_id"`
	Definition  []byte      `json:"definition"`
	UpdatedAt   Timestamptz `json:"updated_at"`
}

type ListLineageSavedQueriesParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	UserID      UUID `json:"user_id"`
}

type ListLineageSavedQueriesRow struct {
	ID              UUID    `json:"id"`
	WorkspaceID     UUID    `json:"workspace_id"`
	SpaceID         UUID    `json:"space_id"`
	Slug            string  `json:"slug"`
	Name            string  `json:"name"`
	Sql             string  `json:"sql"`
	TargetSchema    *string `json:"target_schema"`
	TargetCatalogID UUID    `json:"target_catalog_id"`
}

type ListMaintenanceRunsParams struct {
	CatalogID UUID    `json:"catalog_id"`
	Trigger   *string `json:"trigger"`
	Operation *string `json:"operation"`
	Status    *string `json:"status"`
	Ns        *string `json:"ns"`
	Tbl       *string `json:"tbl"`
	Off       int32   `json:"off"`
	Lim       int32   `json:"lim"`
}

type ListMetadataCatalogsRow struct {
	ID                  UUID        `json:"id"`
	Name                string      `json:"name"`
	Origin              string      `json:"origin"`
	ProviderKind        string      `json:"provider_kind"`
	CredentialsReadOnly bool        `json:"credentials_read_only"`
	MetadataSyncedAt    Timestamptz `json:"metadata_synced_at"`
	MetadataSyncStatus  string      `json:"metadata_sync_status"`
	Bindings            []byte      `json:"bindings"`
}

type ListMetadataColumnsRow struct {
	Ordinal    int32   `json:"ordinal"`
	Name       string  `json:"name"`
	NativeType string  `json:"native_type"`
	TypeSystem string  `json:"type_system"`
	Nullable   *bool   `json:"nullable"`
	Comment    *string `json:"comment"`
}

type ListMetadataSyncRunsParams struct {
	CatalogID UUID  `json:"catalog_id"`
	Limit     int32 `json:"limit"`
}

type ListMetadataSyncRunsRow struct {
	ID            UUID        `json:"id"`
	CatalogID     UUID        `json:"catalog_id"`
	Status        string      `json:"status"`
	SourcePath    string      `json:"source_path"`
	StartedAt     Timestamptz `json:"started_at"`
	FinishedAt    Timestamptz `json:"finished_at"`
	TablesSeen    *int32      `json:"tables_seen"`
	TablesChanged *int32      `json:"tables_changed"`
	TablesRemoved *int32      `json:"tables_removed"`
	ErrorCode     *string     `json:"error_code"`
}

type ListMetadataTablesPageParams struct {
	CatalogID UUID     `json:"catalog_id"`
	Namespace []string `json:"namespace"`
	After     string   `json:"after"`
	Lim       int32    `json:"lim"`
}

// The properties of a metadata table that carry why a source lists it but
// cannot read it (#977); the sync writes them and the reads turn them into the
// reason the API returns.
const (
	TablePropUnsupportedCode   = "unsupported_code"
	TablePropUnsupportedDetail = "unsupported_detail"
)

type ListMetadataTablesPageRow struct {
	ID            UUID        `json:"id"`
	Name          string      `json:"name"`
	Kind          string      `json:"kind"`
	Comment       *string     `json:"comment"`
	LastChangedAt Timestamptz `json:"last_changed_at"`
	// UnsupportedCode is why the source lists the table but cannot read it; empty
	// for a readable table. UnsupportedDetail refines it (the source's table kind).
	UnsupportedCode   string `json:"unsupported_code"`
	UnsupportedDetail string `json:"unsupported_detail"`
}

type ListPopularTablesParams struct {
	WorkspaceID UUID  `json:"workspace_id"`
	Lim         int32 `json:"lim"`
}

type ListPopularTablesRow struct {
	TableID          UUID        `json:"table_id"`
	CatalogID        UUID        `json:"catalog_id"`
	Namespace        []string    `json:"namespace"`
	Name             string      `json:"name"`
	Kind             string      `json:"kind"`
	Uses             int64       `json:"uses"`
	Score            float64     `json:"score"`
	LastUsedAt       Timestamptz `json:"last_used_at"`
	MetadataSyncedAt Timestamptz `json:"metadata_synced_at"`
}

type ListQueryClassesParams struct {
	ExecutionIds []UUID   `json:"execution_ids"`
	QueryIds     []string `json:"query_ids"`
}

type ListQueryClassesRow struct {
	ID            UUID    `json:"id"`
	EngineQueryID *string `json:"engine_query_id"`
	QueueClass    *string `json:"queue_class"`
}

type ListQueryExecutionsByWorkspaceParams struct {
	WorkspaceID UUID  `json:"workspace_id"`
	Limit       int32 `json:"limit"`
	Offset      int32 `json:"offset"`
}

type ListQueryStatementsByWorkspaceParams struct {
	ExecutionID UUID `json:"execution_id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type ListReconcileRunsParams struct {
	EngineID UUID  `json:"engine_id"`
	Limit    int32 `json:"limit"`
}

type ListRetrieverDestinationsRow struct {
	BindingID         UUID   `json:"binding_id"`
	EngineCatalogName string `json:"engine_catalog_name"`
	Connector         string `json:"connector"`
	CatalogID         UUID   `json:"catalog_id"`
	CatalogName       string `json:"catalog_name"`
	EngineID          UUID   `json:"engine_id"`
	EngineName        string `json:"engine_name"`
	EngineKind        string `json:"engine_kind"`
}

type ListRetrieverRefreshHistoryParams struct {
	RetrieverID UUID  `json:"retriever_id"`
	Limit       int32 `json:"limit"`
}

type ListRunningStatementHandlesRow struct {
	Position    int32  `json:"position"`
	QueryHandle []byte `json:"query_handle"`
}

type ListSavedQueryVersionsRow struct {
	EntityID UUID  `json:"entity_id"`
	Version  int32 `json:"version"`
}

type ListSemanticDocsForTableParams struct {
	WorkspaceID UUID     `json:"workspace_id"`
	CatalogID   UUID     `json:"catalog_id"`
	Namespace   []string `json:"namespace"`
	TableName   string   `json:"table_name"`
	ColumnName  *string  `json:"column_name"`
}

type ListSemanticMetricVersionsRow struct {
	EntityID UUID  `json:"entity_id"`
	Version  int32 `json:"version"`
}

type ListSemanticModelVersionsRow struct {
	EntityID UUID  `json:"entity_id"`
	Version  int32 `json:"version"`
}

type ListSemanticTagsParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	Q           string `json:"q"`
	Lim         int32  `json:"lim"`
}

type ListSemanticTagsRow struct {
	Tag   string `json:"tag"`
	Count int64  `json:"count"`
}

type ListSpacesForPrincipalParams struct {
	UserID      UUID `json:"user_id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type ListSpacesForPrincipalRow struct {
	ID          UUID        `json:"id"`
	WorkspaceID UUID        `json:"workspace_id"`
	Name        string      `json:"name"`
	Description *string     `json:"description"`
	Visibility  string      `json:"visibility"`
	DefaultRole string      `json:"default_role"`
	IsDefault   bool        `json:"is_default"`
	CreatedBy   UUID        `json:"created_by"`
	CreatedAt   Timestamptz `json:"created_at"`
	UpdatedAt   Timestamptz `json:"updated_at"`
	Slug        string      `json:"slug"`
	MyRole      string      `json:"my_role"`
}

type ListStatementsForReferenceBackfillParams struct {
	Cursor UUID  `json:"cursor"`
	Lim    int32 `json:"lim"`
}

type ListStatementsForReferenceBackfillRow struct {
	ID              UUID   `json:"id"`
	Sql             string `json:"sql"`
	WorkspaceID     UUID   `json:"workspace_id"`
	TargetCatalogID UUID   `json:"target_catalog_id"`
	TargetSchema    string `json:"target_schema"`
}

type ListSyncBindingsRow struct {
	ID                UUID   `json:"id"`
	EngineID          UUID   `json:"engine_id"`
	EngineCatalogName string `json:"engine_catalog_name"`
	Mode              string `json:"mode"`
	EngineKind        string `json:"engine_kind"`
}

type ListTableHealthByCatalogParams struct {
	CatalogID UUID    `json:"catalog_id"`
	Ns        *string `json:"ns"`
	Needs     *string `json:"needs"`
	MaxScore  *int32  `json:"max_score"`
	Off       int32   `json:"off"`
	Lim       int32   `json:"lim"`
}

type ListTableHealthByConnectionParams struct {
	CatalogConnectionID UUID  `json:"catalog_connection_id"`
	Limit               int32 `json:"limit"`
	Offset              int32 `json:"offset"`
}

type ListTableHealthSnapshotsParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

type ListTableMaintenanceRunsParams struct {
	CatalogID UUID   `json:"catalog_id"`
	Namespace string `json:"namespace"`
	TableName string `json:"table_name"`
	Lim       int32  `json:"lim"`
}

type ListTeamMembersRow struct {
	ID               UUID        `json:"id"`
	TeamID           UUID        `json:"team_id"`
	UserID           UUID        `json:"user_id"`
	Role             string      `json:"role"`
	JoinedAt         Timestamptz `json:"joined_at"`
	UserName         string      `json:"user_name"`
	UserEmail        string      `json:"user_email"`
	UserRole         string      `json:"user_role"`
	UserAuthProvider string      `json:"user_auth_provider"`
	UserStatus       string      `json:"user_status"`
	UserCreatedAt    Timestamptz `json:"user_created_at"`
}

type ListTrinoQuerySnapshotsByEngineParams struct {
	EngineID UUID    `json:"engine_id"`
	State    *string `json:"state"`
	UserName *string `json:"user_name"`
	Q        *string `json:"q"`
	Off      int32   `json:"off"`
	Lim      int32   `json:"lim"`
}

type ListTrinoQuerySnapshotsByUserParams struct {
	User   string `json:"user"`
	Limit  int32  `json:"limit"`
	Offset int32  `json:"offset"`
}

type ListTrinoQuerySnapshotsParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

type ListVisibleCatalogConnectionsParams struct {
	IsAdmin bool `json:"is_admin"`
	UserID  UUID `json:"user_id"`
}

type ListVisibleCatalogsParams struct {
	IsAdmin bool `json:"is_admin"`
	UserID  UUID `json:"user_id"`
}

type ListVisibleQueryFoldersParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	UserID      UUID `json:"user_id"`
	SpaceID     UUID `json:"space_id"`
	ParentID    UUID `json:"parent_id"`
}

type ListVisibleSavedQueriesParams struct {
	WorkspaceID UUID    `json:"workspace_id"`
	UserID      UUID    `json:"user_id"`
	SpaceID     UUID    `json:"space_id"`
	FolderID    UUID    `json:"folder_id"`
	Q           *string `json:"q"`
	Lim         *int32  `json:"lim"`
}

type ListVisibleSemanticMetricsParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	UserID      UUID `json:"user_id"`
	SpaceID     UUID `json:"space_id"`
	// IDs, when not empty, restricts the result to these rows.
	IDs []UUID `json:"ids"`
}

type ListVisibleSemanticModelsParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	UserID      UUID `json:"user_id"`
	SpaceID     UUID `json:"space_id"`
	// IDs, when not empty, restricts the result to these rows.
	IDs []UUID `json:"ids"`
}

type ListWorkspaceCatalogGrantsRow struct {
	WorkspaceID UUID        `json:"workspace_id"`
	CatalogID   UUID        `json:"catalog_id"`
	GrantedAt   Timestamptz `json:"granted_at"`
	CatalogName string      `json:"catalog_name"`
}

type ListWorkspaceMembersRow struct {
	User    User   `json:"user"`
	Role    string `json:"role"`
	Source  string `json:"source"`
	RoleSet bool   `json:"role_set"`
}

type ListWorkspacePrincipalUsersParams struct {
	WorkspaceID UUID  `json:"workspace_id"`
	Lim         int32 `json:"lim"`
}

type ListWorkspacePrincipalUsersRow struct {
	ID    UUID   `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type ListWorkspaceTargetsRow struct {
	BindingID         UUID   `json:"binding_id"`
	EngineCatalogName string `json:"engine_catalog_name"`
	Mode              string `json:"mode"`
	BindingEnabled    bool   `json:"binding_enabled"`
	EngineID          UUID   `json:"engine_id"`
	EngineName        string `json:"engine_name"`
	EngineKind        string `json:"engine_kind"`
	EngineEnabled     bool   `json:"engine_enabled"`
	CatalogID         UUID   `json:"catalog_id"`
	CatalogName       string `json:"catalog_name"`
	CatalogEnabled    bool   `json:"catalog_enabled"`
}

type MarkEngineCatalogManagedParams struct {
	EngineID    UUID   `json:"engine_id"`
	CatalogName string `json:"catalog_name"`
}

type MarkEngineCatalogsAbsentParams struct {
	EngineID     UUID     `json:"engine_id"`
	PresentNames []string `json:"present_names"`
}

type MetadataStatusCountsRow struct {
	Catalogs int64 `json:"catalogs"`
	Stale    int64 `json:"stale"`
	Failed   int64 `json:"failed"`
}

type MoveQueryFolderSubtreeParams struct {
	SpaceID     UUID `json:"space_id"`
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type MoveSavedQueriesInFolderSubtreeParams struct {
	SpaceID     UUID `json:"space_id"`
	ID          UUID `json:"id"`
	WorkspaceID UUID `json:"workspace_id"`
}

type NextContentVersionParams struct {
	EntityType string `json:"entity_type"`
	EntityID   UUID   `json:"entity_id"`
}

type OldestExecutionByAgentSinceParams struct {
	AgentID   UUID        `json:"agent_id"`
	StartedAt Timestamptz `json:"started_at"`
}

type RemoveTeamMemberParams struct {
	TeamID UUID `json:"team_id"`
	UserID UUID `json:"user_id"`
}

type RestoreSavedQueryParams struct {
	ID              UUID    `json:"id"`
	WorkspaceID     UUID    `json:"workspace_id"`
	SpaceID         UUID    `json:"space_id"`
	FolderID        UUID    `json:"folder_id"`
	CreatedBy       UUID    `json:"created_by"`
	UpdatedBy       UUID    `json:"updated_by"`
	Name            string  `json:"name"`
	Slug            string  `json:"slug"`
	Sql             string  `json:"sql"`
	TargetBindingID UUID    `json:"target_binding_id"`
	TargetSchema    *string `json:"target_schema"`
}

type RestoreSemanticMetricParams struct {
	ID            UUID     `json:"id"`
	WorkspaceID   UUID     `json:"workspace_id"`
	SpaceID       UUID     `json:"space_id"`
	Slug          string   `json:"slug"`
	Label         string   `json:"label"`
	Description   string   `json:"description"`
	Certified     bool     `json:"certified"`
	CatalogID     UUID     `json:"catalog_id"`
	Namespace     []string `json:"namespace"`
	TableName     string   `json:"table_name"`
	BindingID     UUID     `json:"binding_id"`
	Definition    []byte   `json:"definition"`
	FormatVersion int32    `json:"format_version"`
	OwnerUserID   UUID     `json:"owner_user_id"`
	UpdatedBy     UUID     `json:"updated_by"`
}

type RestoreSemanticModelParams struct {
	ID            UUID   `json:"id"`
	WorkspaceID   UUID   `json:"workspace_id"`
	SpaceID       UUID   `json:"space_id"`
	Slug          string `json:"slug"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	Certified     bool   `json:"certified"`
	CatalogID     UUID   `json:"catalog_id"`
	BindingID     UUID   `json:"binding_id"`
	Definition    []byte `json:"definition"`
	FormatVersion int32  `json:"format_version"`
	OwnerUserID   UUID   `json:"owner_user_id"`
	UpdatedBy     UUID   `json:"updated_by"`
}

type RevokeAgentTokenParams struct {
	ID      UUID `json:"id"`
	AgentID UUID `json:"agent_id"`
}

type SaveUserIDPTokenOnSignInParams struct {
	UserID          UUID        `json:"user_id"`
	AuthProviderID  UUID        `json:"auth_provider_id"`
	AccessTokenEnc  string      `json:"access_token_enc"`
	RefreshTokenEnc *string     `json:"refresh_token_enc"`
	ExpiresAt       Timestamptz `json:"expires_at"`
}

type SavedQuerySlugExistsParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	Slug        string `json:"slug"`
}

type SearchMetadataParams struct {
	WorkspaceID UUID     `json:"workspace_id"`
	Tags        []string `json:"tags"`
	Certified   *bool    `json:"certified"`
	Namespace   []string `json:"namespace"`
	Lim         int32    `json:"lim"`
	CatalogID   UUID     `json:"catalog_id"`
	Q           string   `json:"q"`
	Kind        string   `json:"kind"`
	// PrefixOnly returns only names that start with Q and forgives no typo.
	PrefixOnly bool `json:"prefix_only"`
}

type SearchMetadataRow struct {
	Kind             string      `json:"kind"`
	TableID          UUID        `json:"table_id"`
	CatalogID        UUID        `json:"catalog_id"`
	Namespace        []string    `json:"namespace"`
	TableName        string      `json:"table_name"`
	ColumnName       string      `json:"column_name"`
	NativeType       string      `json:"native_type"`
	Score            float64     `json:"score"`
	Popularity       float64     `json:"popularity"`
	MetadataSyncedAt Timestamptz `json:"metadata_synced_at"`
}

type SearchSemanticDocsParams struct {
	Q           string  `json:"q"`
	WorkspaceID UUID    `json:"workspace_id"`
	Tag         *string `json:"tag"`
	Certified   *bool   `json:"certified"`
	OwnerID     UUID    `json:"owner_id"`
	// CatalogID, when valid, narrows the docs to one catalog.
	CatalogID UUID  `json:"catalog_id"`
	Lim       int32 `json:"lim"`
}

type SearchSemanticDocsRow struct {
	ID          UUID        `json:"id"`
	WorkspaceID UUID        `json:"workspace_id"`
	CatalogID   UUID        `json:"catalog_id"`
	Namespace   []string    `json:"namespace"`
	TableName   string      `json:"table_name"`
	ColumnName  *string     `json:"column_name"`
	Description string      `json:"description"`
	Tags        []string    `json:"tags"`
	OwnerUserID UUID        `json:"owner_user_id"`
	UpdatedBy   UUID        `json:"updated_by"`
	UpdatedAt   Timestamptz `json:"updated_at"`
	Certified   bool        `json:"certified"`
	CertifiedBy UUID        `json:"certified_by"`
	CertifiedAt Timestamptz `json:"certified_at"`
	Score       float64     `json:"score"`
}

type SearchVisibleSavedQueriesParams struct {
	WorkspaceID UUID    `json:"workspace_id"`
	UserID      UUID    `json:"user_id"`
	SpaceID     UUID    `json:"space_id"`
	Q           *string `json:"q"`
	Lim         *int32  `json:"lim"`
	// IDs, when not empty, restricts the result to these rows.
	IDs []UUID `json:"ids"`
}

type SetAgentStatusParams struct {
	ID     UUID   `json:"id"`
	Status string `json:"status"`
}

type SetBIGatewayEnabledParams struct {
	Enabled   bool `json:"enabled"`
	UpdatedBy UUID `json:"updated_by"`
}

type SetAuditExportCursorParams struct {
	Stream     string      `json:"stream"`
	Sink       string      `json:"sink"`
	OccurredAt Timestamptz `json:"occurred_at"`
	ID         int64       `json:"id"`
	CursorKey  string      `json:"cursor_key"`
}

type SetCatalogDiscoverableParams struct {
	ID           UUID `json:"id"`
	Discoverable bool `json:"discoverable"`
}

type SetCatalogEnabledParams struct {
	ID      UUID `json:"id"`
	Enabled bool `json:"enabled"`
}

type SetEngineMaxRunningTotalParams struct {
	ID              UUID   `json:"id"`
	MaxRunningTotal *int32 `json:"max_running_total"`
}

type SetEngineTrinoResourceGroupsParams struct {
	ID                         UUID `json:"id"`
	TrinoResourceGroupsEnabled bool `json:"trino_resource_groups_enabled"`
}

type SetExecutionResultParams struct {
	ID        UUID    `json:"id"`
	ResultKey *string `json:"result_key"`
}

type SetExecutionTargetParams struct {
	ID              UUID    `json:"id"`
	EngineID        UUID    `json:"engine_id"`
	TargetBindingID UUID    `json:"target_binding_id"`
	TargetCatalogID UUID    `json:"target_catalog_id"`
	TargetSchema    *string `json:"target_schema"`
	TargetSnapshot  []byte  `json:"target_snapshot"`
}

type SetMaintenanceRunAttemptsParams struct {
	Attempts int32 `json:"attempts"`
	ID       UUID  `json:"id"`
}

type SetSemanticMetricCertificationParams struct {
	Certified   bool `json:"certified"`
	Actor       UUID `json:"actor"`
	WorkspaceID UUID `json:"workspace_id"`
	ID          UUID `json:"id"`
}

type SetSemanticModelCertificationParams struct {
	Certified   bool `json:"certified"`
	Actor       UUID `json:"actor"`
	WorkspaceID UUID `json:"workspace_id"`
	ID          UUID `json:"id"`
}

type SetSpaceMemberParams struct {
	SpaceID       UUID   `json:"space_id"`
	PrincipalType string `json:"principal_type"`
	PrincipalID   UUID   `json:"principal_id"`
	Role          string `json:"role"`
	GrantedBy     UUID   `json:"granted_by"`
}

type SetStatementQueryHandleParams struct {
	ExecutionID UUID   `json:"execution_id"`
	Position    int32  `json:"position"`
	QueryHandle []byte `json:"query_handle"`
}

type SetTrinoQueryProfileParams struct {
	TrinoQueryID    string `json:"trino_query_id"`
	RawTrinoPayload []byte `json:"raw_trino_payload"`
}

type SetWorkspaceDefaultTargetParams struct {
	ID               UUID    `json:"id"`
	DefaultBindingID UUID    `json:"default_binding_id"`
	DefaultSchema    *string `json:"default_schema"`
}

type SharesWorkspaceParams struct {
	CallerID UUID `json:"caller_id"`
	UserID   UUID `json:"user_id"`
}

type SoftDeleteTrinoCatalogRegistryParams struct {
	ClusterID   UUID   `json:"cluster_id"`
	CatalogName string `json:"catalog_name"`
}

type SpaceSlugExistsParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	Slug        string `json:"slug"`
}

type StartMetadataSyncRunParams struct {
	CatalogID  UUID   `json:"catalog_id"`
	SourcePath string `json:"source_path"`
}

type StartMetadataSyncRunRow struct {
	ID        UUID        `json:"id"`
	StartedAt Timestamptz `json:"started_at"`
}

type StartQueryStatementParams struct {
	ExecutionID UUID  `json:"execution_id"`
	Position    int32 `json:"position"`
}

type SuspendUserParams struct {
	ID              UUID    `json:"id"`
	SuspendedReason *string `json:"suspended_reason"`
}

type TombstoneMissingNamespacesParams struct {
	CatalogID     UUID        `json:"catalog_id"`
	PassStartedAt Timestamptz `json:"pass_started_at"`
}

type TombstoneMissingTablesParams struct {
	CatalogID     UUID        `json:"catalog_id"`
	PassStartedAt Timestamptz `json:"pass_started_at"`
}

type TouchMetadataTableParams struct {
	CatalogID UUID     `json:"catalog_id"`
	Namespace []string `json:"namespace"`
	Name      string   `json:"name"`
}

type TransitionBindingParams struct {
	ToState           string   `json:"to_state"`
	ErrorCode         *string  `json:"error_code"`
	ErrorMessage      *string  `json:"error_message"`
	EngineCatalogName *string  `json:"engine_catalog_name"`
	DesiredVersion    *string  `json:"desired_version"`
	ID                UUID     `json:"id"`
	FromStates        []string `json:"from_states"`
}

type UpdateAgentParams struct {
	ID          UUID   `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type UpdateAgentPolicyParams struct {
	ID              UUID   `json:"id"`
	Policy          []byte `json:"policy"`
	PolicyUpdatedBy UUID   `json:"policy_updated_by"`
}

type UpdateAuthProviderParams struct {
	ID        UUID   `json:"id"`
	Name      string `json:"name"`
	ConfigEnc string `json:"config_enc"`
	Enabled   bool   `json:"enabled"`
}

type UpdateBindingParams struct {
	ID                UUID     `json:"id"`
	EngineCatalogName string   `json:"engine_catalog_name"`
	Connector         *string  `json:"connector"`
	ExtraProps        []byte   `json:"extra_props"`
	Enabled           bool     `json:"enabled"`
	LifecycleState    string   `json:"lifecycle_state"`
	DesiredVersion    *string  `json:"desired_version"`
	FromStates        []string `json:"from_states"`
}

type UpdateCatalogConnectionParams struct {
	Name                *string `json:"name"`
	Url                 *string `json:"url"`
	Config              []byte  `json:"config"`
	CredentialsEnc      *string `json:"credentials_enc"`
	CredentialsReadOnly *bool   `json:"credentials_read_only"`
	ID                  UUID    `json:"id"`
}

type UpdateCatalogMetaParams struct {
	ID          UUID    `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

type UpdateCatalogParams struct {
	ID               UUID    `json:"id"`
	Name             string  `json:"name"`
	Description      *string `json:"description"`
	Warehouse        *string `json:"warehouse"`
	Connector        *string `json:"connector"`
	ExtraProps       []byte  `json:"extra_props"`
	TrinoCatalogName *string `json:"trino_catalog_name"`
}

type UpdateEngineParams struct {
	Enabled            *bool   `json:"enabled"`
	IdentityMode       *string `json:"identity_mode"`
	CostRoutingEnabled *bool   `json:"cost_routing_enabled"`
	ClearCostThreshold *bool   `json:"clear_cost_threshold"`
	CostThresholdBytes *int64  `json:"cost_threshold_bytes"`
	ClearExternalUrl   *bool   `json:"clear_external_url"`
	ExternalUrl        *string `json:"external_url"`
	ID                 UUID    `json:"id"`
}

type UpdateMaintenanceRunParams struct {
	Status       string  `json:"status"`
	ErrorCode    *string `json:"error_code"`
	ErrorMessage *string `json:"error_message"`
	ID           UUID    `json:"id"`
}

type UpdateMigrationItemParams struct {
	ID           UUID    `json:"id"`
	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message"`
}

type UpdateMigrationProgressParams struct {
	ID             UUID  `json:"id"`
	MigratedTables int32 `json:"migrated_tables"`
	FailedTables   int32 `json:"failed_tables"`
}

type UpdateMigrationStatusParams struct {
	ID                 UUID    `json:"id"`
	Status             string  `json:"status"`
	TemporalWorkflowID *string `json:"temporal_workflow_id"`
}

type UpdateQueryExecutionParams struct {
	ID            UUID        `json:"id"`
	Status        string      `json:"status"`
	EngineQueryID *string     `json:"engine_query_id"`
	RowsReturned  *int64      `json:"rows_returned"`
	DurationMs    *int64      `json:"duration_ms"`
	FinishedAt    Timestamptz `json:"finished_at"`
}

type UpdateQueryFolderParams struct {
	Name        string  `json:"name"`
	Slug        *string `json:"slug"`
	ParentID    UUID    `json:"parent_id"`
	SpaceID     UUID    `json:"space_id"`
	ID          UUID    `json:"id"`
	WorkspaceID UUID    `json:"workspace_id"`
}

type UpdateQueryStatementParams struct {
	ID            UUID        `json:"id"`
	Status        string      `json:"status"`
	EngineQueryID *string     `json:"engine_query_id"`
	RowsReturned  *int64      `json:"rows_returned"`
	DurationMs    *int64      `json:"duration_ms"`
	ErrorMessage  *string     `json:"error_message"`
	FinishedAt    Timestamptz `json:"finished_at"`
	ResultKey     *string     `json:"result_key"`
	ResultBytes   *int64      `json:"result_bytes"`
	Truncated     bool        `json:"truncated"`
	ResultSets    []byte      `json:"result_sets"`
	ErrorCode     *string     `json:"error_code"`
}

type UpdateRetrieverAfterRefreshParams struct {
	ID        UUID    `json:"id"`
	Status    string  `json:"status"`
	ErrorCode *string `json:"error_code"`
}

type UpdateRetrieverParams struct {
	ID            UUID        `json:"id"`
	WorkspaceID   UUID        `json:"workspace_id"`
	Name          string      `json:"name"`
	DefinitionSql string      `json:"definition_sql"`
	DestCatalog   string      `json:"dest_catalog"`
	DestNamespace string      `json:"dest_namespace"`
	DestTable     string      `json:"dest_table"`
	DestBindingID UUID        `json:"dest_binding_id"`
	CronSchedule  string      `json:"cron_schedule"`
	RefreshType   string      `json:"refresh_type"`
	Status        string      `json:"status"`
	ErrorCode     *string     `json:"error_code"`
	NextRefreshAt Timestamptz `json:"next_refresh_at"`
}

type UpdateRetrieverRefreshHistoryParams struct {
	ID            UUID    `json:"id"`
	Status        string  `json:"status"`
	RowsProcessed *int64  `json:"rows_processed"`
	DurationMs    *int64  `json:"duration_ms"`
	ErrorMessage  *string `json:"error_message"`
	ErrorCode     *string `json:"error_code"`
}

type UpdateSavedQueryParams struct {
	Name            string  `json:"name"`
	Sql             string  `json:"sql"`
	FolderID        UUID    `json:"folder_id"`
	SpaceID         UUID    `json:"space_id"`
	Slug            string  `json:"slug"`
	TargetBindingID UUID    `json:"target_binding_id"`
	TargetSchema    *string `json:"target_schema"`
	UpdatedBy       UUID    `json:"updated_by"`
	ID              UUID    `json:"id"`
	WorkspaceID     UUID    `json:"workspace_id"`
}

type UpdateSemanticMetricParams struct {
	SpaceID       UUID     `json:"space_id"`
	Slug          string   `json:"slug"`
	Label         string   `json:"label"`
	Description   string   `json:"description"`
	Certified     bool     `json:"certified"`
	CatalogID     UUID     `json:"catalog_id"`
	Namespace     []string `json:"namespace"`
	TableName     string   `json:"table_name"`
	BindingID     UUID     `json:"binding_id"`
	Definition    []byte   `json:"definition"`
	FormatVersion int32    `json:"format_version"`
	OwnerUserID   UUID     `json:"owner_user_id"`
	UpdatedBy     UUID     `json:"updated_by"`
	WorkspaceID   UUID     `json:"workspace_id"`
	ID            UUID     `json:"id"`
}

type UpdateSemanticModelParams struct {
	SpaceID       UUID   `json:"space_id"`
	Slug          string `json:"slug"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	Certified     bool   `json:"certified"`
	CatalogID     UUID   `json:"catalog_id"`
	BindingID     UUID   `json:"binding_id"`
	Definition    []byte `json:"definition"`
	FormatVersion int32  `json:"format_version"`
	OwnerUserID   UUID   `json:"owner_user_id"`
	UpdatedBy     UUID   `json:"updated_by"`
	WorkspaceID   UUID   `json:"workspace_id"`
	ID            UUID   `json:"id"`
}

type UpdateServiceAccountParams struct {
	Name           string  `json:"name"`
	Description    *string `json:"description"`
	AuthProvider   string  `json:"auth_provider"`
	AuthProviderID UUID    `json:"auth_provider_id"`
	ExternalID     *string `json:"external_id"`
	ID             UUID    `json:"id"`
}

type UpdateSpaceParams struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Visibility  string  `json:"visibility"`
	DefaultRole string  `json:"default_role"`
	WorkspaceID UUID    `json:"workspace_id"`
	ID          UUID    `json:"id"`
}

type UpdateTableMaintenancePolicyParams struct {
	Mode                        string `json:"mode"`
	WriterCommitIntervalSeconds int32  `json:"writer_commit_interval_seconds"`
	Optimize                    []byte `json:"optimize"`
	OptimizeManifests           []byte `json:"optimize_manifests"`
	ExpireSnapshots             []byte `json:"expire_snapshots"`
	RemoveOrphanFiles           []byte `json:"remove_orphan_files"`
	Actor                       UUID   `json:"actor"`
	CatalogID                   UUID   `json:"catalog_id"`
	Namespace                   string `json:"namespace"`
	TableName                   string `json:"table_name"`
	Revision                    int64  `json:"revision"`
}

type UpdateTeamMemberRoleParams struct {
	TeamID UUID   `json:"team_id"`
	UserID UUID   `json:"user_id"`
	Role   string `json:"role"`
}

type UpdateTeamParams struct {
	ID          UUID    `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

type UpdateUserIDPTokenParams struct {
	AccessTokenEnc  string      `json:"access_token_enc"`
	RefreshTokenEnc *string     `json:"refresh_token_enc"`
	ExpiresAt       Timestamptz `json:"expires_at"`
	UserID          UUID        `json:"user_id"`
}

type UpdateUserParams struct {
	ID     UUID    `json:"id"`
	Name   string  `json:"name"`
	Email  string  `json:"email"`
	Locale *string `json:"locale"`
}

type UpdateUserPasswordParams struct {
	ID           UUID    `json:"id"`
	PasswordHash *string `json:"password_hash"`
}

type UpdateUserPersonalWorkspaceParams struct {
	ID                  UUID `json:"id"`
	PersonalWorkspaceID UUID `json:"personal_workspace_id"`
}

type UpdateUserRoleParams struct {
	ID   UUID   `json:"id"`
	Role string `json:"role"`
}

type UpdateWorkloadRuleParams struct {
	ID       UUID    `json:"id"`
	Position int32   `json:"position"`
	Enabled  bool    `json:"enabled"`
	Match    []byte  `json:"match"`
	Class    string  `json:"class"`
	MaxClass *string `json:"max_class"`
	Weight   int32   `json:"weight"`
}

type UpdateWorkspaceParams struct {
	ID          UUID    `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	BIEnabled   *bool   `json:"bi_enabled"`
}

type UpsertAdoptedBindingParams struct {
	CatalogID         UUID    `json:"catalog_id"`
	EngineID          UUID    `json:"engine_id"`
	EngineCatalogName string  `json:"engine_catalog_name"`
	Connector         *string `json:"connector"`
}

type UpsertEngineCatalogObservationParams struct {
	EngineID    UUID    `json:"engine_id"`
	CatalogName string  `json:"catalog_name"`
	Mode        string  `json:"mode"`
	Connector   *string `json:"connector"`
}

type UpsertEngineQueueCapacityParams struct {
	EngineID         UUID   `json:"engine_id"`
	Class            string `json:"class"`
	MaxRunning       int32  `json:"max_running"`
	MaxQueued        int32  `json:"max_queued"`
	QueueTimeoutMs   int64  `json:"queue_timeout_ms"`
	RuntimeTimeoutMs *int64 `json:"runtime_timeout_ms"`
}

type UpsertManagedBindingParams struct {
	CatalogID         UUID    `json:"catalog_id"`
	EngineID          UUID    `json:"engine_id"`
	EngineCatalogName string  `json:"engine_catalog_name"`
	Connector         *string `json:"connector"`
	ExtraProps        []byte  `json:"extra_props"`
	Enabled           bool    `json:"enabled"`
	LifecycleState    string  `json:"lifecycle_state"`
	DesiredVersion    *string `json:"desired_version"`
}

type UpsertMetadataNamespaceParams struct {
	CatalogID UUID     `json:"catalog_id"`
	Namespace []string `json:"namespace"`
}

type UpsertMetadataTableParams struct {
	CatalogID   UUID     `json:"catalog_id"`
	Namespace   []string `json:"namespace"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Fingerprint string   `json:"fingerprint"`
	Properties  []byte   `json:"properties"`
	Comment     *string  `json:"comment"`
}

type UpsertMetadataTableRow struct {
	ID      UUID `json:"id"`
	Changed bool `json:"changed"`
}

type UpsertNativeBindingParams struct {
	CatalogID         UUID   `json:"catalog_id"`
	EngineID          UUID   `json:"engine_id"`
	EngineCatalogName string `json:"engine_catalog_name"`
}

type UpsertNativeCatalogParams struct {
	ConnectionID UUID   `json:"connection_id"`
	Name         string `json:"name"`
}

type UpsertSemanticDocParams struct {
	WorkspaceID UUID     `json:"workspace_id"`
	CatalogID   UUID     `json:"catalog_id"`
	Namespace   []string `json:"namespace"`
	TableName   string   `json:"table_name"`
	ColumnName  *string  `json:"column_name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	OwnerUserID UUID     `json:"owner_user_id"`
	UpdatedBy   UUID     `json:"updated_by"`
	Certified   bool     `json:"certified"`
}

type UpsertTableHealthSnapshotParams struct {
	CatalogID              UUID        `json:"catalog_id"`
	CatalogConnectionID    UUID        `json:"catalog_connection_id"`
	Catalog                string      `json:"catalog"`
	Namespace              string      `json:"namespace"`
	TableName              string      `json:"table_name"`
	SnapshotCount          int32       `json:"snapshot_count"`
	FileCount              *int32      `json:"file_count"`
	TotalSizeBytes         *int64      `json:"total_size_bytes"`
	OldestSnapshotAgeHours *int32      `json:"oldest_snapshot_age_hours"`
	LastMaintenanceAt      Timestamptz `json:"last_maintenance_at"`
	HealthScore            int32       `json:"health_score"`
	NeedsCompaction        bool        `json:"needs_compaction"`
	NeedsSnapshotExpiry    bool        `json:"needs_snapshot_expiry"`
	DeleteFileCount        *int64      `json:"delete_file_count"`
	EqualityDeleteCount    *int64      `json:"equality_delete_count"`
	PositionDeleteCount    *int64      `json:"position_delete_count"`
	SmallFileCount         *int64      `json:"small_file_count"`
	CommitsLastHour        *int32      `json:"commits_last_hour"`
	CommitsLastDay         *int32      `json:"commits_last_day"`
	AvgFileSizeBytes       *int64      `json:"avg_file_size_bytes"`
	ManifestCount          *int32      `json:"manifest_count"`
	FormatVersion          *int32      `json:"format_version"`
	TableProperties        []byte      `json:"table_properties"`
	DueOperations          []string    `json:"due_operations"`
	PolicyRevision         *int64      `json:"policy_revision"`
}

type UpsertTeamMemberParams struct {
	TeamID UUID   `json:"team_id"`
	UserID UUID   `json:"user_id"`
	Role   string `json:"role"`
}

type UpsertTrinoCatalogRegistryParams struct {
	ClusterID      UUID    `json:"cluster_id"`
	CatalogName    string  `json:"catalog_name"`
	ConnectorName  string  `json:"connector_name"`
	Column4        []byte  `json:"column_4"`
	CatalogVersion *string `json:"catalog_version"`
}

type UpsertTrinoQuerySnapshotParams struct {
	TrinoQueryID       string      `json:"trino_query_id"`
	EngineID           UUID        `json:"engine_id"`
	ExecutionID        UUID        `json:"execution_id"`
	Origin             string      `json:"origin"`
	User               string      `json:"user"`
	State              string      `json:"state"`
	Sql                string      `json:"sql"`
	Catalog            *string     `json:"catalog"`
	Schema             *string     `json:"schema"`
	CreatedAt          Timestamptz `json:"created_at"`
	ElapsedMs          *int32      `json:"elapsed_ms"`
	CpuMs              *int32      `json:"cpu_ms"`
	PhysicalInputBytes *int64      `json:"physical_input_bytes"`
	OutputRows         *int64      `json:"output_rows"`
	ErrorMessage       *string     `json:"error_message"`
	RawTrinoPayload    []byte      `json:"raw_trino_payload"`
}

type UpsertWorkspaceCatalogGrantParams struct {
	WorkspaceID UUID `json:"workspace_id"`
	CatalogID   UUID `json:"catalog_id"`
	GrantedBy   UUID `json:"granted_by"`
}

type UpsertWorkspaceMemberParams struct {
	WorkspaceID UUID   `json:"workspace_id"`
	UserID      UUID   `json:"user_id"`
	Role        string `json:"role"`
	GrantedBy   UUID   `json:"granted_by"`
}
