package model

import "time"

// ClusterSnapshot is the cluster_snapshots table.
type ClusterSnapshot struct {
	ID               UUID      `gorm:"column:id;primaryKey;not null"`
	CapturedAt       time.Time `gorm:"column:captured_at;not null;default:CURRENT_TIMESTAMP(3);index:idx_cluster_snapshots_captured,priority:1;index:cluster_snapshots_engine_time,priority:2"`
	ActiveWorkers    int32     `gorm:"column:active_workers;not null;default:0"`
	RunningQueries   int32     `gorm:"column:running_queries;not null;default:0"`
	QueuedQueries    int32     `gorm:"column:queued_queries;not null;default:0"`
	BlockedQueries   int32     `gorm:"column:blocked_queries;not null;default:0"`
	TotalMemoryBytes *int64    `gorm:"column:total_memory_bytes"`
	FreeMemoryBytes  *int64    `gorm:"column:free_memory_bytes"`
	RawPayload       *JSON     `gorm:"column:raw_payload"`
	EngineID         UUID      `gorm:"column:engine_id;not null;index:cluster_snapshots_engine_time,priority:1"`
	Engine           *Engine   `gorm:"foreignKey:EngineID;references:ID;constraint:OnDelete:CASCADE"`
}

func (ClusterSnapshot) TableName() string { return "cluster_snapshots" }

// TrinoQuerySnapshot is the trino_query_snapshots table.
type TrinoQuerySnapshot struct {
	ID                 UUID            `gorm:"column:id;primaryKey;not null;check:trino_query_snapshots_origin_check,((origin IN ('baleia', 'trino_direct')))"`
	TrinoQueryID       Text            `gorm:"column:trino_query_id;not null;size:128;uniqueIndex:trino_query_snapshots_trino_query_id_key,priority:1;index:idx_trino_query_snapshots_query_id,priority:1;check:trino_query_snapshots_state_check,((state IN ('running', 'finished', 'failed', 'cancelled')))"`
	ExecutionID        *UUID           `gorm:"column:execution_id"`
	Origin             Text            `gorm:"column:origin;not null;size:128;index:idx_trino_query_snapshots_origin,priority:1"`
	User               Text            `gorm:"column:user;not null;size:128;index:idx_trino_query_snapshots_user,priority:1"`
	State              Text            `gorm:"column:state;not null;size:128;index:idx_trino_query_snapshots_state,priority:1"`
	SQL                string          `gorm:"column:sql;not null"`
	Catalog            *string         `gorm:"column:catalog"`
	Schema             *string         `gorm:"column:schema"`
	CreatedAt          time.Time       `gorm:"column:created_at;not null;index:idx_trino_query_snapshots_created,priority:1;index:trino_query_snapshots_engine_time,priority:2"`
	ElapsedMs          *int32          `gorm:"column:elapsed_ms"`
	CPUMs              *int32          `gorm:"column:cpu_ms"`
	PhysicalInputBytes *int64          `gorm:"column:physical_input_bytes"`
	OutputRows         *int64          `gorm:"column:output_rows"`
	ErrorMessage       *string         `gorm:"column:error_message"`
	RawTrinoPayload    *JSON           `gorm:"column:raw_trino_payload"`
	FirstSeenAt        time.Time       `gorm:"column:first_seen_at;not null;default:CURRENT_TIMESTAMP(3)"`
	LastSeenAt         time.Time       `gorm:"column:last_seen_at;not null;default:CURRENT_TIMESTAMP(3)"`
	EngineID           UUID            `gorm:"column:engine_id;not null;index:trino_query_snapshots_engine_time,priority:1"`
	Execution          *QueryExecution `gorm:"foreignKey:ExecutionID;references:ID;constraint:OnDelete:SET NULL"`
	Engine             *Engine         `gorm:"foreignKey:EngineID;references:ID;constraint:OnDelete:CASCADE"`
}

func (TrinoQuerySnapshot) TableName() string { return "trino_query_snapshots" }

// TrinoCluster is the trino_clusters table.
type TrinoCluster struct {
	ID             UUID       `gorm:"column:id;primaryKey;not null"`
	Name           Text       `gorm:"column:name;not null;size:128;uniqueIndex:trino_clusters_name_key,priority:1"`
	CoordinatorURL string     `gorm:"column:coordinator_url;not null"`
	NodeVersion    *string    `gorm:"column:node_version"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt      *time.Time `gorm:"column:updated_at"`
	EngineID       *UUID      `gorm:"column:engine_id;uniqueIndex:trino_clusters_engine_id_key,priority:1"`
	Engine         *Engine    `gorm:"foreignKey:EngineID;references:ID;constraint:OnDelete:RESTRICT"`
}

func (TrinoCluster) TableName() string { return "trino_clusters" }

// TrinoResourceGroupRevision is the trino_resource_group_revisions table.
type TrinoResourceGroupRevision struct {
	ID        UUID      `gorm:"column:id;primaryKey;not null"`
	EngineID  UUID      `gorm:"column:engine_id;not null;uniqueIndex:trino_resource_group_revisions_engine_id_revision_key,priority:1"`
	Revision  int32     `gorm:"column:revision;not null;uniqueIndex:trino_resource_group_revisions_engine_id_revision_key,priority:2"`
	Config    JSON      `gorm:"column:config;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Engine    *Engine   `gorm:"foreignKey:EngineID;references:ID;constraint:OnDelete:CASCADE"`
}

func (TrinoResourceGroupRevision) TableName() string { return "trino_resource_group_revisions" }

// ResourceGroup is the resource_groups table.
type ResourceGroup struct {
	ResourceGroupID           int64          `gorm:"column:resource_group_id;primaryKey;autoIncrement"`
	Name                      string         `gorm:"column:name;not null;size:250"`
	SoftMemoryLimit           string         `gorm:"column:soft_memory_limit;not null;size:128"`
	MaxQueued                 int32          `gorm:"column:max_queued;not null"`
	SoftConcurrencyLimit      *int32         `gorm:"column:soft_concurrency_limit"`
	HardConcurrencyLimit      int32          `gorm:"column:hard_concurrency_limit;not null"`
	SchedulingPolicy          *string        `gorm:"column:scheduling_policy;size:128"`
	SchedulingWeight          *int32         `gorm:"column:scheduling_weight"`
	JmxExport                 *bool          `gorm:"column:jmx_export"`
	SoftCPULimit              *string        `gorm:"column:soft_cpu_limit;size:128"`
	HardCPULimit              *string        `gorm:"column:hard_cpu_limit;size:128"`
	HardPhysicalDataScanLimit *string        `gorm:"column:hard_physical_data_scan_limit;size:128"`
	Parent                    *int64         `gorm:"column:parent"`
	Environment               *string        `gorm:"column:environment;size:128"`
	ParentRef                 *ResourceGroup `gorm:"foreignKey:Parent;references:ResourceGroupID;constraint:OnDelete:CASCADE"`
}

func (ResourceGroup) TableName() string { return "resource_groups" }

// ResourceGroupsGlobalProperty is the resource_groups_global_properties table.
type ResourceGroupsGlobalProperty struct {
	Name  string  `gorm:"column:name;primaryKey;not null;size:128"`
	Value *string `gorm:"column:value;size:512"`
}

func (ResourceGroupsGlobalProperty) TableName() string { return "resource_groups_global_properties" }

// TableHealthSnapshot is the table_health_snapshots table.
type TableHealthSnapshot struct {
	ID                     UUID               `gorm:"column:id;primaryKey;not null;check:table_health_snapshots_health_score_check,(((health_score >= 0) AND (health_score <= 100)))"`
	CatalogConnectionID    UUID               `gorm:"column:catalog_connection_id;not null;uniqueIndex:table_health_snapshots_catalog_connection_id_catalog_namesp_key,priority:1;index:idx_table_health_conn,priority:1"`
	Catalog                Text               `gorm:"column:catalog;not null;size:128;uniqueIndex:table_health_snapshots_catalog_connection_id_catalog_namesp_key,priority:2"`
	Namespace              Text               `gorm:"column:namespace;not null;size:128;uniqueIndex:table_health_snapshots_catalog_connection_id_catalog_namesp_key,priority:3;uniqueIndex:table_health_unique_catalog,priority:2"`
	TableNameValue         Text               `gorm:"column:table_name;not null;size:128;uniqueIndex:table_health_snapshots_catalog_connection_id_catalog_namesp_key,priority:4;uniqueIndex:table_health_unique_catalog,priority:3"`
	CapturedAt             time.Time          `gorm:"column:captured_at;not null;default:CURRENT_TIMESTAMP(3);index:idx_table_health_captured,priority:1"`
	SnapshotCount          int32              `gorm:"column:snapshot_count;not null;default:0"`
	FileCount              *int32             `gorm:"column:file_count"`
	TotalSizeBytes         *int64             `gorm:"column:total_size_bytes"`
	OldestSnapshotAgeHours *int32             `gorm:"column:oldest_snapshot_age_hours"`
	LastMaintenanceAt      *time.Time         `gorm:"column:last_maintenance_at"`
	HealthScore            int32              `gorm:"column:health_score;not null;default:100"`
	NeedsCompaction        bool               `gorm:"column:needs_compaction;not null;default:false"`
	NeedsSnapshotExpiry    bool               `gorm:"column:needs_snapshot_expiry;not null;default:false"`
	CatalogID              UUID               `gorm:"column:catalog_id;not null;uniqueIndex:table_health_unique_catalog,priority:1"`
	DeleteFileCount        *int64             `gorm:"column:delete_file_count"`
	EqualityDeleteCount    *int64             `gorm:"column:equality_delete_count"`
	PositionDeleteCount    *int64             `gorm:"column:position_delete_count"`
	SmallFileCount         *int64             `gorm:"column:small_file_count"`
	CommitsLastHour        *int32             `gorm:"column:commits_last_hour"`
	CommitsLastDay         *int32             `gorm:"column:commits_last_day"`
	AvgFileSizeBytes       *int64             `gorm:"column:avg_file_size_bytes"`
	ManifestCount          *int32             `gorm:"column:manifest_count"`
	FormatVersion          *int32             `gorm:"column:format_version"`
	TableProperties        JSON               `gorm:"column:table_properties;not null;default:('{}')"`
	DueOperations          StringArray        `gorm:"column:due_operations;not null;default:'{}'"`
	PolicyRevision         *int64             `gorm:"column:policy_revision"`
	CatalogConnection      *CatalogConnection `gorm:"foreignKey:CatalogConnectionID;references:ID;constraint:OnDelete:CASCADE"`
	CatalogRef             *Catalog           `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
}

func (TableHealthSnapshot) TableName() string { return "table_health_snapshots" }

// MaintenanceRun is the maintenance_runs table.
type MaintenanceRun struct {
	ID             UUID            `gorm:"column:id;primaryKey;not null;check:maintenance_runs_trigger_check,((maintenance_runs.trigger IN ('manual', 'automatic', 'dry_run')))"`
	CatalogID      UUID            `gorm:"column:catalog_id;not null;index:maintenance_runs_catalog,priority:1;uniqueIndex:maintenance_runs_one_running,priority:1,where:status = 'running';index:maintenance_runs_table,priority:1;check:maintenance_runs_attempts_check,((attempts > 0))"`
	BindingID      *UUID           `gorm:"column:binding_id;check:maintenance_runs_status_check,((status IN ('running', 'finished', 'failed', 'gave_up', 'dry_run')))"`
	Namespace      Text            `gorm:"column:namespace;not null;size:128;uniqueIndex:maintenance_runs_one_running,priority:2;index:maintenance_runs_table,priority:2"`
	TableNameValue Text            `gorm:"column:table_name;not null;size:128;uniqueIndex:maintenance_runs_one_running,priority:3;index:maintenance_runs_table,priority:3"`
	Operations     StringArray     `gorm:"column:operations;not null"`
	Retention      *string         `gorm:"column:retention"`
	Status         string          `gorm:"column:status;not null"`
	ErrorCode      *string         `gorm:"column:error_code"`
	ErrorMessage   *string         `gorm:"column:error_message"`
	StartedBy      *UUID           `gorm:"column:started_by"`
	StartedAt      time.Time       `gorm:"column:started_at;not null;default:CURRENT_TIMESTAMP(3);index:maintenance_runs_catalog,priority:2;index:maintenance_runs_table,priority:4"`
	FinishedAt     *time.Time      `gorm:"column:finished_at"`
	WorkflowID     string          `gorm:"column:workflow_id;not null"`
	Trigger        Text            `gorm:"column:trigger;not null;size:255;default:'manual'"`
	Reason         JSON            `gorm:"column:reason;not null;default:('{}')"`
	Parameters     JSON            `gorm:"column:parameters;not null;default:('{}')"`
	Attempts       int32           `gorm:"column:attempts;not null;default:1"`
	PolicyRevision *int64          `gorm:"column:policy_revision"`
	Catalog        *Catalog        `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
	Binding        *CatalogBinding `gorm:"foreignKey:BindingID;references:ID;constraint:OnDelete:SET NULL"`
	StartedByRef   *User           `gorm:"foreignKey:StartedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (MaintenanceRun) TableName() string { return "maintenance_runs" }

// TableMaintenancePolicy is the table_maintenance_policies table.
type TableMaintenancePolicy struct {
	ID                          UUID      `gorm:"column:id;primaryKey;not null;check:table_maintenance_policies_mode_check,((mode IN ('managed', 'off', 'maintained_elsewhere')))"`
	CatalogID                   UUID      `gorm:"column:catalog_id;not null;uniqueIndex:table_maintenance_policies_catalog_id_namespace_table_name_key,priority:1;index:table_maintenance_policies_managed,priority:1,where:mode = 'managed';check:table_maintenance_policies_writer_commit_interval_seconds_check,((writer_commit_interval_seconds > 0))"`
	Namespace                   Text      `gorm:"column:namespace;not null;size:128;uniqueIndex:table_maintenance_policies_catalog_id_namespace_table_name_key,priority:2"`
	TableNameValue              Text      `gorm:"column:table_name;not null;size:128;uniqueIndex:table_maintenance_policies_catalog_id_namespace_table_name_key,priority:3"`
	Mode                        string    `gorm:"column:mode;not null"`
	WriterCommitIntervalSeconds int32     `gorm:"column:writer_commit_interval_seconds;not null;default:60"`
	Optimize                    JSON      `gorm:"column:optimize;not null;default:('{}')"`
	OptimizeManifests           JSON      `gorm:"column:optimize_manifests;not null;default:('{}')"`
	ExpireSnapshots             JSON      `gorm:"column:expire_snapshots;not null;default:('{}')"`
	RemoveOrphanFiles           JSON      `gorm:"column:remove_orphan_files;not null;default:('{}')"`
	Revision                    int64     `gorm:"column:revision;not null;default:1"`
	CreatedBy                   *UUID     `gorm:"column:created_by"`
	UpdatedBy                   *UUID     `gorm:"column:updated_by"`
	CreatedAt                   time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt                   time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Catalog                     *Catalog  `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
	CreatedByRef                *User     `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:SET NULL"`
	UpdatedByRef                *User     `gorm:"foreignKey:UpdatedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (TableMaintenancePolicy) TableName() string { return "table_maintenance_policies" }

// WorkloadRule is the workload_rules table.
type WorkloadRule struct {
	ID           UUID      `gorm:"column:id;primaryKey;not null"`
	Position     int32     `gorm:"column:position;not null;index:workload_rules_position_idx,priority:1;check:workload_rules_class_check,((class IN ('interactive', 'heavy', 'background')))"`
	Enabled      bool      `gorm:"column:enabled;not null;default:true;check:workload_rules_max_class_check,(((max_class IS NULL) OR (max_class IN ('interactive', 'heavy', 'background'))))"`
	Match        JSON      `gorm:"column:match;not null;default:('{}');check:workload_rules_weight_check,((weight > 0))"`
	Class        string    `gorm:"column:class;not null"`
	MaxClass     *string   `gorm:"column:max_class"`
	Weight       int32     `gorm:"column:weight;not null;default:1"`
	CreatedBy    *UUID     `gorm:"column:created_by"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	CreatedByRef *User     `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (WorkloadRule) TableName() string { return "workload_rules" }

// Selector is the selectors table.
type Selector struct {
	ID                     int64          `gorm:"column:id;primaryKey;autoIncrement"`
	GroupID                int64          `gorm:"column:resource_group_id;not null"`
	Priority               int64          `gorm:"column:priority;not null"`
	UserRegex              *string        `gorm:"column:user_regex;size:512"`
	OriginalUserRegex      *string        `gorm:"column:original_user_regex;size:512"`
	AuthenticatedUserRegex *string        `gorm:"column:authenticated_user_regex;size:512"`
	UserGroupRegex         *string        `gorm:"column:user_group_regex;size:512"`
	SourceRegex            *string        `gorm:"column:source_regex;size:512"`
	QueryType              *string        `gorm:"column:query_type;size:128"`
	ClientTags             *string        `gorm:"column:client_tags"`
	ResourceGroup          *ResourceGroup `gorm:"foreignKey:GroupID;references:ResourceGroupID;constraint:OnDelete:CASCADE"`
}

func (Selector) TableName() string { return "selectors" }

// DirectoryReconcileRun is the directory_reconcile_runs table.
type DirectoryReconcileRun struct {
	ID         UUID          `gorm:"column:id;primaryKey;not null;check:directory_reconcile_runs_outcome_check,((outcome IN ('success', 'error', 'aborted_safety')))"`
	ProviderID UUID          `gorm:"column:provider_id;not null;index:idx_directory_reconcile_runs_provider,priority:1"`
	StartedAt  time.Time     `gorm:"column:started_at;not null;default:CURRENT_TIMESTAMP(3);index:idx_directory_reconcile_runs_provider,priority:2"`
	FinishedAt *time.Time    `gorm:"column:finished_at"`
	Outcome    *string       `gorm:"column:outcome"`
	Seen       int32         `gorm:"column:seen;not null;default:0"`
	Suspended  int32         `gorm:"column:suspended;not null;default:0"`
	Reinstated int32         `gorm:"column:reinstated;not null;default:0"`
	Error      *string       `gorm:"column:error"`
	Actions    JSON          `gorm:"column:actions;not null;default:('[]')"`
	Provider   *AuthProvider `gorm:"foreignKey:ProviderID;references:ID;constraint:OnDelete:CASCADE"`
}

func (DirectoryReconcileRun) TableName() string { return "directory_reconcile_runs" }

// CatalogReconcileRun is the catalog_reconcile_runs table.
type CatalogReconcileRun struct {
	ID         UUID        `gorm:"column:id;primaryKey;not null"`
	EngineID   UUID        `gorm:"column:engine_id;not null;index:catalog_reconcile_runs_engine,priority:1"`
	StartedAt  time.Time   `gorm:"column:started_at;not null;index:catalog_reconcile_runs_engine,priority:2"`
	FinishedAt time.Time   `gorm:"column:finished_at;not null"`
	DryRun     bool        `gorm:"column:dry_run;not null"`
	Actions    JSON        `gorm:"column:actions;not null;default:('[]')"`
	Adopted    StringArray `gorm:"column:adopted;not null;default:'{}'"`
	Error      *string     `gorm:"column:error"`
	Engine     *Engine     `gorm:"foreignKey:EngineID;references:ID;constraint:OnDelete:CASCADE"`
}

func (CatalogReconcileRun) TableName() string { return "catalog_reconcile_runs" }

// MonitoringEvent is the monitoring outbox: a writer inserts an event and every
// API replica reads it and relays it to its SSE subscribers (#733).
type MonitoringEvent struct {
	ID        uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	Channel   Text      `gorm:"column:channel;not null;size:64;index:idx_monitoring_events_channel_id,priority:1"`
	Payload   string    `gorm:"column:payload;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3);index:idx_monitoring_events_channel_id,priority:2"`
}

func (MonitoringEvent) TableName() string { return "monitoring_events" }
