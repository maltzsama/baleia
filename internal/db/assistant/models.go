// The structs below are the assistant row shapes. They are hand-maintained now
// that the store runs through GORM (ADR 0020).
//
//nolint:revive // DTO fields keep the SQL column spelling and the published JSON contract.
package assistant

import (
	"net/netip"

	"github.com/galileostudio/baleia/internal/db"
)

type Agent struct {
	ID              db.UUID        `json:"id"`
	OwnerID         db.UUID        `json:"owner_id"`
	Kind            string         `json:"kind"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Status          string         `json:"status"`
	CreatedAt       db.Timestamptz `json:"created_at"`
	UpdatedAt       db.Timestamptz `json:"updated_at"`
	Policy          []byte         `json:"policy"`
	PolicyUpdatedAt db.Timestamptz `json:"policy_updated_at"`
	PolicyUpdatedBy db.UUID        `json:"policy_updated_by"`
}

type AgentToken struct {
	ID         db.UUID        `json:"id"`
	AgentID    db.UUID        `json:"agent_id"`
	Name       string         `json:"name"`
	TokenHash  string         `json:"token_hash"`
	Prefix     string         `json:"prefix"`
	ExpiresAt  db.Timestamptz `json:"expires_at"`
	LastUsedAt db.Timestamptz `json:"last_used_at"`
	CreatedBy  db.UUID        `json:"created_by"`
	CreatedAt  db.Timestamptz `json:"created_at"`
	RevokedAt  db.Timestamptz `json:"revoked_at"`
}

type AgentWorkspace struct {
	AgentID     db.UUID `json:"agent_id"`
	WorkspaceID db.UUID `json:"workspace_id"`
}

type ApiKey struct {
	ID         db.UUID        `json:"id"`
	UserID     db.UUID        `json:"user_id"`
	Name       string         `json:"name"`
	KeyHash    string         `json:"key_hash"`
	ExpiresAt  db.Timestamptz `json:"expires_at"`
	CreatedAt  db.Timestamptz `json:"created_at"`
	Prefix     string         `json:"prefix"`
	Scopes     []string       `json:"scopes"`
	LastUsedAt db.Timestamptz `json:"last_used_at"`
}

type AssistantConversation struct {
	ID              db.UUID        `json:"id"`
	UserID          db.UUID        `json:"user_id"`
	WorkspaceID     db.UUID        `json:"workspace_id"`
	AgentID         db.UUID        `json:"agent_id"`
	Title           string         `json:"title"`
	State           string         `json:"state"`
	TurnOwner       *string        `json:"turn_owner"`
	TurnHeartbeatAt db.Timestamptz `json:"turn_heartbeat_at"`
	LastSeq         int32          `json:"last_seq"`
	CreatedAt       db.Timestamptz `json:"created_at"`
	UpdatedAt       db.Timestamptz `json:"updated_at"`
}

type AssistantMessage struct {
	ConversationID db.UUID        `json:"conversation_id"`
	Seq            int32          `json:"seq"`
	Role           string         `json:"role"`
	Content        []byte         `json:"content"`
	InputTokens    *int32         `json:"input_tokens"`
	OutputTokens   *int32         `json:"output_tokens"`
	CreatedAt      db.Timestamptz `json:"created_at"`
}

type AssistantSetting struct {
	ID                      bool           `json:"id"`
	Enabled                 bool           `json:"enabled"`
	Provider                *string        `json:"provider"`
	BaseUrl                 *string        `json:"base_url"`
	Model                   *string        `json:"model"`
	ApiKeyEnc               *string        `json:"api_key_enc"`
	DataSharing             string         `json:"data_sharing"`
	MaxTurnSeconds          int32          `json:"max_turn_seconds"`
	MaxToolCallsPerTurn     int32          `json:"max_tool_calls_per_turn"`
	MaxOutputTokensPerTurn  int32          `json:"max_output_tokens_per_turn"`
	DailyTokenBudgetPerUser int64          `json:"daily_token_budget_per_user"`
	UpdatedBy               db.UUID        `json:"updated_by"`
	UpdatedAt               db.Timestamptz `json:"updated_at"`
}

type AuditEvent struct {
	ID           int64          `json:"id"`
	OccurredAt   db.Timestamptz `json:"occurred_at"`
	ActorKind    string         `json:"actor_kind"`
	ActorID      *string        `json:"actor_id"`
	AuthMethod   *string        `json:"auth_method"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   *string        `json:"resource_id"`
	WorkspaceID  db.UUID        `json:"workspace_id"`
	Outcome      string         `json:"outcome"`
	Reason       *string        `json:"reason"`
	RequestID    *string        `json:"request_id"`
	TraceID      *string        `json:"trace_id"`
	ClientIp     *netip.Addr    `json:"client_ip"`
	UserAgent    *string        `json:"user_agent"`
	Changes      []byte         `json:"changes"`
	OnBehalfOf   db.UUID        `json:"on_behalf_of"`
}

type AuditExportCursor struct {
	Sink       string         `json:"sink"`
	OccurredAt db.Timestamptz `json:"occurred_at"`
	ID         int64          `json:"id"`
	UpdatedAt  db.Timestamptz `json:"updated_at"`
}

type AuthProvider struct {
	ID        db.UUID        `json:"id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	ConfigEnc string         `json:"config_enc"`
	Enabled   bool           `json:"enabled"`
	CreatedAt db.Timestamptz `json:"created_at"`
	UpdatedAt db.Timestamptz `json:"updated_at"`
}

type BindingStatusV struct {
	BindingID      db.UUID `json:"binding_id"`
	LifecycleState string  `json:"lifecycle_state"`
	SyncStatus     string  `json:"sync_status"`
	EngineError    string  `json:"engine_error"`
}

type Catalog struct {
	ID               db.UUID        `json:"id"`
	ConnectionID     db.UUID        `json:"connection_id"`
	Name             string         `json:"name"`
	Description      *string        `json:"description"`
	TrinoCatalogName *string        `json:"trino_catalog_name"`
	ClusterID        db.UUID        `json:"cluster_id"`
	Warehouse        *string        `json:"warehouse"`
	Connector        *string        `json:"connector"`
	ExtraProps       []byte         `json:"extra_props"`
	Enabled          bool           `json:"enabled"`
	CreatedAt        db.Timestamptz `json:"created_at"`
	UpdatedAt        db.Timestamptz `json:"updated_at"`
	Origin           string         `json:"origin"`
	SourceRef        *string        `json:"source_ref"`
	Discoverable     bool           `json:"discoverable"`
}

type CatalogBinding struct {
	ID                db.UUID        `json:"id"`
	CatalogID         db.UUID        `json:"catalog_id"`
	EngineID          db.UUID        `json:"engine_id"`
	EngineCatalogName string         `json:"engine_catalog_name"`
	Connector         *string        `json:"connector"`
	ExtraProps        []byte         `json:"extra_props"`
	Mode              string         `json:"mode"`
	Enabled           bool           `json:"enabled"`
	CreatedAt         db.Timestamptz `json:"created_at"`
	UpdatedAt         db.Timestamptz `json:"updated_at"`
	LifecycleState    string         `json:"lifecycle_state"`
	DesiredVersion    *string        `json:"desired_version"`
	LastErrorCode     *string        `json:"last_error_code"`
	LastErrorMessage  *string        `json:"last_error_message"`
	StateChangedAt    db.Timestamptz `json:"state_changed_at"`
}

type CatalogConnection struct {
	ID                  db.UUID        `json:"id"`
	Name                string         `json:"name"`
	Type                string         `json:"type"`
	Url                 string         `json:"url"`
	CredentialsEnc      *string        `json:"credentials_enc"`
	CreatedAt           db.Timestamptz `json:"created_at"`
	UpdatedAt           db.Timestamptz `json:"updated_at"`
	Host                *string        `json:"host"`
	Port                *string        `json:"port"`
	CreatedBy           db.UUID        `json:"created_by"`
	Config              []byte         `json:"config"`
	CredentialsReadOnly bool           `json:"credentials_read_only"`
}

type CatalogGrantRequest struct {
	WorkspaceID     db.UUID        `json:"workspace_id"`
	CatalogID       db.UUID        `json:"catalog_id"`
	InitiatedBySide string         `json:"initiated_by_side"`
	RequestedBy     db.UUID        `json:"requested_by"`
	RequestedAt     db.Timestamptz `json:"requested_at"`
}

type CatalogReconcileRun struct {
	ID         db.UUID        `json:"id"`
	EngineID   db.UUID        `json:"engine_id"`
	StartedAt  db.Timestamptz `json:"started_at"`
	FinishedAt db.Timestamptz `json:"finished_at"`
	DryRun     bool           `json:"dry_run"`
	Actions    []byte         `json:"actions"`
	Adopted    []string       `json:"adopted"`
	Error      *string        `json:"error"`
}

type ClusterSnapshot struct {
	ID               db.UUID        `json:"id"`
	CapturedAt       db.Timestamptz `json:"captured_at"`
	ActiveWorkers    int32          `json:"active_workers"`
	RunningQueries   int32          `json:"running_queries"`
	QueuedQueries    int32          `json:"queued_queries"`
	BlockedQueries   int32          `json:"blocked_queries"`
	TotalMemoryBytes *int64         `json:"total_memory_bytes"`
	FreeMemoryBytes  *int64         `json:"free_memory_bytes"`
	RawPayload       []byte         `json:"raw_payload"`
	EngineID         db.UUID        `json:"engine_id"`
}

type ContentVersion struct {
	ID           int64          `json:"id"`
	WorkspaceID  db.UUID        `json:"workspace_id"`
	SpaceID      db.UUID        `json:"space_id"`
	EntityType   string         `json:"entity_type"`
	EntityID     db.UUID        `json:"entity_id"`
	Version      int32          `json:"version"`
	ChangeKind   string         `json:"change_kind"`
	Snapshot     []byte         `json:"snapshot"`
	AuthorID     db.UUID        `json:"author_id"`
	RestoredFrom *int32         `json:"restored_from"`
	CreatedAt    db.Timestamptz `json:"created_at"`
}

type DirectoryReconcileRun struct {
	ID         db.UUID        `json:"id"`
	ProviderID db.UUID        `json:"provider_id"`
	StartedAt  db.Timestamptz `json:"started_at"`
	FinishedAt db.Timestamptz `json:"finished_at"`
	Outcome    *string        `json:"outcome"`
	Seen       int32          `json:"seen"`
	Suspended  int32          `json:"suspended"`
	Reinstated int32          `json:"reinstated"`
	Error      *string        `json:"error"`
	Actions    []byte         `json:"actions"`
}

type Engine struct {
	ID                         db.UUID        `json:"id"`
	Name                       string         `json:"name"`
	Kind                       string         `json:"kind"`
	Class                      string         `json:"class"`
	ConnectionID               db.UUID        `json:"connection_id"`
	IdentityMode               string         `json:"identity_mode"`
	Enabled                    bool           `json:"enabled"`
	CreatedBy                  db.UUID        `json:"created_by"`
	CreatedAt                  db.Timestamptz `json:"created_at"`
	UpdatedAt                  db.Timestamptz `json:"updated_at"`
	MaxRunningTotal            *int32         `json:"max_running_total"`
	CostRoutingEnabled         bool           `json:"cost_routing_enabled"`
	CostThresholdBytes         *int64         `json:"cost_threshold_bytes"`
	ExternalUrl                *string        `json:"external_url"`
	TrinoResourceGroupsEnabled bool           `json:"trino_resource_groups_enabled"`
}

type EngineCatalogObservation struct {
	EngineID    db.UUID        `json:"engine_id"`
	CatalogName string         `json:"catalog_name"`
	Present     bool           `json:"present"`
	Mode        string         `json:"mode"`
	Connector   *string        `json:"connector"`
	FirstSeenAt db.Timestamptz `json:"first_seen_at"`
	ObservedAt  db.Timestamptz `json:"observed_at"`
}

type EngineQueueCapacity struct {
	EngineID         db.UUID        `json:"engine_id"`
	Class            string         `json:"class"`
	MaxRunning       int32          `json:"max_running"`
	MaxQueued        int32          `json:"max_queued"`
	QueueTimeoutMs   int64          `json:"queue_timeout_ms"`
	CreatedAt        db.Timestamptz `json:"created_at"`
	UpdatedAt        db.Timestamptz `json:"updated_at"`
	RuntimeTimeoutMs *int64         `json:"runtime_timeout_ms"`
}

type MaintenanceRun struct {
	ID             db.UUID        `json:"id"`
	CatalogID      db.UUID        `json:"catalog_id"`
	BindingID      db.UUID        `json:"binding_id"`
	Namespace      string         `json:"namespace"`
	TableName      string         `json:"table_name"`
	Operations     []string       `json:"operations"`
	Retention      *string        `json:"retention"`
	Status         string         `json:"status"`
	ErrorCode      *string        `json:"error_code"`
	ErrorMessage   *string        `json:"error_message"`
	StartedBy      db.UUID        `json:"started_by"`
	StartedAt      db.Timestamptz `json:"started_at"`
	FinishedAt     db.Timestamptz `json:"finished_at"`
	WorkflowID     string         `json:"workflow_id"`
	Trigger        string         `json:"trigger"`
	Reason         []byte         `json:"reason"`
	Parameters     []byte         `json:"parameters"`
	Attempts       int32          `json:"attempts"`
	PolicyRevision *int64         `json:"policy_revision"`
}

type MetadataColumn struct {
	TableID    db.UUID `json:"table_id"`
	Ordinal    int32   `json:"ordinal"`
	Name       string  `json:"name"`
	NativeType string  `json:"native_type"`
	TypeSystem string  `json:"type_system"`
	Nullable   *bool   `json:"nullable"`
	Comment    *string `json:"comment"`
}

type MetadataForeignKey struct {
	CatalogID    db.UUID        `json:"catalog_id"`
	TableID      db.UUID        `json:"table_id"`
	ColumnName   string         `json:"column_name"`
	RefNamespace []string       `json:"ref_namespace"`
	RefTable     string         `json:"ref_table"`
	RefColumn    string         `json:"ref_column"`
	LastSeenAt   db.Timestamptz `json:"last_seen_at"`
}

type MetadataNamespace struct {
	CatalogID  db.UUID        `json:"catalog_id"`
	Namespace  []string       `json:"namespace"`
	LastSeenAt db.Timestamptz `json:"last_seen_at"`
	RemovedAt  db.Timestamptz `json:"removed_at"`
}

type MetadataSyncRun struct {
	ID            db.UUID        `json:"id"`
	CatalogID     db.UUID        `json:"catalog_id"`
	StartedAt     db.Timestamptz `json:"started_at"`
	FinishedAt    db.Timestamptz `json:"finished_at"`
	Status        string         `json:"status"`
	TablesSeen    *int32         `json:"tables_seen"`
	TablesChanged *int32         `json:"tables_changed"`
	TablesRemoved *int32         `json:"tables_removed"`
	ErrorCode     *string        `json:"error_code"`
	ErrorMessage  *string        `json:"error_message"`
	SourcePath    string         `json:"source_path"`
}

type MetadataTable struct {
	ID            db.UUID        `json:"id"`
	CatalogID     db.UUID        `json:"catalog_id"`
	Namespace     []string       `json:"namespace"`
	Name          string         `json:"name"`
	Kind          string         `json:"kind"`
	Fingerprint   string         `json:"fingerprint"`
	Properties    []byte         `json:"properties"`
	Comment       *string        `json:"comment"`
	LastSeenAt    db.Timestamptz `json:"last_seen_at"`
	LastChangedAt db.Timestamptz `json:"last_changed_at"`
	RemovedAt     db.Timestamptz `json:"removed_at"`
}

type Migration struct {
	ID                        db.UUID        `json:"id"`
	CreatedBy                 db.UUID        `json:"created_by"`
	Name                      string         `json:"name"`
	SourceType                string         `json:"source_type"`
	SourceUrl                 string         `json:"source_url"`
	SourceCredentialsEnc      *string        `json:"source_credentials_enc"`
	TargetCatalogConnectionID db.UUID        `json:"target_catalog_connection_id"`
	Status                    string         `json:"status"`
	TotalTables               int32          `json:"total_tables"`
	MigratedTables            int32          `json:"migrated_tables"`
	FailedTables              int32          `json:"failed_tables"`
	TemporalWorkflowID        *string        `json:"temporal_workflow_id"`
	StartedAt                 db.Timestamptz `json:"started_at"`
	FinishedAt                db.Timestamptz `json:"finished_at"`
	CreatedAt                 db.Timestamptz `json:"created_at"`
}

type MigrationItem struct {
	ID              db.UUID        `json:"id"`
	MigrationID     db.UUID        `json:"migration_id"`
	SourceNamespace string         `json:"source_namespace"`
	SourceTable     string         `json:"source_table"`
	TargetNamespace string         `json:"target_namespace"`
	TargetTable     string         `json:"target_table"`
	Status          string         `json:"status"`
	ErrorMessage    *string        `json:"error_message"`
	StartedAt       db.Timestamptz `json:"started_at"`
	FinishedAt      db.Timestamptz `json:"finished_at"`
}

type OidcLoginAttempt struct {
	State        string         `json:"state"`
	ProviderID   db.UUID        `json:"provider_id"`
	Nonce        *string        `json:"nonce"`
	CodeVerifier string         `json:"code_verifier"`
	Redirect     *string        `json:"redirect"`
	ExpiresAt    db.Timestamptz `json:"expires_at"`
	CreatedAt    db.Timestamptz `json:"created_at"`
}

type PasswordResetToken struct {
	TokenHash string         `json:"token_hash"`
	UserID    db.UUID        `json:"user_id"`
	ExpiresAt db.Timestamptz `json:"expires_at"`
	UsedAt    db.Timestamptz `json:"used_at"`
	CreatedAt db.Timestamptz `json:"created_at"`
}

type QueryExecution struct {
	ID              db.UUID        `json:"id"`
	WorkspaceID     db.UUID        `json:"workspace_id"`
	UserID          db.UUID        `json:"user_id"`
	SavedQueryID    db.UUID        `json:"saved_query_id"`
	Status          string         `json:"status"`
	Sql             string         `json:"sql"`
	EngineQueryID   *string        `json:"engine_query_id"`
	RowsReturned    *int64         `json:"rows_returned"`
	DurationMs      *int64         `json:"duration_ms"`
	StartedAt       db.Timestamptz `json:"started_at"`
	FinishedAt      db.Timestamptz `json:"finished_at"`
	TrinoCatalog    *string        `json:"trino_catalog"`
	ResultKey       *string        `json:"result_key"`
	RequestID       *string        `json:"request_id"`
	EngineID        db.UUID        `json:"engine_id"`
	TargetBindingID db.UUID        `json:"target_binding_id"`
	TargetCatalogID db.UUID        `json:"target_catalog_id"`
	TargetSchema    *string        `json:"target_schema"`
	TargetSnapshot  []byte         `json:"target_snapshot"`
	AgentID         db.UUID        `json:"agent_id"`
	QueueClass      *string        `json:"queue_class"`
	QueueReason     *string        `json:"queue_reason"`
	FairnessKey     *string        `json:"fairness_key"`
	QueuedAt        db.Timestamptz `json:"queued_at"`
	AdmittedAt      db.Timestamptz `json:"admitted_at"`
	QueueWaitMs     *int64         `json:"queue_wait_ms"`
	ErrorCode       *string        `json:"error_code"`
}

type QueryFolder struct {
	ID          db.UUID        `json:"id"`
	WorkspaceID db.UUID        `json:"workspace_id"`
	ParentID    db.UUID        `json:"parent_id"`
	Name        string         `json:"name"`
	CreatedAt   db.Timestamptz `json:"created_at"`
	SpaceID     db.UUID        `json:"space_id"`
	Slug        string         `json:"slug"`
}

type QueryStatement struct {
	ID            db.UUID        `json:"id"`
	ExecutionID   db.UUID        `json:"execution_id"`
	Position      int32          `json:"position"`
	Sql           string         `json:"sql"`
	Status        string         `json:"status"`
	EngineQueryID *string        `json:"engine_query_id"`
	RowsReturned  *int64         `json:"rows_returned"`
	DurationMs    *int64         `json:"duration_ms"`
	ErrorMessage  *string        `json:"error_message"`
	StartedAt     db.Timestamptz `json:"started_at"`
	FinishedAt    db.Timestamptz `json:"finished_at"`
	ResultKey     *string        `json:"result_key"`
	ResultBytes   *int64         `json:"result_bytes"`
	Truncated     bool           `json:"truncated"`
	QueryHandle   []byte         `json:"query_handle"`
	ResultSets    []byte         `json:"result_sets"`
	ErrorCode     *string        `json:"error_code"`
}

type RefreshToken struct {
	ID        db.UUID        `json:"id"`
	UserID    db.UUID        `json:"user_id"`
	TokenHash string         `json:"token_hash"`
	ExpiresAt db.Timestamptz `json:"expires_at"`
	Revoked   bool           `json:"revoked"`
	CreatedAt db.Timestamptz `json:"created_at"`
	FamilyID  db.UUID        `json:"family_id"`
}

type ResourceGroup struct {
	ResourceGroupID           int64   `json:"resource_group_id"`
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

type ResourceGroupsGlobalProperty struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
}

type Retriever struct {
	ID            db.UUID        `json:"id"`
	WorkspaceID   db.UUID        `json:"workspace_id"`
	CreatedBy     db.UUID        `json:"created_by"`
	Name          string         `json:"name"`
	DefinitionSql string         `json:"definition_sql"`
	DestCatalog   string         `json:"dest_catalog"`
	DestNamespace string         `json:"dest_namespace"`
	DestTable     string         `json:"dest_table"`
	CronSchedule  string         `json:"cron_schedule"`
	RefreshType   string         `json:"refresh_type"`
	Status        string         `json:"status"`
	LastRefreshAt db.Timestamptz `json:"last_refresh_at"`
	NextRefreshAt db.Timestamptz `json:"next_refresh_at"`
	CreatedAt     db.Timestamptz `json:"created_at"`
	UpdatedAt     db.Timestamptz `json:"updated_at"`
	DestBindingID db.UUID        `json:"dest_binding_id"`
	ErrorCode     *string        `json:"error_code"`
}

type RetrieverRefreshHistory struct {
	ID                 db.UUID        `json:"id"`
	RetrieverID        db.UUID        `json:"retriever_id"`
	TemporalWorkflowID string         `json:"temporal_workflow_id"`
	RefreshType        string         `json:"refresh_type"`
	Status             string         `json:"status"`
	RowsProcessed      *int64         `json:"rows_processed"`
	DurationMs         *int64         `json:"duration_ms"`
	ErrorMessage       *string        `json:"error_message"`
	StartedAt          db.Timestamptz `json:"started_at"`
	FinishedAt         db.Timestamptz `json:"finished_at"`
	ErrorCode          *string        `json:"error_code"`
}

type SavedQuery struct {
	ID              db.UUID        `json:"id"`
	WorkspaceID     db.UUID        `json:"workspace_id"`
	FolderID        db.UUID        `json:"folder_id"`
	CreatedBy       db.UUID        `json:"created_by"`
	Name            string         `json:"name"`
	Sql             string         `json:"sql"`
	CreatedAt       db.Timestamptz `json:"created_at"`
	UpdatedAt       db.Timestamptz `json:"updated_at"`
	SpaceID         db.UUID        `json:"space_id"`
	UpdatedBy       db.UUID        `json:"updated_by"`
	TargetBindingID db.UUID        `json:"target_binding_id"`
	TargetSchema    *string        `json:"target_schema"`
	Slug            string         `json:"slug"`
}

type Selector struct {
	ID                     int64   `json:"id"`
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

type SemanticDoc struct {
	ID          db.UUID        `json:"id"`
	WorkspaceID db.UUID        `json:"workspace_id"`
	CatalogID   db.UUID        `json:"catalog_id"`
	Namespace   []string       `json:"namespace"`
	TableName   string         `json:"table_name"`
	ColumnName  *string        `json:"column_name"`
	Description string         `json:"description"`
	Tags        []string       `json:"tags"`
	OwnerUserID db.UUID        `json:"owner_user_id"`
	UpdatedBy   db.UUID        `json:"updated_by"`
	UpdatedAt   db.Timestamptz `json:"updated_at"`
	Certified   bool           `json:"certified"`
	CertifiedBy db.UUID        `json:"certified_by"`
	CertifiedAt db.Timestamptz `json:"certified_at"`
}

type SemanticMetric struct {
	ID            db.UUID        `json:"id"`
	WorkspaceID   db.UUID        `json:"workspace_id"`
	Slug          string         `json:"slug"`
	Label         string         `json:"label"`
	Description   string         `json:"description"`
	Certified     bool           `json:"certified"`
	CatalogID     db.UUID        `json:"catalog_id"`
	Namespace     []string       `json:"namespace"`
	TableName     string         `json:"table_name"`
	BindingID     db.UUID        `json:"binding_id"`
	Definition    []byte         `json:"definition"`
	FormatVersion int32          `json:"format_version"`
	OwnerUserID   db.UUID        `json:"owner_user_id"`
	UpdatedBy     db.UUID        `json:"updated_by"`
	UpdatedAt     db.Timestamptz `json:"updated_at"`
	SpaceID       db.UUID        `json:"space_id"`
	CertifiedBy   db.UUID        `json:"certified_by"`
	CertifiedAt   db.Timestamptz `json:"certified_at"`
}

type SemanticModel struct {
	ID            db.UUID        `json:"id"`
	WorkspaceID   db.UUID        `json:"workspace_id"`
	SpaceID       db.UUID        `json:"space_id"`
	Slug          string         `json:"slug"`
	Label         string         `json:"label"`
	Description   string         `json:"description"`
	Certified     bool           `json:"certified"`
	CertifiedBy   db.UUID        `json:"certified_by"`
	CertifiedAt   db.Timestamptz `json:"certified_at"`
	CatalogID     db.UUID        `json:"catalog_id"`
	BindingID     db.UUID        `json:"binding_id"`
	Definition    []byte         `json:"definition"`
	FormatVersion int32          `json:"format_version"`
	OwnerUserID   db.UUID        `json:"owner_user_id"`
	UpdatedBy     db.UUID        `json:"updated_by"`
	UpdatedAt     db.Timestamptz `json:"updated_at"`
}

type Space struct {
	ID          db.UUID        `json:"id"`
	WorkspaceID db.UUID        `json:"workspace_id"`
	Name        string         `json:"name"`
	Description *string        `json:"description"`
	Visibility  string         `json:"visibility"`
	DefaultRole string         `json:"default_role"`
	IsDefault   bool           `json:"is_default"`
	CreatedBy   db.UUID        `json:"created_by"`
	CreatedAt   db.Timestamptz `json:"created_at"`
	UpdatedAt   db.Timestamptz `json:"updated_at"`
	Slug        string         `json:"slug"`
}

type SpaceMember struct {
	SpaceID       db.UUID        `json:"space_id"`
	PrincipalType string         `json:"principal_type"`
	PrincipalID   db.UUID        `json:"principal_id"`
	Role          string         `json:"role"`
	GrantedBy     db.UUID        `json:"granted_by"`
	GrantedAt     db.Timestamptz `json:"granted_at"`
}

type StatementJoin struct {
	StatementID db.UUID `json:"statement_id"`
	WorkspaceID db.UUID `json:"workspace_id"`
	LeftAlias   string  `json:"left_alias"`
	LeftColumn  string  `json:"left_column"`
	RightAlias  string  `json:"right_alias"`
	RightColumn string  `json:"right_column"`
}

type StatementReference struct {
	StatementID db.UUID `json:"statement_id"`
	WorkspaceID db.UUID `json:"workspace_id"`
	CatalogID   db.UUID `json:"catalog_id"`
	Namespace   string  `json:"namespace"`
	TableName   string  `json:"table_name"`
	Alias       string  `json:"alias"`
	Kind        string  `json:"kind"`
}

type TableHealthSnapshot struct {
	ID                     db.UUID        `json:"id"`
	CatalogConnectionID    db.UUID        `json:"catalog_connection_id"`
	Catalog                string         `json:"catalog"`
	Namespace              string         `json:"namespace"`
	TableName              string         `json:"table_name"`
	CapturedAt             db.Timestamptz `json:"captured_at"`
	SnapshotCount          int32          `json:"snapshot_count"`
	FileCount              *int32         `json:"file_count"`
	TotalSizeBytes         *int64         `json:"total_size_bytes"`
	OldestSnapshotAgeHours *int32         `json:"oldest_snapshot_age_hours"`
	LastMaintenanceAt      db.Timestamptz `json:"last_maintenance_at"`
	HealthScore            int32          `json:"health_score"`
	NeedsCompaction        bool           `json:"needs_compaction"`
	NeedsSnapshotExpiry    bool           `json:"needs_snapshot_expiry"`
	CatalogID              db.UUID        `json:"catalog_id"`
	DeleteFileCount        *int64         `json:"delete_file_count"`
	EqualityDeleteCount    *int64         `json:"equality_delete_count"`
	PositionDeleteCount    *int64         `json:"position_delete_count"`
	SmallFileCount         *int64         `json:"small_file_count"`
	CommitsLastHour        *int32         `json:"commits_last_hour"`
	CommitsLastDay         *int32         `json:"commits_last_day"`
	AvgFileSizeBytes       *int64         `json:"avg_file_size_bytes"`
	ManifestCount          *int32         `json:"manifest_count"`
	FormatVersion          *int32         `json:"format_version"`
	TableProperties        []byte         `json:"table_properties"`
	DueOperations          []string       `json:"due_operations"`
	PolicyRevision         *int64         `json:"policy_revision"`
}

type TableMaintenancePolicy struct {
	ID                          db.UUID        `json:"id"`
	CatalogID                   db.UUID        `json:"catalog_id"`
	Namespace                   string         `json:"namespace"`
	TableName                   string         `json:"table_name"`
	Mode                        string         `json:"mode"`
	WriterCommitIntervalSeconds int32          `json:"writer_commit_interval_seconds"`
	Optimize                    []byte         `json:"optimize"`
	OptimizeManifests           []byte         `json:"optimize_manifests"`
	ExpireSnapshots             []byte         `json:"expire_snapshots"`
	RemoveOrphanFiles           []byte         `json:"remove_orphan_files"`
	Revision                    int64          `json:"revision"`
	CreatedBy                   db.UUID        `json:"created_by"`
	UpdatedBy                   db.UUID        `json:"updated_by"`
	CreatedAt                   db.Timestamptz `json:"created_at"`
	UpdatedAt                   db.Timestamptz `json:"updated_at"`
}

type TablePopularity struct {
	WorkspaceID db.UUID        `json:"workspace_id"`
	CatalogID   db.UUID        `json:"catalog_id"`
	Namespace   string         `json:"namespace"`
	TableName   string         `json:"table_name"`
	Uses        int64          `json:"uses"`
	Score       float64        `json:"score"`
	LastUsedAt  db.Timestamptz `json:"last_used_at"`
	ComputedAt  db.Timestamptz `json:"computed_at"`
}

type Team struct {
	ID          db.UUID        `json:"id"`
	Name        string         `json:"name"`
	Description *string        `json:"description"`
	CreatedBy   db.UUID        `json:"created_by"`
	CreatedAt   db.Timestamptz `json:"created_at"`
	UpdatedAt   db.Timestamptz `json:"updated_at"`
}

type TeamMember struct {
	ID       db.UUID        `json:"id"`
	TeamID   db.UUID        `json:"team_id"`
	UserID   db.UUID        `json:"user_id"`
	Role     string         `json:"role"`
	JoinedAt db.Timestamptz `json:"joined_at"`
}

// Flat projection read by the baleia-trino-catalog-store plugin at Trino boot. Written by Baleia (write-through) and by the plugin (CREATE/DROP CATALOG). NEVER change the format without versioning the plugin alongside.
type TrinoCatalogRegistry struct {
	ClusterID      db.UUID        `json:"cluster_id"`
	CatalogName    string         `json:"catalog_name"`
	ConnectorName  string         `json:"connector_name"`
	Properties     []byte         `json:"properties"`
	CatalogVersion *string        `json:"catalog_version"`
	Enabled        bool           `json:"enabled"`
	SyncStatus     string         `json:"sync_status"`
	SyncError      *string        `json:"sync_error"`
	UpdatedBy      string         `json:"updated_by"`
	UpdatedAt      db.Timestamptz `json:"updated_at"`
	ExpiresAt      db.Timestamptz `json:"expires_at"`
}

type TrinoCluster struct {
	ID             db.UUID        `json:"id"`
	Name           string         `json:"name"`
	CoordinatorUrl string         `json:"coordinator_url"`
	NodeVersion    *string        `json:"node_version"`
	CreatedAt      db.Timestamptz `json:"created_at"`
	UpdatedAt      db.Timestamptz `json:"updated_at"`
	EngineID       db.UUID        `json:"engine_id"`
}

type TrinoQuerySnapshot struct {
	ID                 db.UUID        `json:"id"`
	TrinoQueryID       string         `json:"trino_query_id"`
	ExecutionID        db.UUID        `json:"execution_id"`
	Origin             string         `json:"origin"`
	User               string         `json:"user"`
	State              string         `json:"state"`
	Sql                string         `json:"sql"`
	Catalog            *string        `json:"catalog"`
	Schema             *string        `json:"schema"`
	CreatedAt          db.Timestamptz `json:"created_at"`
	ElapsedMs          *int32         `json:"elapsed_ms"`
	CpuMs              *int32         `json:"cpu_ms"`
	PhysicalInputBytes *int64         `json:"physical_input_bytes"`
	OutputRows         *int64         `json:"output_rows"`
	ErrorMessage       *string        `json:"error_message"`
	RawTrinoPayload    []byte         `json:"raw_trino_payload"`
	FirstSeenAt        db.Timestamptz `json:"first_seen_at"`
	LastSeenAt         db.Timestamptz `json:"last_seen_at"`
	EngineID           db.UUID        `json:"engine_id"`
}

type TrinoResourceGroupRevision struct {
	ID        db.UUID        `json:"id"`
	EngineID  db.UUID        `json:"engine_id"`
	Revision  int32          `json:"revision"`
	Config    []byte         `json:"config"`
	CreatedAt db.Timestamptz `json:"created_at"`
}

type User struct {
	ID                  db.UUID        `json:"id"`
	Name                string         `json:"name"`
	Email               string         `json:"email"`
	PasswordHash        *string        `json:"password_hash"`
	Role                string         `json:"role"`
	AuthProvider        string         `json:"auth_provider"`
	AuthProviderID      db.UUID        `json:"auth_provider_id"`
	ExternalID          *string        `json:"external_id"`
	PersonalWorkspaceID db.UUID        `json:"personal_workspace_id"`
	CreatedAt           db.Timestamptz `json:"created_at"`
	UpdatedAt           db.Timestamptz `json:"updated_at"`
	Status              string         `json:"status"`
	SuspendedAt         db.Timestamptz `json:"suspended_at"`
	SuspendedReason     *string        `json:"suspended_reason"`
	DirectorySyncedAt   db.Timestamptz `json:"directory_synced_at"`
	Locale              *string        `json:"locale"`
	Kind                string         `json:"kind"`
	HomeWorkspaceID     db.UUID        `json:"home_workspace_id"`
	Description         *string        `json:"description"`
}

type UserIdpToken struct {
	UserID          db.UUID        `json:"user_id"`
	AuthProviderID  db.UUID        `json:"auth_provider_id"`
	AccessTokenEnc  string         `json:"access_token_enc"`
	RefreshTokenEnc *string        `json:"refresh_token_enc"`
	ExpiresAt       db.Timestamptz `json:"expires_at"`
	UpdatedAt       db.Timestamptz `json:"updated_at"`
}

type WorkloadRule struct {
	ID        db.UUID        `json:"id"`
	Position  int32          `json:"position"`
	Enabled   bool           `json:"enabled"`
	Match     []byte         `json:"match"`
	Class     string         `json:"class"`
	MaxClass  *string        `json:"max_class"`
	Weight    int32          `json:"weight"`
	CreatedBy db.UUID        `json:"created_by"`
	CreatedAt db.Timestamptz `json:"created_at"`
	UpdatedAt db.Timestamptz `json:"updated_at"`
}

type Workspace struct {
	ID                 db.UUID        `json:"id"`
	Name               string         `json:"name"`
	Description        *string        `json:"description"`
	Type               string         `json:"type"`
	PolarisCatalogName string         `json:"polaris_catalog_name"`
	OwnerUserID        db.UUID        `json:"owner_user_id"`
	OwnerTeamID        db.UUID        `json:"owner_team_id"`
	CreatedAt          db.Timestamptz `json:"created_at"`
	UpdatedAt          db.Timestamptz `json:"updated_at"`
	DefaultBindingID   db.UUID        `json:"default_binding_id"`
	DefaultSchema      *string        `json:"default_schema"`
}

type WorkspaceCatalogGrant struct {
	WorkspaceID db.UUID        `json:"workspace_id"`
	CatalogID   db.UUID        `json:"catalog_id"`
	GrantedBy   db.UUID        `json:"granted_by"`
	GrantedAt   db.Timestamptz `json:"granted_at"`
}

type WorkspaceMember struct {
	WorkspaceID db.UUID        `json:"workspace_id"`
	UserID      db.UUID        `json:"user_id"`
	Role        string         `json:"role"`
	GrantedBy   db.UUID        `json:"granted_by"`
	GrantedAt   db.Timestamptz `json:"granted_at"`
	Source      string         `json:"source"`
}
