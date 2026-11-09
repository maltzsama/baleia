// Package db keeps the hand-maintained data-transfer types the store and
// its callers share. They were extracted from the retired generated query
// code and are maintained here from now on.
//
//nolint:revive // DTO fields keep the SQL column spelling and the published JSON contract.
package db

import (
	"net/netip"

	"github.com/jackc/pgx/v5/pgtype"
)

type AbandonRunningStatementsParams struct {
	ExecutionID  pgtype.UUID `json:"execution_id"`
	Status       string      `json:"status"`
	ErrorCode    *string     `json:"error_code"`
	ErrorMessage *string     `json:"error_message"`
}

type AbandonRunningStatementsRow struct {
	Position    int32  `json:"position"`
	QueryHandle []byte `json:"query_handle"`
}

type AddAgentWorkspaceParams struct {
	AgentID     pgtype.UUID `json:"agent_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type AddTeamMemberParams struct {
	TeamID pgtype.UUID `json:"team_id"`
	UserID pgtype.UUID `json:"user_id"`
	Role   string      `json:"role"`
}

type AdvanceRetrieverScheduleParams struct {
	ID            pgtype.UUID        `json:"id"`
	NextRefreshAt pgtype.Timestamptz `json:"next_refresh_at"`
}

type AgentWorkspaceBoundParams struct {
	AgentID     pgtype.UUID `json:"agent_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type ApplyGroupWorkspaceRolesParams struct {
	UserID      pgtype.UUID `json:"user_id"`
	Assignments []byte      `json:"assignments"`
}

type CancelQueryExecutionParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type CatalogGrantedToAgentParams struct {
	AgentID   pgtype.UUID `json:"agent_id"`
	CatalogID pgtype.UUID `json:"catalog_id"`
	UserID    pgtype.UUID `json:"user_id"`
}

type CheckWorkspaceAccessParams struct {
	ID     pgtype.UUID `json:"id"`
	UserID pgtype.UUID `json:"user_id"`
}

type ClearWorkspaceDefaultForCatalogParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
}

type ConsumeRefreshTokenRow struct {
	ID       pgtype.UUID `json:"id"`
	UserID   pgtype.UUID `json:"user_id"`
	FamilyID pgtype.UUID `json:"family_id"`
}

type CountCatalogAccessImpactParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
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
	AgentID   pgtype.UUID        `json:"agent_id"`
	StartedAt pgtype.Timestamptz `json:"started_at"`
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
	UserID    pgtype.UUID        `json:"user_id"`
	Name      string             `json:"name"`
	KeyHash   string             `json:"key_hash"`
	Prefix    string             `json:"prefix"`
	Scopes    []string           `json:"scopes"`
	ExpiresAt pgtype.Timestamptz `json:"expires_at"`
}

type CreateAgentParams struct {
	OwnerID     pgtype.UUID `json:"owner_id"`
	Kind        string      `json:"kind"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
}

type CreateAgentTokenParams struct {
	AgentID   pgtype.UUID        `json:"agent_id"`
	Name      string             `json:"name"`
	TokenHash string             `json:"token_hash"`
	Prefix    string             `json:"prefix"`
	ExpiresAt pgtype.Timestamptz `json:"expires_at"`
	CreatedBy pgtype.UUID        `json:"created_by"`
}

type CreateAuthProviderParams struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	ConfigEnc string `json:"config_enc"`
	Enabled   bool   `json:"enabled"`
}

type CreateCatalogConnectionParams struct {
	ID                  pgtype.UUID `json:"id"`
	Name                string      `json:"name"`
	Type                string      `json:"type"`
	Url                 string      `json:"url"`
	Config              []byte      `json:"config"`
	CredentialsEnc      *string     `json:"credentials_enc"`
	CredentialsReadOnly bool        `json:"credentials_read_only"`
	CreatedBy           pgtype.UUID `json:"created_by"`
}

type CreateCatalogGrantRequestParams struct {
	WorkspaceID     pgtype.UUID `json:"workspace_id"`
	CatalogID       pgtype.UUID `json:"catalog_id"`
	InitiatedBySide string      `json:"initiated_by_side"`
	RequestedBy     pgtype.UUID `json:"requested_by"`
}

type CreateCatalogParams struct {
	ConnectionID     pgtype.UUID `json:"connection_id"`
	Name             string      `json:"name"`
	Description      *string     `json:"description"`
	TrinoCatalogName *string     `json:"trino_catalog_name"`
	ClusterID        pgtype.UUID `json:"cluster_id"`
	Warehouse        *string     `json:"warehouse"`
	Connector        *string     `json:"connector"`
	ExtraProps       []byte      `json:"extra_props"`
}

type CreateEngineParams struct {
	Name         string      `json:"name"`
	Kind         string      `json:"kind"`
	Class        string      `json:"class"`
	ConnectionID pgtype.UUID `json:"connection_id"`
	IdentityMode string      `json:"identity_mode"`
	CreatedBy    pgtype.UUID `json:"created_by"`
}

type CreateMaintenanceRunParams struct {
	CatalogID  pgtype.UUID `json:"catalog_id"`
	BindingID  pgtype.UUID `json:"binding_id"`
	Namespace  string      `json:"namespace"`
	TableName  string      `json:"table_name"`
	Operations []string    `json:"operations"`
	Retention  *string     `json:"retention"`
	StartedBy  pgtype.UUID `json:"started_by"`
	WorkflowID string      `json:"workflow_id"`
}

type CreateManagedBindingParams struct {
	CatalogID         pgtype.UUID `json:"catalog_id"`
	EngineID          pgtype.UUID `json:"engine_id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	Connector         *string     `json:"connector"`
	ExtraProps        []byte      `json:"extra_props"`
	DesiredVersion    *string     `json:"desired_version"`
}

type CreateMigrationItemParams struct {
	MigrationID     pgtype.UUID `json:"migration_id"`
	SourceNamespace string      `json:"source_namespace"`
	SourceTable     string      `json:"source_table"`
	TargetNamespace string      `json:"target_namespace"`
	TargetTable     string      `json:"target_table"`
	Status          string      `json:"status"`
}

type CreateMigrationParams struct {
	CreatedBy                 pgtype.UUID `json:"created_by"`
	Name                      string      `json:"name"`
	SourceType                string      `json:"source_type"`
	SourceUrl                 string      `json:"source_url"`
	SourceCredentialsEnc      *string     `json:"source_credentials_enc"`
	TargetCatalogConnectionID pgtype.UUID `json:"target_catalog_connection_id"`
}

type CreateOIDCLoginAttemptParams struct {
	State        string             `json:"state"`
	ProviderID   pgtype.UUID        `json:"provider_id"`
	Nonce        *string            `json:"nonce"`
	CodeVerifier string             `json:"code_verifier"`
	Redirect     *string            `json:"redirect"`
	ExpiresAt    pgtype.Timestamptz `json:"expires_at"`
}

type CreatePasswordResetTokenParams struct {
	TokenHash string             `json:"token_hash"`
	UserID    pgtype.UUID        `json:"user_id"`
	ExpiresAt pgtype.Timestamptz `json:"expires_at"`
}

type CreatePlannedMaintenanceRunParams struct {
	CatalogID      pgtype.UUID `json:"catalog_id"`
	BindingID      pgtype.UUID `json:"binding_id"`
	Namespace      string      `json:"namespace"`
	TableName      string      `json:"table_name"`
	Operations     []string    `json:"operations"`
	Retention      *string     `json:"retention"`
	Trigger        string      `json:"trigger"`
	StartedBy      pgtype.UUID `json:"started_by"`
	WorkflowID     string      `json:"workflow_id"`
	Reason         []byte      `json:"reason"`
	Parameters     []byte      `json:"parameters"`
	PolicyRevision *int64      `json:"policy_revision"`
}

type CreateQueryExecutionParams struct {
	WorkspaceID     pgtype.UUID `json:"workspace_id"`
	UserID          pgtype.UUID `json:"user_id"`
	SavedQueryID    pgtype.UUID `json:"saved_query_id"`
	Status          string      `json:"status"`
	Sql             string      `json:"sql"`
	TrinoCatalog    *string     `json:"trino_catalog"`
	RequestID       *string     `json:"request_id"`
	EngineID        pgtype.UUID `json:"engine_id"`
	TargetBindingID pgtype.UUID `json:"target_binding_id"`
	TargetCatalogID pgtype.UUID `json:"target_catalog_id"`
	TargetSchema    *string     `json:"target_schema"`
	TargetSnapshot  []byte      `json:"target_snapshot"`
	AgentID         pgtype.UUID `json:"agent_id"`
	QueueClass      *string     `json:"queue_class"`
	QueueReason     *string     `json:"queue_reason"`
}

type CreateQueryFolderParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	SpaceID     pgtype.UUID `json:"space_id"`
	ParentID    pgtype.UUID `json:"parent_id"`
	Name        string      `json:"name"`
	Slug        string      `json:"slug"`
}

type CreateQueryStatementParams struct {
	ExecutionID pgtype.UUID `json:"execution_id"`
	Position    int32       `json:"position"`
	Sql         string      `json:"sql"`
	Status      string      `json:"status"`
}

type CreateRefreshTokenParams struct {
	UserID    pgtype.UUID        `json:"user_id"`
	TokenHash string             `json:"token_hash"`
	ExpiresAt pgtype.Timestamptz `json:"expires_at"`
	FamilyID  pgtype.UUID        `json:"family_id"`
}

type CreateRetrieverParams struct {
	WorkspaceID   pgtype.UUID        `json:"workspace_id"`
	CreatedBy     pgtype.UUID        `json:"created_by"`
	Name          string             `json:"name"`
	DefinitionSql string             `json:"definition_sql"`
	DestCatalog   string             `json:"dest_catalog"`
	DestNamespace string             `json:"dest_namespace"`
	DestTable     string             `json:"dest_table"`
	DestBindingID pgtype.UUID        `json:"dest_binding_id"`
	CronSchedule  string             `json:"cron_schedule"`
	RefreshType   string             `json:"refresh_type"`
	NextRefreshAt pgtype.Timestamptz `json:"next_refresh_at"`
}

type CreateRetrieverRefreshHistoryParams struct {
	RetrieverID        pgtype.UUID `json:"retriever_id"`
	TemporalWorkflowID string      `json:"temporal_workflow_id"`
	RefreshType        string      `json:"refresh_type"`
	Status             string      `json:"status"`
}

type CreateSavedQueryParams struct {
	WorkspaceID     pgtype.UUID `json:"workspace_id"`
	SpaceID         pgtype.UUID `json:"space_id"`
	FolderID        pgtype.UUID `json:"folder_id"`
	CreatedBy       pgtype.UUID `json:"created_by"`
	UpdatedBy       pgtype.UUID `json:"updated_by"`
	Name            string      `json:"name"`
	Slug            string      `json:"slug"`
	Sql             string      `json:"sql"`
	TargetBindingID pgtype.UUID `json:"target_binding_id"`
	TargetSchema    *string     `json:"target_schema"`
}

type CreateSemanticMetricParams struct {
	WorkspaceID   pgtype.UUID `json:"workspace_id"`
	SpaceID       pgtype.UUID `json:"space_id"`
	Slug          string      `json:"slug"`
	Label         string      `json:"label"`
	Description   string      `json:"description"`
	Certified     bool        `json:"certified"`
	CatalogID     pgtype.UUID `json:"catalog_id"`
	Namespace     []string    `json:"namespace"`
	TableName     string      `json:"table_name"`
	BindingID     pgtype.UUID `json:"binding_id"`
	Definition    []byte      `json:"definition"`
	FormatVersion int32       `json:"format_version"`
	OwnerUserID   pgtype.UUID `json:"owner_user_id"`
	UpdatedBy     pgtype.UUID `json:"updated_by"`
}

type CreateSemanticModelParams struct {
	WorkspaceID   pgtype.UUID `json:"workspace_id"`
	SpaceID       pgtype.UUID `json:"space_id"`
	Slug          string      `json:"slug"`
	Label         string      `json:"label"`
	Description   string      `json:"description"`
	Certified     bool        `json:"certified"`
	CatalogID     pgtype.UUID `json:"catalog_id"`
	BindingID     pgtype.UUID `json:"binding_id"`
	Definition    []byte      `json:"definition"`
	FormatVersion int32       `json:"format_version"`
	OwnerUserID   pgtype.UUID `json:"owner_user_id"`
	UpdatedBy     pgtype.UUID `json:"updated_by"`
}

type CreateServiceAccountParams struct {
	Name            string      `json:"name"`
	Email           string      `json:"email"`
	AuthProvider    string      `json:"auth_provider"`
	AuthProviderID  pgtype.UUID `json:"auth_provider_id"`
	ExternalID      *string     `json:"external_id"`
	HomeWorkspaceID pgtype.UUID `json:"home_workspace_id"`
	Description     *string     `json:"description"`
}

type CreateSpaceParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Name        string      `json:"name"`
	Slug        string      `json:"slug"`
	Description *string     `json:"description"`
	Visibility  string      `json:"visibility"`
	DefaultRole string      `json:"default_role"`
	CreatedBy   pgtype.UUID `json:"created_by"`
}

type CreateTableMaintenancePolicyParams struct {
	CatalogID                   pgtype.UUID `json:"catalog_id"`
	Namespace                   string      `json:"namespace"`
	TableName                   string      `json:"table_name"`
	Mode                        string      `json:"mode"`
	WriterCommitIntervalSeconds int32       `json:"writer_commit_interval_seconds"`
	Optimize                    []byte      `json:"optimize"`
	OptimizeManifests           []byte      `json:"optimize_manifests"`
	ExpireSnapshots             []byte      `json:"expire_snapshots"`
	RemoveOrphanFiles           []byte      `json:"remove_orphan_files"`
	Actor                       pgtype.UUID `json:"actor"`
}

type CreateTeamParams struct {
	Name        string      `json:"name"`
	Description *string     `json:"description"`
	CreatedBy   pgtype.UUID `json:"created_by"`
}

type CreateUserParams struct {
	Name           string      `json:"name"`
	Email          string      `json:"email"`
	PasswordHash   *string     `json:"password_hash"`
	Role           string      `json:"role"`
	AuthProvider   string      `json:"auth_provider"`
	AuthProviderID pgtype.UUID `json:"auth_provider_id"`
	ExternalID     *string     `json:"external_id"`
}

type CreateWorkloadRuleParams struct {
	Position  int32       `json:"position"`
	Enabled   bool        `json:"enabled"`
	Match     []byte      `json:"match"`
	Class     string      `json:"class"`
	MaxClass  *string     `json:"max_class"`
	Weight    int32       `json:"weight"`
	CreatedBy pgtype.UUID `json:"created_by"`
}

type CreateWorkspaceParams struct {
	Name               string      `json:"name"`
	Description        *string     `json:"description"`
	Type               string      `json:"type"`
	PolarisCatalogName string      `json:"polaris_catalog_name"`
	OwnerUserID        pgtype.UUID `json:"owner_user_id"`
	OwnerTeamID        pgtype.UUID `json:"owner_team_id"`
}

type DeleteAPIKeyParams struct {
	ID     pgtype.UUID `json:"id"`
	UserID pgtype.UUID `json:"user_id"`
}

type DeleteCatalogGrantRequestParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
}

type DeleteEphemeralRegistryRowParams struct {
	ClusterID   pgtype.UUID `json:"cluster_id"`
	CatalogName string      `json:"catalog_name"`
}

type DeleteQueryFolderParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type DeleteRetrieverParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type DeleteSavedQueryParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type DeleteSemanticDocParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
	Namespace   []string    `json:"namespace"`
	TableName   string      `json:"table_name"`
	ColumnName  *string     `json:"column_name"`
}

type DeleteSemanticMetricParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	ID          pgtype.UUID `json:"id"`
}

type DeleteSemanticModelParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	ID          pgtype.UUID `json:"id"`
}

type DeleteSpaceMemberParams struct {
	SpaceID       pgtype.UUID `json:"space_id"`
	PrincipalType string      `json:"principal_type"`
	PrincipalID   pgtype.UUID `json:"principal_id"`
}

type DeleteSpaceParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	ID          pgtype.UUID `json:"id"`
}

type DeleteTableMaintenancePolicyParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace string      `json:"namespace"`
	TableName string      `json:"table_name"`
}

type DeleteWorkspaceCatalogGrantParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
}

type DeleteWorkspaceMemberParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
}

type DisableMissingNativeBindingsParams struct {
	EngineID pgtype.UUID `json:"engine_id"`
	Present  []string    `json:"present"`
}

type FailQueryExecutionParams struct {
	ID        pgtype.UUID `json:"id"`
	ErrorCode *string     `json:"error_code"`
}

type FinishDirectoryReconcileRunParams struct {
	ID         pgtype.UUID `json:"id"`
	Outcome    *string     `json:"outcome"`
	Seen       int32       `json:"seen"`
	Suspended  int32       `json:"suspended"`
	Reinstated int32       `json:"reinstated"`
	Error      *string     `json:"error"`
	Actions    []byte      `json:"actions"`
}

type FinishMetadataSyncRunParams struct {
	ID            pgtype.UUID `json:"id"`
	Status        string      `json:"status"`
	TablesSeen    *int32      `json:"tables_seen"`
	TablesChanged *int32      `json:"tables_changed"`
	TablesRemoved *int32      `json:"tables_removed"`
	ErrorCode     *string     `json:"error_code"`
	ErrorMessage  *string     `json:"error_message"`
}

type FolderSlugExistsParams struct {
	SpaceID pgtype.UUID `json:"space_id"`
	Slug    string      `json:"slug"`
}

type GetAPIKeyByIDParams struct {
	ID     pgtype.UUID `json:"id"`
	UserID pgtype.UUID `json:"user_id"`
}

type GetAgentTokenByIDParams struct {
	ID      pgtype.UUID `json:"id"`
	AgentID pgtype.UUID `json:"agent_id"`
}

type GetAppliedCatalogRetentionParams struct {
	EngineID    pgtype.UUID `json:"engine_id"`
	CatalogName string      `json:"catalog_name"`
}

type GetAppliedCatalogRetentionRow struct {
	Properties []byte `json:"properties"`
	UpdatedBy  string `json:"updated_by"`
}

type GetAuditExportCursorRow struct {
	OccurredAt pgtype.Timestamptz `json:"occurred_at"`
	ID         int64              `json:"id"`
}

type GetBindingForTargetRow struct {
	BindingID         pgtype.UUID `json:"binding_id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	BindingEnabled    bool        `json:"binding_enabled"`
	Mode              string      `json:"mode"`
	EngineID          pgtype.UUID `json:"engine_id"`
	EngineName        string      `json:"engine_name"`
	EngineKind        string      `json:"engine_kind"`
	EngineEnabled     bool        `json:"engine_enabled"`
	CatalogID         pgtype.UUID `json:"catalog_id"`
	CatalogName       string      `json:"catalog_name"`
	CatalogEnabled    bool        `json:"catalog_enabled"`
}

type GetBindingTargetNamesRow struct {
	CatalogName string `json:"catalog_name"`
	EngineName  string `json:"engine_name"`
}

type GetCatalogBindingForDisplayParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	BindingID pgtype.UUID `json:"binding_id"`
}

type GetCatalogBindingForDisplayRow struct {
	ID                pgtype.UUID `json:"id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	EngineKind        string      `json:"engine_kind"`
}

type GetCatalogByTrinoNameParams struct {
	ClusterID        pgtype.UUID `json:"cluster_id"`
	TrinoCatalogName string      `json:"trino_catalog_name"`
}

type GetCatalogForSyncRow struct {
	ID             pgtype.UUID `json:"id"`
	Name           string      `json:"name"`
	Origin         string      `json:"origin"`
	SourceRef      *string     `json:"source_ref"`
	Warehouse      *string     `json:"warehouse"`
	Enabled        bool        `json:"enabled"`
	ConnectionID   pgtype.UUID `json:"connection_id"`
	ConnectionType string      `json:"connection_type"`
}

type GetCatalogGrantRequestParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
}

type GetCatalogSyncStateRow struct {
	LastStatus    string             `json:"last_status"`
	LastErrorCode string             `json:"last_error_code"`
	SyncedAt      pgtype.Timestamptz `json:"synced_at"`
}

type GetContentVersionParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	EntityType  string      `json:"entity_type"`
	EntityID    pgtype.UUID `json:"entity_id"`
	Version     int32       `json:"version"`
}

type GetContentVersionRow struct {
	ID           int64              `json:"id"`
	WorkspaceID  pgtype.UUID        `json:"workspace_id"`
	SpaceID      pgtype.UUID        `json:"space_id"`
	EntityType   string             `json:"entity_type"`
	EntityID     pgtype.UUID        `json:"entity_id"`
	Version      int32              `json:"version"`
	ChangeKind   string             `json:"change_kind"`
	Snapshot     []byte             `json:"snapshot"`
	AuthorID     pgtype.UUID        `json:"author_id"`
	RestoredFrom *int32             `json:"restored_from"`
	CreatedAt    pgtype.Timestamptz `json:"created_at"`
	AuthorName   *string            `json:"author_name"`
}

type GetDefaultTrinoEngineRow struct {
	Engine            Engine            `json:"engine"`
	CatalogConnection CatalogConnection `json:"catalog_connection"`
}

type GetEffectiveSpaceRoleParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	SpaceID     pgtype.UUID `json:"space_id"`
	UserID      pgtype.UUID `json:"user_id"`
}

type GetEngineWithConnectionRow struct {
	Engine            Engine            `json:"engine"`
	CatalogConnection CatalogConnection `json:"catalog_connection"`
}

type GetFolderBySlugParams struct {
	SpaceID pgtype.UUID `json:"space_id"`
	Slug    string      `json:"slug"`
}

type GetGrantedCatalogByNameParams struct {
	Name        string      `json:"name"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetGrantedMetadataCatalogParams struct {
	CatalogID   pgtype.UUID `json:"catalog_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetGrantedMetadataCatalogRow struct {
	ID               pgtype.UUID        `json:"id"`
	MetadataSyncedAt pgtype.Timestamptz `json:"metadata_synced_at"`
}

type GetGrantedMetadataTableParams struct {
	TableID     pgtype.UUID `json:"table_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetGrantedMetadataTableRow struct {
	ID               pgtype.UUID        `json:"id"`
	CatalogID        pgtype.UUID        `json:"catalog_id"`
	Namespace        []string           `json:"namespace"`
	Name             string             `json:"name"`
	Kind             string             `json:"kind"`
	Properties       []byte             `json:"properties"`
	Comment          *string            `json:"comment"`
	LastChangedAt    pgtype.Timestamptz `json:"last_changed_at"`
	RemovedAt        pgtype.Timestamptz `json:"removed_at"`
	MetadataSyncedAt pgtype.Timestamptz `json:"metadata_synced_at"`
}

type GetManagedBindingOfCatalogParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	EngineID  pgtype.UUID `json:"engine_id"`
}

type GetManagedConnectionOfEngineCatalogParams struct {
	EngineID          pgtype.UUID `json:"engine_id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
}

type GetMetadataTableByNameParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace []string    `json:"namespace"`
	Name      string      `json:"name"`
}

type GetMetadataTableByRefParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace []string    `json:"namespace"`
	Name      string      `json:"name"`
}

type GetMetadataTableByRefRow struct {
	ID        pgtype.UUID `json:"id"`
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace []string    `json:"namespace"`
	Name      string      `json:"name"`
	Kind      string      `json:"kind"`
}

type GetQueryExecutionByIDAndWorkspaceParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetQueryFolderByIDParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetQueryStatementByPositionParams struct {
	ExecutionID pgtype.UUID `json:"execution_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Position    int32       `json:"position"`
}

type GetReconcileRunParams struct {
	EngineID pgtype.UUID `json:"engine_id"`
	ID       pgtype.UUID `json:"id"`
}

type GetRetrieverByIDParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetRunningMaintenanceRunParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace string      `json:"namespace"`
	TableName string      `json:"table_name"`
}

type GetSavedQueryByIDParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetSavedQueryBySlugParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Slug        string      `json:"slug"`
}

type GetSavedQueryForUpdateParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetSemanticDocParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
	Namespace   []string    `json:"namespace"`
	TableName   string      `json:"table_name"`
	ColumnName  *string     `json:"column_name"`
}

type GetSemanticMetricBySlugParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Slug        string      `json:"slug"`
}

type GetSemanticMetricForUpdateParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetSemanticMetricParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	ID          pgtype.UUID `json:"id"`
}

type GetSemanticModelBySlugParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Slug        string      `json:"slug"`
}

type GetSemanticModelForUpdateParams struct {
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetSemanticModelParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	ID          pgtype.UUID `json:"id"`
}

type GetServiceAccountByClientParams struct {
	AuthProviderID pgtype.UUID `json:"auth_provider_id"`
	ExternalID     string      `json:"external_id"`
}

type GetSpaceParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	ID          pgtype.UUID `json:"id"`
}

type GetStatementReferenceTargetParams struct {
	ExecutionID pgtype.UUID `json:"execution_id"`
	Position    int32       `json:"position"`
}

type GetStatementReferenceTargetRow struct {
	StatementID pgtype.UUID `json:"statement_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetTableFingerprintsRow struct {
	Namespace   []string `json:"namespace"`
	Name        string   `json:"name"`
	Fingerprint string   `json:"fingerprint"`
	Properties  []byte   `json:"properties"`
}

type GetTableHealthByTableParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace string      `json:"namespace"`
	TableName string      `json:"table_name"`
}

type GetTableHealthSnapshotParams struct {
	CatalogConnectionID pgtype.UUID `json:"catalog_connection_id"`
	Catalog             string      `json:"catalog"`
	Namespace           string      `json:"namespace"`
	TableName           string      `json:"table_name"`
}

type GetTableMaintenancePolicyParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace string      `json:"namespace"`
	TableName string      `json:"table_name"`
}

type GetTeamMemberRoleParams struct {
	TeamID pgtype.UUID `json:"team_id"`
	UserID pgtype.UUID `json:"user_id"`
}

type GetTrinoCatalogRegistryStateParams struct {
	ClusterID   pgtype.UUID `json:"cluster_id"`
	CatalogName string      `json:"catalog_name"`
}

type GetTrinoCatalogRegistryStateRow struct {
	CatalogVersion *string `json:"catalog_version"`
	Enabled        bool    `json:"enabled"`
	SyncStatus     string  `json:"sync_status"`
	UpdatedBy      string  `json:"updated_by"`
}

type GetTrinoQuerySnapshotByEngineParams struct {
	EngineID     pgtype.UUID `json:"engine_id"`
	TrinoQueryID string      `json:"trino_query_id"`
}

type GetTrinoResourceGroupRevisionParams struct {
	EngineID pgtype.UUID `json:"engine_id"`
	Revision int32       `json:"revision"`
}

type GetUserByExternalIDParams struct {
	AuthProviderID pgtype.UUID `json:"auth_provider_id"`
	ExternalID     *string     `json:"external_id"`
}

type GetWorkspaceMemberRoleParams struct {
	ID     pgtype.UUID `json:"id"`
	UserID pgtype.UUID `json:"user_id"`
}

type InsertAuditEventParams struct {
	ActorKind    string      `json:"actor_kind"`
	ActorID      *string     `json:"actor_id"`
	AuthMethod   *string     `json:"auth_method"`
	Action       string      `json:"action"`
	ResourceType string      `json:"resource_type"`
	ResourceID   *string     `json:"resource_id"`
	WorkspaceID  pgtype.UUID `json:"workspace_id"`
	Outcome      string      `json:"outcome"`
	Reason       *string     `json:"reason"`
	RequestID    *string     `json:"request_id"`
	TraceID      *string     `json:"trace_id"`
	ClientIp     *netip.Addr `json:"client_ip"`
	UserAgent    *string     `json:"user_agent"`
	Changes      []byte      `json:"changes"`
	OnBehalfOf   pgtype.UUID `json:"on_behalf_of"`
}

type InsertClusterSnapshotParams struct {
	EngineID         pgtype.UUID `json:"engine_id"`
	ActiveWorkers    int32       `json:"active_workers"`
	RunningQueries   int32       `json:"running_queries"`
	QueuedQueries    int32       `json:"queued_queries"`
	BlockedQueries   int32       `json:"blocked_queries"`
	TotalMemoryBytes *int64      `json:"total_memory_bytes"`
	FreeMemoryBytes  *int64      `json:"free_memory_bytes"`
	RawPayload       []byte      `json:"raw_payload"`
}

type InsertContentVersionParams struct {
	WorkspaceID  pgtype.UUID `json:"workspace_id"`
	SpaceID      pgtype.UUID `json:"space_id"`
	EntityType   string      `json:"entity_type"`
	EntityID     pgtype.UUID `json:"entity_id"`
	Version      int32       `json:"version"`
	ChangeKind   string      `json:"change_kind"`
	Snapshot     []byte      `json:"snapshot"`
	AuthorID     pgtype.UUID `json:"author_id"`
	RestoredFrom *int32      `json:"restored_from"`
}

type InsertEphemeralRegistryRowParams struct {
	ClusterID      pgtype.UUID     `json:"cluster_id"`
	CatalogName    string          `json:"catalog_name"`
	ConnectorName  string          `json:"connector_name"`
	Column4        []byte          `json:"column_4"`
	CatalogVersion *string         `json:"catalog_version"`
	Ttl            pgtype.Interval `json:"ttl"`
}

type InsertMetadataColumnsParams struct {
	TableID     pgtype.UUID `json:"table_id"`
	TypeSystem  string      `json:"type_system"`
	Ordinals    []int32     `json:"ordinals"`
	Names       []string    `json:"names"`
	NativeTypes []string    `json:"native_types"`
	Nullables   []bool      `json:"nullables"`
	Comments    []string    `json:"comments"`
}

type InsertMetadataForeignKeysParams struct {
	CatalogID   pgtype.UUID `json:"catalog_id"`
	TableID     pgtype.UUID `json:"table_id"`
	ForeignKeys []byte      `json:"foreign_keys"`
}

type InsertReconcileRunParams struct {
	EngineID   pgtype.UUID        `json:"engine_id"`
	StartedAt  pgtype.Timestamptz `json:"started_at"`
	FinishedAt pgtype.Timestamptz `json:"finished_at"`
	DryRun     bool               `json:"dry_run"`
	Actions    []byte             `json:"actions"`
	Adopted    []string           `json:"adopted"`
	Error      *string            `json:"error"`
}

type InsertReconcileRunRow struct {
	ID         pgtype.UUID        `json:"id"`
	EngineID   pgtype.UUID        `json:"engine_id"`
	StartedAt  pgtype.Timestamptz `json:"started_at"`
	FinishedAt pgtype.Timestamptz `json:"finished_at"`
	DryRun     bool               `json:"dry_run"`
	Actions    []byte             `json:"actions"`
	Adopted    []string           `json:"adopted"`
	Error      *string            `json:"error"`
}

type InsertStatementJoinParams struct {
	StatementID pgtype.UUID `json:"statement_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	LeftAlias   string      `json:"left_alias"`
	LeftColumn  string      `json:"left_column"`
	RightAlias  string      `json:"right_alias"`
	RightColumn string      `json:"right_column"`
}

type InsertStatementReferenceParams struct {
	StatementID pgtype.UUID `json:"statement_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
	Namespace   string      `json:"namespace"`
	TableName   string      `json:"table_name"`
	Alias       string      `json:"alias"`
	Kind        string      `json:"kind"`
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
	EngineID pgtype.UUID `json:"engine_id"`
	Revision int32       `json:"revision"`
	Config   []byte      `json:"config"`
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
	ID     pgtype.UUID `json:"id"`
	UserID pgtype.UUID `json:"user_id"`
}

type IsWorkspaceStructuralOwnerParams struct {
	UserID pgtype.UUID `json:"user_id"`
	ID     pgtype.UUID `json:"id"`
}

type LastSuccessfulMaintenanceByOperationParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace string      `json:"namespace"`
	TableName string      `json:"table_name"`
}

type LastSuccessfulMaintenanceByOperationRow struct {
	Operation  string             `json:"operation"`
	FinishedAt pgtype.Timestamptz `json:"finished_at"`
}

type LatestContentVersionParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	EntityType  string      `json:"entity_type"`
	EntityID    pgtype.UUID `json:"entity_id"`
}

type LatestReconcileRunsRow struct {
	EngineID   pgtype.UUID        `json:"engine_id"`
	StartedAt  pgtype.Timestamptz `json:"started_at"`
	FinishedAt pgtype.Timestamptz `json:"finished_at"`
	DryRun     bool               `json:"dry_run"`
	Error      *string            `json:"error"`
}

type LatestRetrieverRefreshRow struct {
	Status    string  `json:"status"`
	ErrorCode *string `json:"error_code"`
}

type LinkTrinoClusterEngineParams struct {
	ID       pgtype.UUID `json:"id"`
	EngineID pgtype.UUID `json:"engine_id"`
}

type ListAllVisibleQueryFoldersParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
	SpaceID     pgtype.UUID `json:"space_id"`
}

type ListAttentionBindingErrorsRow struct {
	ID             pgtype.UUID        `json:"id"`
	CatalogID      pgtype.UUID        `json:"catalog_id"`
	CatalogName    string             `json:"catalog_name"`
	EngineName     string             `json:"engine_name"`
	LifecycleState string             `json:"lifecycle_state"`
	StateChangedAt pgtype.Timestamptz `json:"state_changed_at"`
}

type ListAttentionMetadataStaleRow struct {
	CatalogID   pgtype.UUID        `json:"catalog_id"`
	CatalogName string             `json:"catalog_name"`
	Since       pgtype.Timestamptz `json:"since"`
}

type ListAttentionMetadataSyncFailedRow struct {
	CatalogID   pgtype.UUID        `json:"catalog_id"`
	CatalogName string             `json:"catalog_name"`
	FinishedAt  pgtype.Timestamptz `json:"finished_at"`
	ErrorCode   *string            `json:"error_code"`
}

type ListAttentionReconcileDriftRow struct {
	EngineID    pgtype.UUID        `json:"engine_id"`
	EngineName  string             `json:"engine_name"`
	FinishedAt  pgtype.Timestamptz `json:"finished_at"`
	ActionCount int32              `json:"action_count"`
}

type ListAttentionRetrieverErrorsRow struct {
	ID            pgtype.UUID        `json:"id"`
	Name          string             `json:"name"`
	WorkspaceID   pgtype.UUID        `json:"workspace_id"`
	WorkspaceName string             `json:"workspace_name"`
	UpdatedAt     pgtype.Timestamptz `json:"updated_at"`
}

type ListAuditEventsForExportParams struct {
	AfterAt pgtype.Timestamptz `json:"after_at"`
	AfterID int64              `json:"after_id"`
	Before  pgtype.Timestamptz `json:"before"`
	Lim     int32              `json:"lim"`
}

type ListAuditEventsForExportRow struct {
	OccurredAt   pgtype.Timestamptz `json:"occurred_at"`
	ID           int64              `json:"id"`
	ActorKind    string             `json:"actor_kind"`
	ActorID      string             `json:"actor_id"`
	AuthMethod   string             `json:"auth_method"`
	Action       string             `json:"action"`
	ResourceType string             `json:"resource_type"`
	ResourceID   string             `json:"resource_id"`
	WorkspaceID  string             `json:"workspace_id"`
	Outcome      string             `json:"outcome"`
	Reason       string             `json:"reason"`
	RequestID    string             `json:"request_id"`
	TraceID      string             `json:"trace_id"`
	ClientIp     string             `json:"client_ip"`
	UserAgent    string             `json:"user_agent"`
	Changes      []byte             `json:"changes"`
}

type ListAuditEventsParams struct {
	ResourceType *string            `json:"resource_type"`
	ResourceID   *string            `json:"resource_id"`
	ActorID      *string            `json:"actor_id"`
	Action       *string            `json:"action"`
	Outcome      *string            `json:"outcome"`
	RequestID    *string            `json:"request_id"`
	TraceID      *string            `json:"trace_id"`
	ScopeActor   *string            `json:"scope_actor"`
	ScopeOwner   pgtype.UUID        `json:"scope_owner"`
	FromTime     pgtype.Timestamptz `json:"from_time"`
	ToTime       pgtype.Timestamptz `json:"to_time"`
	CursorAt     pgtype.Timestamptz `json:"cursor_at"`
	CursorID     *int64             `json:"cursor_id"`
	Lim          int32              `json:"lim"`
}

type ListAuditEventsRow struct {
	ID           int64              `json:"id"`
	OccurredAt   pgtype.Timestamptz `json:"occurred_at"`
	ActorKind    string             `json:"actor_kind"`
	ActorID      *string            `json:"actor_id"`
	AuthMethod   *string            `json:"auth_method"`
	Action       string             `json:"action"`
	ResourceType string             `json:"resource_type"`
	ResourceID   *string            `json:"resource_id"`
	WorkspaceID  pgtype.UUID        `json:"workspace_id"`
	Outcome      string             `json:"outcome"`
	Reason       *string            `json:"reason"`
	RequestID    *string            `json:"request_id"`
	TraceID      *string            `json:"trace_id"`
	ClientIp     *netip.Addr        `json:"client_ip"`
	UserAgent    *string            `json:"user_agent"`
	Changes      []byte             `json:"changes"`
}

type ListCatalogGrantRequestsForCatalogRow struct {
	WorkspaceID     pgtype.UUID        `json:"workspace_id"`
	CatalogID       pgtype.UUID        `json:"catalog_id"`
	InitiatedBySide string             `json:"initiated_by_side"`
	RequestedBy     pgtype.UUID        `json:"requested_by"`
	RequestedAt     pgtype.Timestamptz `json:"requested_at"`
	WorkspaceName   string             `json:"workspace_name"`
	WorkspaceType   string             `json:"workspace_type"`
}

type ListCatalogGrantRequestsForWorkspaceRow struct {
	WorkspaceID       pgtype.UUID        `json:"workspace_id"`
	CatalogID         pgtype.UUID        `json:"catalog_id"`
	InitiatedBySide   string             `json:"initiated_by_side"`
	RequestedBy       pgtype.UUID        `json:"requested_by"`
	RequestedAt       pgtype.Timestamptz `json:"requested_at"`
	CatalogName       string             `json:"catalog_name"`
	EngineCatalogName string             `json:"engine_catalog_name"`
	ConnectionName    string             `json:"connection_name"`
}

type ListCatalogGrantsRow struct {
	WorkspaceID   pgtype.UUID        `json:"workspace_id"`
	WorkspaceName string             `json:"workspace_name"`
	WorkspaceType string             `json:"workspace_type"`
	GrantedBy     pgtype.UUID        `json:"granted_by"`
	GrantedAt     pgtype.Timestamptz `json:"granted_at"`
}

type ListCatalogLastSuccessfulMaintenanceRow struct {
	Namespace  string             `json:"namespace"`
	TableName  string             `json:"table_name"`
	Operation  string             `json:"operation"`
	FinishedAt pgtype.Timestamptz `json:"finished_at"`
}

type ListCatalogRunningMaintenanceRow struct {
	Namespace string `json:"namespace"`
	TableName string `json:"table_name"`
}

type ListChildNamespacesParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Parent    []string    `json:"parent"`
}

type ListClusterSnapshotsByEngineParams struct {
	EngineID pgtype.UUID        `json:"engine_id"`
	FromTime pgtype.Timestamptz `json:"from_time"`
	Lim      int32              `json:"lim"`
}

type ListClusterSnapshotsParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

type ListContentVersionsParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	EntityType  string      `json:"entity_type"`
	EntityID    pgtype.UUID `json:"entity_id"`
	Off         int32       `json:"off"`
	Lim         int32       `json:"lim"`
}

type ListContentVersionsRow struct {
	ID           int64              `json:"id"`
	WorkspaceID  pgtype.UUID        `json:"workspace_id"`
	SpaceID      pgtype.UUID        `json:"space_id"`
	EntityType   string             `json:"entity_type"`
	EntityID     pgtype.UUID        `json:"entity_id"`
	Version      int32              `json:"version"`
	ChangeKind   string             `json:"change_kind"`
	Snapshot     []byte             `json:"snapshot"`
	AuthorID     pgtype.UUID        `json:"author_id"`
	RestoredFrom *int32             `json:"restored_from"`
	CreatedAt    pgtype.Timestamptz `json:"created_at"`
	AuthorName   *string            `json:"author_name"`
}

type ListDeletedContentParams struct {
	EntityType  *string     `json:"entity_type"`
	Off         int32       `json:"off"`
	Lim         int32       `json:"lim"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type ListDeletedContentRow struct {
	ID           int64              `json:"id"`
	WorkspaceID  pgtype.UUID        `json:"workspace_id"`
	SpaceID      pgtype.UUID        `json:"space_id"`
	EntityType   string             `json:"entity_type"`
	EntityID     pgtype.UUID        `json:"entity_id"`
	Version      int32              `json:"version"`
	ChangeKind   string             `json:"change_kind"`
	Snapshot     []byte             `json:"snapshot"`
	AuthorID     pgtype.UUID        `json:"author_id"`
	RestoredFrom *int32             `json:"restored_from"`
	CreatedAt    pgtype.Timestamptz `json:"created_at"`
	AuthorName   *string            `json:"author_name"`
	SpaceName    *string            `json:"space_name"`
}

type ListDirectoryReconcileRunsParams struct {
	Column1 pgtype.UUID `json:"column_1"`
	Limit   int32       `json:"limit"`
}

type ListDiscoverableCatalogsRow struct {
	ID                pgtype.UUID `json:"id"`
	Name              string      `json:"name"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	Description       *string     `json:"description"`
	ConnectionName    string      `json:"connection_name"`
	CreatedBy         pgtype.UUID `json:"created_by"`
}

type ListEnabledEnginesWithConnectionRow struct {
	Engine            Engine            `json:"engine"`
	CatalogConnection CatalogConnection `json:"catalog_connection"`
	ClusterID         pgtype.UUID       `json:"cluster_id"`
}

type ListEnabledIcebergCatalogsRow struct {
	ID               pgtype.UUID `json:"id"`
	ConnectionID     pgtype.UUID `json:"connection_id"`
	Name             string      `json:"name"`
	TrinoCatalogName string      `json:"trino_catalog_name"`
	SourceRef        *string     `json:"source_ref"`
	Warehouse        *string     `json:"warehouse"`
}

type ListExecutionHistoryParams struct {
	WorkspaceID pgtype.UUID        `json:"workspace_id"`
	UserID      pgtype.UUID        `json:"user_id"`
	Status      *string            `json:"status"`
	QueueClass  *string            `json:"queue_class"`
	FromTime    pgtype.Timestamptz `json:"from_time"`
	ToTime      pgtype.Timestamptz `json:"to_time"`
	Q           *string            `json:"q"`
	CatalogID   pgtype.UUID        `json:"catalog_id"`
	EngineID    pgtype.UUID        `json:"engine_id"`
	CursorAt    pgtype.Timestamptz `json:"cursor_at"`
	CursorID    pgtype.UUID        `json:"cursor_id"`
	Lim         int32              `json:"lim"`
}

type ListExecutionHistoryRow struct {
	ID              pgtype.UUID        `json:"id"`
	WorkspaceID     pgtype.UUID        `json:"workspace_id"`
	UserID          pgtype.UUID        `json:"user_id"`
	UserName        string             `json:"user_name"`
	SavedQueryID    pgtype.UUID        `json:"saved_query_id"`
	Status          string             `json:"status"`
	Sql             string             `json:"sql"`
	EngineQueryID   *string            `json:"engine_query_id"`
	RowsReturned    *int64             `json:"rows_returned"`
	DurationMs      *int64             `json:"duration_ms"`
	StartedAt       pgtype.Timestamptz `json:"started_at"`
	FinishedAt      pgtype.Timestamptz `json:"finished_at"`
	TrinoCatalog    *string            `json:"trino_catalog"`
	ResultKey       *string            `json:"result_key"`
	RequestID       *string            `json:"request_id"`
	EngineID        pgtype.UUID        `json:"engine_id"`
	TargetBindingID pgtype.UUID        `json:"target_binding_id"`
	TargetCatalogID pgtype.UUID        `json:"target_catalog_id"`
	TargetSchema    *string            `json:"target_schema"`
	TargetSnapshot  []byte             `json:"target_snapshot"`
	QueueClass      *string            `json:"queue_class"`
	QueueReason     *string            `json:"queue_reason"`
	QueuedAt        pgtype.Timestamptz `json:"queued_at"`
	AdmittedAt      pgtype.Timestamptz `json:"admitted_at"`
	QueueWaitMs     *int64             `json:"queue_wait_ms"`
}

type ListExpiredEphemeralRegistryRowsRow struct {
	ClusterID   pgtype.UUID `json:"cluster_id"`
	CatalogName string      `json:"catalog_name"`
}

type ListForeignKeyJoinSuggestionsParams struct {
	LeftTableID    pgtype.UUID `json:"left_table_id"`
	RightNamespace []string    `json:"right_namespace"`
	RightTable     string      `json:"right_table"`
	RightTableID   pgtype.UUID `json:"right_table_id"`
	LeftNamespace  []string    `json:"left_namespace"`
	LeftTable      string      `json:"left_table"`
}

type ListForeignKeyJoinSuggestionsRow struct {
	LeftColumn  string `json:"left_column"`
	RightColumn string `json:"right_column"`
}

type ListGrantableWorkspacesRow struct {
	ID       pgtype.UUID `json:"id"`
	Name     string      `json:"name"`
	Type     string      `json:"type"`
	TeamName *string     `json:"team_name"`
}

type ListGrantedCatalogsOfWorkspaceRow struct {
	ID               pgtype.UUID        `json:"id"`
	ConnectionID     pgtype.UUID        `json:"connection_id"`
	Name             string             `json:"name"`
	Description      *string            `json:"description"`
	TrinoCatalogName *string            `json:"trino_catalog_name"`
	ClusterID        pgtype.UUID        `json:"cluster_id"`
	Warehouse        *string            `json:"warehouse"`
	Connector        *string            `json:"connector"`
	ExtraProps       []byte             `json:"extra_props"`
	Enabled          bool               `json:"enabled"`
	CreatedAt        pgtype.Timestamptz `json:"created_at"`
	UpdatedAt        pgtype.Timestamptz `json:"updated_at"`
	Origin           string             `json:"origin"`
	SourceRef        *string            `json:"source_ref"`
	Discoverable     bool               `json:"discoverable"`
	ConnectionName   string             `json:"connection_name"`
	ConnectionType   string             `json:"connection_type"`
}

type ListJoinSuggestionsParams struct {
	Lim         int32       `json:"lim"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
	LeftNs      string      `json:"left_ns"`
	LeftTable   string      `json:"left_table"`
	RightNs     string      `json:"right_ns"`
	RightTable  string      `json:"right_table"`
}

type ListJoinSuggestionsRow struct {
	LeftColumn  string             `json:"left_column"`
	RightColumn string             `json:"right_column"`
	UseCount    int64              `json:"use_count"`
	LastUsedAt  pgtype.Timestamptz `json:"last_used_at"`
	Score       float64            `json:"score"`
}

type ListLastMaintenancePerOperationParams struct {
	CatalogID  pgtype.UUID `json:"catalog_id"`
	Namespaces []string    `json:"namespaces"`
	Tables     []string    `json:"tables"`
}

type ListLastMaintenancePerOperationRow struct {
	Namespace string             `json:"namespace"`
	TableName string             `json:"table_name"`
	Operation interface{}        `json:"operation"`
	LastAt    pgtype.Timestamptz `json:"last_at"`
}

type ListLastMaintenanceRunsParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace string      `json:"namespace"`
	TableName string      `json:"table_name"`
}

type ListLastMaintenanceRunsRow struct {
	Operation  interface{}        `json:"operation"`
	ID         pgtype.UUID        `json:"id"`
	Status     string             `json:"status"`
	FinishedAt pgtype.Timestamptz `json:"finished_at"`
	WorkflowID string             `json:"workflow_id"`
}

type ListLineageBrokenMetricsRow struct {
	ID          pgtype.UUID        `json:"id"`
	WorkspaceID pgtype.UUID        `json:"workspace_id"`
	SpaceID     pgtype.UUID        `json:"space_id"`
	Slug        string             `json:"slug"`
	Label       string             `json:"label"`
	CatalogID   pgtype.UUID        `json:"catalog_id"`
	Namespace   []string           `json:"namespace"`
	TableName   string             `json:"table_name"`
	UpdatedAt   pgtype.Timestamptz `json:"updated_at"`
}

type ListLineageBrokenModelsRow struct {
	ID          pgtype.UUID        `json:"id"`
	WorkspaceID pgtype.UUID        `json:"workspace_id"`
	SpaceID     pgtype.UUID        `json:"space_id"`
	Slug        string             `json:"slug"`
	Label       string             `json:"label"`
	CatalogID   pgtype.UUID        `json:"catalog_id"`
	Definition  []byte             `json:"definition"`
	UpdatedAt   pgtype.Timestamptz `json:"updated_at"`
}

type ListLineageMetricsParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
}

type ListLineageMetricsRow struct {
	ID          pgtype.UUID        `json:"id"`
	WorkspaceID pgtype.UUID        `json:"workspace_id"`
	SpaceID     pgtype.UUID        `json:"space_id"`
	Slug        string             `json:"slug"`
	Label       string             `json:"label"`
	CatalogID   pgtype.UUID        `json:"catalog_id"`
	Namespace   []string           `json:"namespace"`
	TableName   string             `json:"table_name"`
	UpdatedAt   pgtype.Timestamptz `json:"updated_at"`
}

type ListLineageModelsParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
}

type ListLineageModelsRow struct {
	ID          pgtype.UUID        `json:"id"`
	WorkspaceID pgtype.UUID        `json:"workspace_id"`
	SpaceID     pgtype.UUID        `json:"space_id"`
	Slug        string             `json:"slug"`
	Label       string             `json:"label"`
	CatalogID   pgtype.UUID        `json:"catalog_id"`
	Definition  []byte             `json:"definition"`
	UpdatedAt   pgtype.Timestamptz `json:"updated_at"`
}

type ListLineageSavedQueriesParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
}

type ListLineageSavedQueriesRow struct {
	ID              pgtype.UUID `json:"id"`
	WorkspaceID     pgtype.UUID `json:"workspace_id"`
	SpaceID         pgtype.UUID `json:"space_id"`
	Slug            string      `json:"slug"`
	Name            string      `json:"name"`
	Sql             string      `json:"sql"`
	TargetSchema    *string     `json:"target_schema"`
	TargetCatalogID pgtype.UUID `json:"target_catalog_id"`
}

type ListMaintenanceRunsParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Trigger   *string     `json:"trigger"`
	Operation *string     `json:"operation"`
	Status    *string     `json:"status"`
	Ns        *string     `json:"ns"`
	Tbl       *string     `json:"tbl"`
	Off       int32       `json:"off"`
	Lim       int32       `json:"lim"`
}

type ListMetadataCatalogsRow struct {
	ID                 pgtype.UUID        `json:"id"`
	Name               string             `json:"name"`
	Origin             string             `json:"origin"`
	ProviderKind       string             `json:"provider_kind"`
	MetadataSyncedAt   pgtype.Timestamptz `json:"metadata_synced_at"`
	MetadataSyncStatus string             `json:"metadata_sync_status"`
	Bindings           []byte             `json:"bindings"`
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
	CatalogID pgtype.UUID `json:"catalog_id"`
	Limit     int32       `json:"limit"`
}

type ListMetadataSyncRunsRow struct {
	ID            pgtype.UUID        `json:"id"`
	CatalogID     pgtype.UUID        `json:"catalog_id"`
	Status        string             `json:"status"`
	SourcePath    string             `json:"source_path"`
	StartedAt     pgtype.Timestamptz `json:"started_at"`
	FinishedAt    pgtype.Timestamptz `json:"finished_at"`
	TablesSeen    *int32             `json:"tables_seen"`
	TablesChanged *int32             `json:"tables_changed"`
	TablesRemoved *int32             `json:"tables_removed"`
	ErrorCode     *string            `json:"error_code"`
}

type ListMetadataTablesPageParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace []string    `json:"namespace"`
	After     string      `json:"after"`
	Lim       int32       `json:"lim"`
}

type ListMetadataTablesPageRow struct {
	ID            pgtype.UUID        `json:"id"`
	Name          string             `json:"name"`
	Kind          string             `json:"kind"`
	Comment       *string            `json:"comment"`
	LastChangedAt pgtype.Timestamptz `json:"last_changed_at"`
}

type ListPopularTablesParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Lim         int32       `json:"lim"`
}

type ListPopularTablesRow struct {
	TableID          pgtype.UUID        `json:"table_id"`
	CatalogID        pgtype.UUID        `json:"catalog_id"`
	Namespace        []string           `json:"namespace"`
	Name             string             `json:"name"`
	Kind             string             `json:"kind"`
	Uses             int64              `json:"uses"`
	Score            float64            `json:"score"`
	LastUsedAt       pgtype.Timestamptz `json:"last_used_at"`
	MetadataSyncedAt pgtype.Timestamptz `json:"metadata_synced_at"`
}

type ListQueryClassesParams struct {
	ExecutionIds []pgtype.UUID `json:"execution_ids"`
	QueryIds     []string      `json:"query_ids"`
}

type ListQueryClassesRow struct {
	ID            pgtype.UUID `json:"id"`
	EngineQueryID *string     `json:"engine_query_id"`
	QueueClass    *string     `json:"queue_class"`
}

type ListQueryExecutionsByWorkspaceParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Limit       int32       `json:"limit"`
	Offset      int32       `json:"offset"`
}

type ListQueryStatementsByWorkspaceParams struct {
	ExecutionID pgtype.UUID `json:"execution_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type ListReconcileRunsParams struct {
	EngineID pgtype.UUID `json:"engine_id"`
	Limit    int32       `json:"limit"`
}

type ListRetrieverDestinationsRow struct {
	BindingID         pgtype.UUID `json:"binding_id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	Connector         string      `json:"connector"`
	CatalogID         pgtype.UUID `json:"catalog_id"`
	CatalogName       string      `json:"catalog_name"`
	EngineID          pgtype.UUID `json:"engine_id"`
	EngineName        string      `json:"engine_name"`
	EngineKind        string      `json:"engine_kind"`
}

type ListRetrieverRefreshHistoryParams struct {
	RetrieverID pgtype.UUID `json:"retriever_id"`
	Limit       int32       `json:"limit"`
}

type ListRunningStatementHandlesRow struct {
	Position    int32  `json:"position"`
	QueryHandle []byte `json:"query_handle"`
}

type ListSavedQueryVersionsRow struct {
	EntityID pgtype.UUID `json:"entity_id"`
	Version  int32       `json:"version"`
}

type ListSemanticDocsForTableParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
	Namespace   []string    `json:"namespace"`
	TableName   string      `json:"table_name"`
	ColumnName  *string     `json:"column_name"`
}

type ListSemanticMetricVersionsRow struct {
	EntityID pgtype.UUID `json:"entity_id"`
	Version  int32       `json:"version"`
}

type ListSemanticModelVersionsRow struct {
	EntityID pgtype.UUID `json:"entity_id"`
	Version  int32       `json:"version"`
}

type ListSemanticTagsParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Q           string      `json:"q"`
	Lim         int32       `json:"lim"`
}

type ListSemanticTagsRow struct {
	Tag   string `json:"tag"`
	Count int64  `json:"count"`
}

type ListSpacesForPrincipalParams struct {
	UserID      pgtype.UUID `json:"user_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type ListSpacesForPrincipalRow struct {
	ID          pgtype.UUID        `json:"id"`
	WorkspaceID pgtype.UUID        `json:"workspace_id"`
	Name        string             `json:"name"`
	Description *string            `json:"description"`
	Visibility  string             `json:"visibility"`
	DefaultRole string             `json:"default_role"`
	IsDefault   bool               `json:"is_default"`
	CreatedBy   pgtype.UUID        `json:"created_by"`
	CreatedAt   pgtype.Timestamptz `json:"created_at"`
	UpdatedAt   pgtype.Timestamptz `json:"updated_at"`
	Slug        string             `json:"slug"`
	MyRole      string             `json:"my_role"`
}

type ListStatementsForReferenceBackfillParams struct {
	Cursor pgtype.UUID `json:"cursor"`
	Lim    int32       `json:"lim"`
}

type ListStatementsForReferenceBackfillRow struct {
	ID              pgtype.UUID `json:"id"`
	Sql             string      `json:"sql"`
	WorkspaceID     pgtype.UUID `json:"workspace_id"`
	TargetCatalogID pgtype.UUID `json:"target_catalog_id"`
	TargetSchema    string      `json:"target_schema"`
}

type ListSyncBindingsRow struct {
	ID                pgtype.UUID `json:"id"`
	EngineID          pgtype.UUID `json:"engine_id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	Mode              string      `json:"mode"`
	EngineKind        string      `json:"engine_kind"`
}

type ListTableHealthByCatalogParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Ns        *string     `json:"ns"`
	Needs     *string     `json:"needs"`
	MaxScore  *int32      `json:"max_score"`
	Off       int32       `json:"off"`
	Lim       int32       `json:"lim"`
}

type ListTableHealthByConnectionParams struct {
	CatalogConnectionID pgtype.UUID `json:"catalog_connection_id"`
	Limit               int32       `json:"limit"`
	Offset              int32       `json:"offset"`
}

type ListTableHealthSnapshotsParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

type ListTableMaintenanceRunsParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace string      `json:"namespace"`
	TableName string      `json:"table_name"`
	Lim       int32       `json:"lim"`
}

type ListTeamMembersRow struct {
	ID               pgtype.UUID        `json:"id"`
	TeamID           pgtype.UUID        `json:"team_id"`
	UserID           pgtype.UUID        `json:"user_id"`
	Role             string             `json:"role"`
	JoinedAt         pgtype.Timestamptz `json:"joined_at"`
	UserName         string             `json:"user_name"`
	UserEmail        string             `json:"user_email"`
	UserRole         string             `json:"user_role"`
	UserAuthProvider string             `json:"user_auth_provider"`
	UserStatus       string             `json:"user_status"`
	UserCreatedAt    pgtype.Timestamptz `json:"user_created_at"`
}

type ListTrinoQuerySnapshotsByEngineParams struct {
	EngineID pgtype.UUID `json:"engine_id"`
	State    *string     `json:"state"`
	UserName *string     `json:"user_name"`
	Q        *string     `json:"q"`
	Off      int32       `json:"off"`
	Lim      int32       `json:"lim"`
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
	IsAdmin bool        `json:"is_admin"`
	UserID  pgtype.UUID `json:"user_id"`
}

type ListVisibleCatalogsParams struct {
	IsAdmin bool        `json:"is_admin"`
	UserID  pgtype.UUID `json:"user_id"`
}

type ListVisibleQueryFoldersParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
	SpaceID     pgtype.UUID `json:"space_id"`
	ParentID    pgtype.UUID `json:"parent_id"`
}

type ListVisibleSavedQueriesParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
	SpaceID     pgtype.UUID `json:"space_id"`
	FolderID    pgtype.UUID `json:"folder_id"`
	Q           *string     `json:"q"`
	Lim         *int32      `json:"lim"`
}

type ListVisibleSemanticMetricsParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
	SpaceID     pgtype.UUID `json:"space_id"`
}

type ListVisibleSemanticModelsParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
	SpaceID     pgtype.UUID `json:"space_id"`
}

type ListWorkspaceCatalogGrantsRow struct {
	WorkspaceID pgtype.UUID        `json:"workspace_id"`
	CatalogID   pgtype.UUID        `json:"catalog_id"`
	GrantedAt   pgtype.Timestamptz `json:"granted_at"`
	CatalogName string             `json:"catalog_name"`
}

type ListWorkspaceMembersRow struct {
	User    User   `json:"user"`
	Role    string `json:"role"`
	Source  string `json:"source"`
	RoleSet bool   `json:"role_set"`
}

type ListWorkspacePrincipalUsersParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Lim         int32       `json:"lim"`
}

type ListWorkspacePrincipalUsersRow struct {
	ID    pgtype.UUID `json:"id"`
	Name  string      `json:"name"`
	Email string      `json:"email"`
}

type ListWorkspaceTargetsRow struct {
	BindingID         pgtype.UUID `json:"binding_id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	Mode              string      `json:"mode"`
	BindingEnabled    bool        `json:"binding_enabled"`
	EngineID          pgtype.UUID `json:"engine_id"`
	EngineName        string      `json:"engine_name"`
	EngineKind        string      `json:"engine_kind"`
	EngineEnabled     bool        `json:"engine_enabled"`
	CatalogID         pgtype.UUID `json:"catalog_id"`
	CatalogName       string      `json:"catalog_name"`
	CatalogEnabled    bool        `json:"catalog_enabled"`
}

type MarkEngineCatalogManagedParams struct {
	EngineID    pgtype.UUID `json:"engine_id"`
	CatalogName string      `json:"catalog_name"`
}

type MarkEngineCatalogsAbsentParams struct {
	EngineID     pgtype.UUID `json:"engine_id"`
	PresentNames []string    `json:"present_names"`
}

type MetadataStatusCountsRow struct {
	Catalogs int64 `json:"catalogs"`
	Stale    int64 `json:"stale"`
	Failed   int64 `json:"failed"`
}

type MoveQueryFolderSubtreeParams struct {
	SpaceID     pgtype.UUID `json:"space_id"`
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type MoveSavedQueriesInFolderSubtreeParams struct {
	SpaceID     pgtype.UUID `json:"space_id"`
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type NextContentVersionParams struct {
	EntityType string      `json:"entity_type"`
	EntityID   pgtype.UUID `json:"entity_id"`
}

type OldestExecutionByAgentSinceParams struct {
	AgentID   pgtype.UUID        `json:"agent_id"`
	StartedAt pgtype.Timestamptz `json:"started_at"`
}

type RemoveTeamMemberParams struct {
	TeamID pgtype.UUID `json:"team_id"`
	UserID pgtype.UUID `json:"user_id"`
}

type RestoreSavedQueryParams struct {
	ID              pgtype.UUID `json:"id"`
	WorkspaceID     pgtype.UUID `json:"workspace_id"`
	SpaceID         pgtype.UUID `json:"space_id"`
	FolderID        pgtype.UUID `json:"folder_id"`
	CreatedBy       pgtype.UUID `json:"created_by"`
	UpdatedBy       pgtype.UUID `json:"updated_by"`
	Name            string      `json:"name"`
	Slug            string      `json:"slug"`
	Sql             string      `json:"sql"`
	TargetBindingID pgtype.UUID `json:"target_binding_id"`
	TargetSchema    *string     `json:"target_schema"`
}

type RestoreSemanticMetricParams struct {
	ID            pgtype.UUID `json:"id"`
	WorkspaceID   pgtype.UUID `json:"workspace_id"`
	SpaceID       pgtype.UUID `json:"space_id"`
	Slug          string      `json:"slug"`
	Label         string      `json:"label"`
	Description   string      `json:"description"`
	Certified     bool        `json:"certified"`
	CatalogID     pgtype.UUID `json:"catalog_id"`
	Namespace     []string    `json:"namespace"`
	TableName     string      `json:"table_name"`
	BindingID     pgtype.UUID `json:"binding_id"`
	Definition    []byte      `json:"definition"`
	FormatVersion int32       `json:"format_version"`
	OwnerUserID   pgtype.UUID `json:"owner_user_id"`
	UpdatedBy     pgtype.UUID `json:"updated_by"`
}

type RestoreSemanticModelParams struct {
	ID            pgtype.UUID `json:"id"`
	WorkspaceID   pgtype.UUID `json:"workspace_id"`
	SpaceID       pgtype.UUID `json:"space_id"`
	Slug          string      `json:"slug"`
	Label         string      `json:"label"`
	Description   string      `json:"description"`
	Certified     bool        `json:"certified"`
	CatalogID     pgtype.UUID `json:"catalog_id"`
	BindingID     pgtype.UUID `json:"binding_id"`
	Definition    []byte      `json:"definition"`
	FormatVersion int32       `json:"format_version"`
	OwnerUserID   pgtype.UUID `json:"owner_user_id"`
	UpdatedBy     pgtype.UUID `json:"updated_by"`
}

type RevokeAgentTokenParams struct {
	ID      pgtype.UUID `json:"id"`
	AgentID pgtype.UUID `json:"agent_id"`
}

type SaveUserIDPTokenOnSignInParams struct {
	UserID          pgtype.UUID        `json:"user_id"`
	AuthProviderID  pgtype.UUID        `json:"auth_provider_id"`
	AccessTokenEnc  string             `json:"access_token_enc"`
	RefreshTokenEnc *string            `json:"refresh_token_enc"`
	ExpiresAt       pgtype.Timestamptz `json:"expires_at"`
}

type SavedQuerySlugExistsParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Slug        string      `json:"slug"`
}

type SearchMetadataParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Tags        []string    `json:"tags"`
	Certified   *bool       `json:"certified"`
	Namespace   []string    `json:"namespace"`
	Lim         int32       `json:"lim"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
	Q           string      `json:"q"`
	Kind        string      `json:"kind"`
}

type SearchMetadataRow struct {
	Kind             string             `json:"kind"`
	TableID          pgtype.UUID        `json:"table_id"`
	CatalogID        pgtype.UUID        `json:"catalog_id"`
	Namespace        []string           `json:"namespace"`
	TableName        string             `json:"table_name"`
	ColumnName       string             `json:"column_name"`
	NativeType       string             `json:"native_type"`
	Score            float64            `json:"score"`
	Popularity       float64            `json:"popularity"`
	MetadataSyncedAt pgtype.Timestamptz `json:"metadata_synced_at"`
}

type SearchSemanticDocsParams struct {
	Q           string      `json:"q"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Tag         *string     `json:"tag"`
	Certified   *bool       `json:"certified"`
	OwnerID     pgtype.UUID `json:"owner_id"`
	Lim         int32       `json:"lim"`
}

type SearchSemanticDocsRow struct {
	ID          pgtype.UUID        `json:"id"`
	WorkspaceID pgtype.UUID        `json:"workspace_id"`
	CatalogID   pgtype.UUID        `json:"catalog_id"`
	Namespace   []string           `json:"namespace"`
	TableName   string             `json:"table_name"`
	ColumnName  *string            `json:"column_name"`
	Description string             `json:"description"`
	Tags        []string           `json:"tags"`
	OwnerUserID pgtype.UUID        `json:"owner_user_id"`
	UpdatedBy   pgtype.UUID        `json:"updated_by"`
	UpdatedAt   pgtype.Timestamptz `json:"updated_at"`
	Certified   bool               `json:"certified"`
	CertifiedBy pgtype.UUID        `json:"certified_by"`
	CertifiedAt pgtype.Timestamptz `json:"certified_at"`
	Score       float64            `json:"score"`
}

type SearchVisibleSavedQueriesParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
	SpaceID     pgtype.UUID `json:"space_id"`
	Q           *string     `json:"q"`
	Lim         *int32      `json:"lim"`
}

type SetAgentStatusParams struct {
	ID     pgtype.UUID `json:"id"`
	Status string      `json:"status"`
}

type SetAuditExportCursorParams struct {
	Sink       string             `json:"sink"`
	OccurredAt pgtype.Timestamptz `json:"occurred_at"`
	ID         int64              `json:"id"`
}

type SetCatalogDiscoverableParams struct {
	ID           pgtype.UUID `json:"id"`
	Discoverable bool        `json:"discoverable"`
}

type SetCatalogEnabledParams struct {
	ID      pgtype.UUID `json:"id"`
	Enabled bool        `json:"enabled"`
}

type SetEngineMaxRunningTotalParams struct {
	ID              pgtype.UUID `json:"id"`
	MaxRunningTotal *int32      `json:"max_running_total"`
}

type SetEngineTrinoResourceGroupsParams struct {
	ID                         pgtype.UUID `json:"id"`
	TrinoResourceGroupsEnabled bool        `json:"trino_resource_groups_enabled"`
}

type SetExecutionResultParams struct {
	ID        pgtype.UUID `json:"id"`
	ResultKey *string     `json:"result_key"`
}

type SetExecutionTargetParams struct {
	ID              pgtype.UUID `json:"id"`
	EngineID        pgtype.UUID `json:"engine_id"`
	TargetBindingID pgtype.UUID `json:"target_binding_id"`
	TargetCatalogID pgtype.UUID `json:"target_catalog_id"`
	TargetSchema    *string     `json:"target_schema"`
	TargetSnapshot  []byte      `json:"target_snapshot"`
}

type SetMaintenanceRunAttemptsParams struct {
	Attempts int32       `json:"attempts"`
	ID       pgtype.UUID `json:"id"`
}

type SetSemanticMetricCertificationParams struct {
	Certified   bool        `json:"certified"`
	Actor       pgtype.UUID `json:"actor"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	ID          pgtype.UUID `json:"id"`
}

type SetSemanticModelCertificationParams struct {
	Certified   bool        `json:"certified"`
	Actor       pgtype.UUID `json:"actor"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	ID          pgtype.UUID `json:"id"`
}

type SetSpaceMemberParams struct {
	SpaceID       pgtype.UUID `json:"space_id"`
	PrincipalType string      `json:"principal_type"`
	PrincipalID   pgtype.UUID `json:"principal_id"`
	Role          string      `json:"role"`
	GrantedBy     pgtype.UUID `json:"granted_by"`
}

type SetStatementQueryHandleParams struct {
	ExecutionID pgtype.UUID `json:"execution_id"`
	Position    int32       `json:"position"`
	QueryHandle []byte      `json:"query_handle"`
}

type SetTrinoQueryProfileParams struct {
	TrinoQueryID    string `json:"trino_query_id"`
	RawTrinoPayload []byte `json:"raw_trino_payload"`
}

type SetWorkspaceDefaultTargetParams struct {
	ID               pgtype.UUID `json:"id"`
	DefaultBindingID pgtype.UUID `json:"default_binding_id"`
	DefaultSchema    *string     `json:"default_schema"`
}

type SharesWorkspaceParams struct {
	CallerID pgtype.UUID `json:"caller_id"`
	UserID   pgtype.UUID `json:"user_id"`
}

type SoftDeleteTrinoCatalogRegistryParams struct {
	ClusterID   pgtype.UUID `json:"cluster_id"`
	CatalogName string      `json:"catalog_name"`
}

type SpaceSlugExistsParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Slug        string      `json:"slug"`
}

type StartMetadataSyncRunParams struct {
	CatalogID  pgtype.UUID `json:"catalog_id"`
	SourcePath string      `json:"source_path"`
}

type StartMetadataSyncRunRow struct {
	ID        pgtype.UUID        `json:"id"`
	StartedAt pgtype.Timestamptz `json:"started_at"`
}

type StartQueryStatementParams struct {
	ExecutionID pgtype.UUID `json:"execution_id"`
	Position    int32       `json:"position"`
}

type SuspendUserParams struct {
	ID              pgtype.UUID `json:"id"`
	SuspendedReason *string     `json:"suspended_reason"`
}

type TombstoneMissingNamespacesParams struct {
	CatalogID     pgtype.UUID        `json:"catalog_id"`
	PassStartedAt pgtype.Timestamptz `json:"pass_started_at"`
}

type TombstoneMissingTablesParams struct {
	CatalogID     pgtype.UUID        `json:"catalog_id"`
	PassStartedAt pgtype.Timestamptz `json:"pass_started_at"`
}

type TouchMetadataTableParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace []string    `json:"namespace"`
	Name      string      `json:"name"`
}

type TransitionBindingParams struct {
	ToState           string      `json:"to_state"`
	ErrorCode         *string     `json:"error_code"`
	ErrorMessage      *string     `json:"error_message"`
	EngineCatalogName *string     `json:"engine_catalog_name"`
	DesiredVersion    *string     `json:"desired_version"`
	ID                pgtype.UUID `json:"id"`
	FromStates        []string    `json:"from_states"`
}

type UpdateAgentParams struct {
	ID          pgtype.UUID `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
}

type UpdateAgentPolicyParams struct {
	ID              pgtype.UUID `json:"id"`
	Policy          []byte      `json:"policy"`
	PolicyUpdatedBy pgtype.UUID `json:"policy_updated_by"`
}

type UpdateAuthProviderParams struct {
	ID        pgtype.UUID `json:"id"`
	Name      string      `json:"name"`
	ConfigEnc string      `json:"config_enc"`
	Enabled   bool        `json:"enabled"`
}

type UpdateBindingParams struct {
	ID                pgtype.UUID `json:"id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	Connector         *string     `json:"connector"`
	ExtraProps        []byte      `json:"extra_props"`
	Enabled           bool        `json:"enabled"`
	LifecycleState    string      `json:"lifecycle_state"`
	DesiredVersion    *string     `json:"desired_version"`
	FromStates        []string    `json:"from_states"`
}

type UpdateCatalogConnectionParams struct {
	Name                *string     `json:"name"`
	Url                 *string     `json:"url"`
	Config              []byte      `json:"config"`
	CredentialsEnc      *string     `json:"credentials_enc"`
	CredentialsReadOnly *bool       `json:"credentials_read_only"`
	ID                  pgtype.UUID `json:"id"`
}

type UpdateCatalogMetaParams struct {
	ID          pgtype.UUID `json:"id"`
	Name        string      `json:"name"`
	Description *string     `json:"description"`
}

type UpdateCatalogParams struct {
	ID               pgtype.UUID `json:"id"`
	Name             string      `json:"name"`
	Description      *string     `json:"description"`
	Warehouse        *string     `json:"warehouse"`
	Connector        *string     `json:"connector"`
	ExtraProps       []byte      `json:"extra_props"`
	TrinoCatalogName *string     `json:"trino_catalog_name"`
}

type UpdateEngineParams struct {
	Enabled            *bool       `json:"enabled"`
	IdentityMode       *string     `json:"identity_mode"`
	CostRoutingEnabled *bool       `json:"cost_routing_enabled"`
	ClearCostThreshold *bool       `json:"clear_cost_threshold"`
	CostThresholdBytes *int64      `json:"cost_threshold_bytes"`
	ClearExternalUrl   *bool       `json:"clear_external_url"`
	ExternalUrl        *string     `json:"external_url"`
	ID                 pgtype.UUID `json:"id"`
}

type UpdateMaintenanceRunParams struct {
	Status       string      `json:"status"`
	ErrorCode    *string     `json:"error_code"`
	ErrorMessage *string     `json:"error_message"`
	ID           pgtype.UUID `json:"id"`
}

type UpdateMigrationItemParams struct {
	ID           pgtype.UUID `json:"id"`
	Status       string      `json:"status"`
	ErrorMessage *string     `json:"error_message"`
}

type UpdateMigrationProgressParams struct {
	ID             pgtype.UUID `json:"id"`
	MigratedTables int32       `json:"migrated_tables"`
	FailedTables   int32       `json:"failed_tables"`
}

type UpdateMigrationStatusParams struct {
	ID                 pgtype.UUID `json:"id"`
	Status             string      `json:"status"`
	TemporalWorkflowID *string     `json:"temporal_workflow_id"`
}

type UpdateQueryExecutionParams struct {
	ID            pgtype.UUID        `json:"id"`
	Status        string             `json:"status"`
	EngineQueryID *string            `json:"engine_query_id"`
	RowsReturned  *int64             `json:"rows_returned"`
	DurationMs    *int64             `json:"duration_ms"`
	FinishedAt    pgtype.Timestamptz `json:"finished_at"`
}

type UpdateQueryFolderParams struct {
	Name        string      `json:"name"`
	Slug        *string     `json:"slug"`
	ParentID    pgtype.UUID `json:"parent_id"`
	SpaceID     pgtype.UUID `json:"space_id"`
	ID          pgtype.UUID `json:"id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type UpdateQueryStatementParams struct {
	ID            pgtype.UUID        `json:"id"`
	Status        string             `json:"status"`
	EngineQueryID *string            `json:"engine_query_id"`
	RowsReturned  *int64             `json:"rows_returned"`
	DurationMs    *int64             `json:"duration_ms"`
	ErrorMessage  *string            `json:"error_message"`
	FinishedAt    pgtype.Timestamptz `json:"finished_at"`
	ResultKey     *string            `json:"result_key"`
	ResultBytes   *int64             `json:"result_bytes"`
	Truncated     bool               `json:"truncated"`
	ResultSets    []byte             `json:"result_sets"`
	ErrorCode     *string            `json:"error_code"`
}

type UpdateRetrieverAfterRefreshParams struct {
	ID        pgtype.UUID `json:"id"`
	Status    string      `json:"status"`
	ErrorCode *string     `json:"error_code"`
}

type UpdateRetrieverParams struct {
	ID            pgtype.UUID        `json:"id"`
	WorkspaceID   pgtype.UUID        `json:"workspace_id"`
	Name          string             `json:"name"`
	DefinitionSql string             `json:"definition_sql"`
	DestCatalog   string             `json:"dest_catalog"`
	DestNamespace string             `json:"dest_namespace"`
	DestTable     string             `json:"dest_table"`
	DestBindingID pgtype.UUID        `json:"dest_binding_id"`
	CronSchedule  string             `json:"cron_schedule"`
	RefreshType   string             `json:"refresh_type"`
	Status        string             `json:"status"`
	ErrorCode     *string            `json:"error_code"`
	NextRefreshAt pgtype.Timestamptz `json:"next_refresh_at"`
}

type UpdateRetrieverRefreshHistoryParams struct {
	ID            pgtype.UUID `json:"id"`
	Status        string      `json:"status"`
	RowsProcessed *int64      `json:"rows_processed"`
	DurationMs    *int64      `json:"duration_ms"`
	ErrorMessage  *string     `json:"error_message"`
	ErrorCode     *string     `json:"error_code"`
}

type UpdateSavedQueryParams struct {
	Name            string      `json:"name"`
	Sql             string      `json:"sql"`
	FolderID        pgtype.UUID `json:"folder_id"`
	SpaceID         pgtype.UUID `json:"space_id"`
	Slug            string      `json:"slug"`
	TargetBindingID pgtype.UUID `json:"target_binding_id"`
	TargetSchema    *string     `json:"target_schema"`
	UpdatedBy       pgtype.UUID `json:"updated_by"`
	ID              pgtype.UUID `json:"id"`
	WorkspaceID     pgtype.UUID `json:"workspace_id"`
}

type UpdateSemanticMetricParams struct {
	SpaceID       pgtype.UUID `json:"space_id"`
	Slug          string      `json:"slug"`
	Label         string      `json:"label"`
	Description   string      `json:"description"`
	Certified     bool        `json:"certified"`
	CatalogID     pgtype.UUID `json:"catalog_id"`
	Namespace     []string    `json:"namespace"`
	TableName     string      `json:"table_name"`
	BindingID     pgtype.UUID `json:"binding_id"`
	Definition    []byte      `json:"definition"`
	FormatVersion int32       `json:"format_version"`
	OwnerUserID   pgtype.UUID `json:"owner_user_id"`
	UpdatedBy     pgtype.UUID `json:"updated_by"`
	WorkspaceID   pgtype.UUID `json:"workspace_id"`
	ID            pgtype.UUID `json:"id"`
}

type UpdateSemanticModelParams struct {
	SpaceID       pgtype.UUID `json:"space_id"`
	Slug          string      `json:"slug"`
	Label         string      `json:"label"`
	Description   string      `json:"description"`
	Certified     bool        `json:"certified"`
	CatalogID     pgtype.UUID `json:"catalog_id"`
	BindingID     pgtype.UUID `json:"binding_id"`
	Definition    []byte      `json:"definition"`
	FormatVersion int32       `json:"format_version"`
	OwnerUserID   pgtype.UUID `json:"owner_user_id"`
	UpdatedBy     pgtype.UUID `json:"updated_by"`
	WorkspaceID   pgtype.UUID `json:"workspace_id"`
	ID            pgtype.UUID `json:"id"`
}

type UpdateServiceAccountParams struct {
	Name           string      `json:"name"`
	Description    *string     `json:"description"`
	AuthProvider   string      `json:"auth_provider"`
	AuthProviderID pgtype.UUID `json:"auth_provider_id"`
	ExternalID     *string     `json:"external_id"`
	ID             pgtype.UUID `json:"id"`
}

type UpdateSpaceParams struct {
	Name        string      `json:"name"`
	Description *string     `json:"description"`
	Visibility  string      `json:"visibility"`
	DefaultRole string      `json:"default_role"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	ID          pgtype.UUID `json:"id"`
}

type UpdateTableMaintenancePolicyParams struct {
	Mode                        string      `json:"mode"`
	WriterCommitIntervalSeconds int32       `json:"writer_commit_interval_seconds"`
	Optimize                    []byte      `json:"optimize"`
	OptimizeManifests           []byte      `json:"optimize_manifests"`
	ExpireSnapshots             []byte      `json:"expire_snapshots"`
	RemoveOrphanFiles           []byte      `json:"remove_orphan_files"`
	Actor                       pgtype.UUID `json:"actor"`
	CatalogID                   pgtype.UUID `json:"catalog_id"`
	Namespace                   string      `json:"namespace"`
	TableName                   string      `json:"table_name"`
	Revision                    int64       `json:"revision"`
}

type UpdateTeamMemberRoleParams struct {
	TeamID pgtype.UUID `json:"team_id"`
	UserID pgtype.UUID `json:"user_id"`
	Role   string      `json:"role"`
}

type UpdateTeamParams struct {
	ID          pgtype.UUID `json:"id"`
	Name        string      `json:"name"`
	Description *string     `json:"description"`
}

type UpdateUserIDPTokenParams struct {
	AccessTokenEnc  string             `json:"access_token_enc"`
	RefreshTokenEnc *string            `json:"refresh_token_enc"`
	ExpiresAt       pgtype.Timestamptz `json:"expires_at"`
	UserID          pgtype.UUID        `json:"user_id"`
}

type UpdateUserParams struct {
	ID     pgtype.UUID `json:"id"`
	Name   string      `json:"name"`
	Email  string      `json:"email"`
	Locale *string     `json:"locale"`
}

type UpdateUserPasswordParams struct {
	ID           pgtype.UUID `json:"id"`
	PasswordHash *string     `json:"password_hash"`
}

type UpdateUserPersonalWorkspaceParams struct {
	ID                  pgtype.UUID `json:"id"`
	PersonalWorkspaceID pgtype.UUID `json:"personal_workspace_id"`
}

type UpdateUserRoleParams struct {
	ID   pgtype.UUID `json:"id"`
	Role string      `json:"role"`
}

type UpdateWorkloadRuleParams struct {
	ID       pgtype.UUID `json:"id"`
	Position int32       `json:"position"`
	Enabled  bool        `json:"enabled"`
	Match    []byte      `json:"match"`
	Class    string      `json:"class"`
	MaxClass *string     `json:"max_class"`
	Weight   int32       `json:"weight"`
}

type UpdateWorkspaceParams struct {
	ID          pgtype.UUID `json:"id"`
	Name        string      `json:"name"`
	Description *string     `json:"description"`
}

type UpsertAdoptedBindingParams struct {
	CatalogID         pgtype.UUID `json:"catalog_id"`
	EngineID          pgtype.UUID `json:"engine_id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	Connector         *string     `json:"connector"`
}

type UpsertEngineCatalogObservationParams struct {
	EngineID    pgtype.UUID `json:"engine_id"`
	CatalogName string      `json:"catalog_name"`
	Mode        string      `json:"mode"`
	Connector   *string     `json:"connector"`
}

type UpsertEngineQueueCapacityParams struct {
	EngineID         pgtype.UUID `json:"engine_id"`
	Class            string      `json:"class"`
	MaxRunning       int32       `json:"max_running"`
	MaxQueued        int32       `json:"max_queued"`
	QueueTimeoutMs   int64       `json:"queue_timeout_ms"`
	RuntimeTimeoutMs *int64      `json:"runtime_timeout_ms"`
}

type UpsertManagedBindingParams struct {
	CatalogID         pgtype.UUID `json:"catalog_id"`
	EngineID          pgtype.UUID `json:"engine_id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
	Connector         *string     `json:"connector"`
	ExtraProps        []byte      `json:"extra_props"`
	Enabled           bool        `json:"enabled"`
	LifecycleState    string      `json:"lifecycle_state"`
	DesiredVersion    *string     `json:"desired_version"`
}

type UpsertMetadataNamespaceParams struct {
	CatalogID pgtype.UUID `json:"catalog_id"`
	Namespace []string    `json:"namespace"`
}

type UpsertMetadataTableParams struct {
	CatalogID   pgtype.UUID `json:"catalog_id"`
	Namespace   []string    `json:"namespace"`
	Name        string      `json:"name"`
	Kind        string      `json:"kind"`
	Fingerprint string      `json:"fingerprint"`
	Properties  []byte      `json:"properties"`
	Comment     *string     `json:"comment"`
}

type UpsertMetadataTableRow struct {
	ID      pgtype.UUID `json:"id"`
	Changed bool        `json:"changed"`
}

type UpsertNativeBindingParams struct {
	CatalogID         pgtype.UUID `json:"catalog_id"`
	EngineID          pgtype.UUID `json:"engine_id"`
	EngineCatalogName string      `json:"engine_catalog_name"`
}

type UpsertNativeCatalogParams struct {
	ConnectionID pgtype.UUID `json:"connection_id"`
	Name         string      `json:"name"`
}

type UpsertSemanticDocParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
	Namespace   []string    `json:"namespace"`
	TableName   string      `json:"table_name"`
	ColumnName  *string     `json:"column_name"`
	Description string      `json:"description"`
	Tags        []string    `json:"tags"`
	OwnerUserID pgtype.UUID `json:"owner_user_id"`
	UpdatedBy   pgtype.UUID `json:"updated_by"`
	Certified   bool        `json:"certified"`
}

type UpsertTableHealthSnapshotParams struct {
	CatalogID              pgtype.UUID        `json:"catalog_id"`
	CatalogConnectionID    pgtype.UUID        `json:"catalog_connection_id"`
	Catalog                string             `json:"catalog"`
	Namespace              string             `json:"namespace"`
	TableName              string             `json:"table_name"`
	SnapshotCount          int32              `json:"snapshot_count"`
	FileCount              *int32             `json:"file_count"`
	TotalSizeBytes         *int64             `json:"total_size_bytes"`
	OldestSnapshotAgeHours *int32             `json:"oldest_snapshot_age_hours"`
	LastMaintenanceAt      pgtype.Timestamptz `json:"last_maintenance_at"`
	HealthScore            int32              `json:"health_score"`
	NeedsCompaction        bool               `json:"needs_compaction"`
	NeedsSnapshotExpiry    bool               `json:"needs_snapshot_expiry"`
	DeleteFileCount        *int64             `json:"delete_file_count"`
	EqualityDeleteCount    *int64             `json:"equality_delete_count"`
	PositionDeleteCount    *int64             `json:"position_delete_count"`
	SmallFileCount         *int64             `json:"small_file_count"`
	CommitsLastHour        *int32             `json:"commits_last_hour"`
	CommitsLastDay         *int32             `json:"commits_last_day"`
	AvgFileSizeBytes       *int64             `json:"avg_file_size_bytes"`
	ManifestCount          *int32             `json:"manifest_count"`
	FormatVersion          *int32             `json:"format_version"`
	TableProperties        []byte             `json:"table_properties"`
	DueOperations          []string           `json:"due_operations"`
	PolicyRevision         *int64             `json:"policy_revision"`
}

type UpsertTeamMemberParams struct {
	TeamID pgtype.UUID `json:"team_id"`
	UserID pgtype.UUID `json:"user_id"`
	Role   string      `json:"role"`
}

type UpsertTrinoCatalogRegistryParams struct {
	ClusterID      pgtype.UUID `json:"cluster_id"`
	CatalogName    string      `json:"catalog_name"`
	ConnectorName  string      `json:"connector_name"`
	Column4        []byte      `json:"column_4"`
	CatalogVersion *string     `json:"catalog_version"`
}

type UpsertTrinoQuerySnapshotParams struct {
	TrinoQueryID       string             `json:"trino_query_id"`
	EngineID           pgtype.UUID        `json:"engine_id"`
	ExecutionID        pgtype.UUID        `json:"execution_id"`
	Origin             string             `json:"origin"`
	User               string             `json:"user"`
	State              string             `json:"state"`
	Sql                string             `json:"sql"`
	Catalog            *string            `json:"catalog"`
	Schema             *string            `json:"schema"`
	CreatedAt          pgtype.Timestamptz `json:"created_at"`
	ElapsedMs          *int32             `json:"elapsed_ms"`
	CpuMs              *int32             `json:"cpu_ms"`
	PhysicalInputBytes *int64             `json:"physical_input_bytes"`
	OutputRows         *int64             `json:"output_rows"`
	ErrorMessage       *string            `json:"error_message"`
	RawTrinoPayload    []byte             `json:"raw_trino_payload"`
}

type UpsertWorkspaceCatalogGrantParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	CatalogID   pgtype.UUID `json:"catalog_id"`
	GrantedBy   pgtype.UUID `json:"granted_by"`
}

type UpsertWorkspaceMemberParams struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	UserID      pgtype.UUID `json:"user_id"`
	Role        string      `json:"role"`
	GrantedBy   pgtype.UUID `json:"granted_by"`
}
