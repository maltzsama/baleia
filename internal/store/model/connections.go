package model

import "time"

// CatalogConnection is the catalog_connections table.
type CatalogConnection struct {
	ID                  UUID      `gorm:"column:id;primaryKey;not null;check:catalog_connections_type_check,((type IN ('polaris', 'nessie', 'iceberg', 'lakekeeper', 'unity', 'hive', 'glue', 'postgresql', 'mysql', 'mariadb', 'sqlserver', 'oracle', 'sqlite', 'clickhouse', 'flightsql', 'trino', 'doris', 'starrocks')))"`
	Name                Text      `gorm:"column:name;not null;size:128;uniqueIndex:catalog_connections_name_key,priority:1"`
	Type                string    `gorm:"column:type;not null"`
	AccessMode          *string   `gorm:"column:access_mode;check:catalog_connections_access_mode_check,(((access_mode IS NULL) OR (access_mode IN ('direct', 'trino'))))"`
	URL                 string    `gorm:"column:url;not null"`
	CredentialsEnc      *string   `gorm:"column:credentials_enc"`
	CreatedAt           time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Host                *string   `gorm:"column:host"`
	Port                *string   `gorm:"column:port"`
	CreatedBy           *UUID     `gorm:"column:created_by"`
	Config              JSON      `gorm:"column:config;not null;default:('{}')"`
	CredentialsReadOnly bool      `gorm:"column:credentials_read_only;not null;default:false"`
	CreatedByRef        *User     `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (CatalogConnection) TableName() string { return "catalog_connections" }

// Engine is the engines table.
type Engine struct {
	ID                         UUID               `gorm:"column:id;primaryKey;not null"`
	Name                       Text               `gorm:"column:name;not null;size:128;uniqueIndex:engines_name_key,priority:1;check:engines_class_check,((class IN ('federated', 'direct')))"`
	Kind                       string             `gorm:"column:kind;not null;check:engines_identity_mode_check,((identity_mode IN ('impersonation', 'connection', 'per_user')))"`
	Class                      string             `gorm:"column:class;not null;check:engines_max_running_total_check,(((max_running_total IS NULL) OR (max_running_total >= 0)))"`
	ConnectionID               UUID               `gorm:"column:connection_id;not null;uniqueIndex:engines_connection_id_key,priority:1;check:engines_cost_threshold_bytes_check,(((cost_threshold_bytes IS NULL) OR (cost_threshold_bytes > 0)))"`
	IdentityMode               Text               `gorm:"column:identity_mode;not null;size:255;default:'connection'"`
	Enabled                    bool               `gorm:"column:enabled;not null;default:true"`
	CreatedBy                  *UUID              `gorm:"column:created_by"`
	CreatedAt                  time.Time          `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt                  time.Time          `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	MaxRunningTotal            *int32             `gorm:"column:max_running_total"`
	CostRoutingEnabled         bool               `gorm:"column:cost_routing_enabled;not null;default:false"`
	CostThresholdBytes         *int64             `gorm:"column:cost_threshold_bytes"`
	ExternalURL                *string            `gorm:"column:external_url"`
	TrinoResourceGroupsEnabled bool               `gorm:"column:trino_resource_groups_enabled;not null;default:false"`
	Connection                 *CatalogConnection `gorm:"foreignKey:ConnectionID;references:ID;constraint:OnDelete:RESTRICT"`
	CreatedByRef               *User              `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (Engine) TableName() string { return "engines" }

// Catalog is the catalogs table.
type Catalog struct {
	ID               UUID               `gorm:"column:id;primaryKey;not null"`
	ConnectionID     UUID               `gorm:"column:connection_id;not null;uniqueIndex:catalogs_connection_id_name_key,priority:1;check:catalogs_trino_name_not_reserved,((trino_catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory', 'env', 'file')))"`
	Name             Text               `gorm:"column:name;not null;size:128;uniqueIndex:catalogs_connection_id_name_key,priority:2"`
	Description      *string            `gorm:"column:description;check:catalogs_origin_check,((origin IN ('source', 'native')))"`
	TrinoCatalogName *Text              `gorm:"column:trino_catalog_name;size:128;uniqueIndex:catalogs_cluster_id_trino_catalog_name_key,priority:2;check:catalogs_source_has_trino_columns,(((origin = 'native') OR ((trino_catalog_name IS NOT NULL) AND (cluster_id IS NOT NULL) AND (warehouse IS NOT NULL) AND (connector IS NOT NULL))))"`
	ClusterID        *UUID              `gorm:"column:cluster_id;uniqueIndex:catalogs_cluster_id_trino_catalog_name_key,priority:1"`
	Warehouse        *string            `gorm:"column:warehouse"`
	Connector        *Text              `gorm:"column:connector;size:255;default:'iceberg'"`
	ExtraProps       JSON               `gorm:"column:extra_props;not null;default:('{}')"`
	Enabled          bool               `gorm:"column:enabled;not null;default:true"`
	CreatedAt        time.Time          `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt        time.Time          `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Origin           Text               `gorm:"column:origin;not null;size:255;default:'source'"`
	SourceRef        *string            `gorm:"column:source_ref"`
	Discoverable     bool               `gorm:"column:discoverable;not null;default:false"`
	Connection       *CatalogConnection `gorm:"foreignKey:ConnectionID;references:ID;constraint:OnDelete:CASCADE"`
	Cluster          *TrinoCluster      `gorm:"foreignKey:ClusterID;references:ID"`
}

func (Catalog) TableName() string { return "catalogs" }

// CatalogBinding is the catalog_bindings table.
type CatalogBinding struct {
	ID                UUID      `gorm:"column:id;primaryKey;not null;check:catalog_bindings_mode_check,((mode IN ('managed', 'adopted', 'native')))"`
	CatalogID         UUID      `gorm:"column:catalog_id;not null;uniqueIndex:catalog_bindings_catalog_id_engine_id_key,priority:1;index:catalog_bindings_catalog_idx,priority:1;check:catalog_bindings_lifecycle_state_check,((lifecycle_state IN ('provisioning', 'active', 'updating', 'error', 'deprovisioning', 'disabled')))"`
	EngineID          UUID      `gorm:"column:engine_id;not null;uniqueIndex:catalog_bindings_engine_id_engine_catalog_name_key,priority:1;uniqueIndex:catalog_bindings_catalog_id_engine_id_key,priority:2"`
	EngineCatalogName Text      `gorm:"column:engine_catalog_name;not null;size:128;uniqueIndex:catalog_bindings_engine_id_engine_catalog_name_key,priority:2"`
	Connector         *string   `gorm:"column:connector"`
	ExtraProps        JSON      `gorm:"column:extra_props;not null;default:('{}')"`
	Mode              string    `gorm:"column:mode;not null"`
	Enabled           bool      `gorm:"column:enabled;not null;default:true"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	LifecycleState    Text      `gorm:"column:lifecycle_state;not null;size:255;default:'active'"`
	DesiredVersion    *string   `gorm:"column:desired_version"`
	LastErrorCode     *string   `gorm:"column:last_error_code"`
	LastErrorMessage  *string   `gorm:"column:last_error_message"`
	StateChangedAt    time.Time `gorm:"column:state_changed_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Catalog           *Catalog  `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
	Engine            *Engine   `gorm:"foreignKey:EngineID;references:ID;constraint:OnDelete:CASCADE"`
}

func (CatalogBinding) TableName() string { return "catalog_bindings" }

// EngineCatalogObservation is the engine_catalog_observations table.
type EngineCatalogObservation struct {
	EngineID    UUID      `gorm:"column:engine_id;primaryKey;not null;check:engine_catalog_observations_mode_check,((mode IN ('managed', 'adopted', 'native')))"`
	CatalogName Text      `gorm:"column:catalog_name;primaryKey;not null;size:128"`
	Present     bool      `gorm:"column:present;not null"`
	Mode        string    `gorm:"column:mode;not null"`
	Connector   *string   `gorm:"column:connector"`
	FirstSeenAt time.Time `gorm:"column:first_seen_at;not null;default:CURRENT_TIMESTAMP(3)"`
	ObservedAt  time.Time `gorm:"column:observed_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Engine      *Engine   `gorm:"foreignKey:EngineID;references:ID;constraint:OnDelete:CASCADE"`
}

func (EngineCatalogObservation) TableName() string { return "engine_catalog_observations" }

// EngineQueueCapacity is the engine_queue_capacity table.
type EngineQueueCapacity struct {
	EngineID         UUID      `gorm:"column:engine_id;primaryKey;not null;check:engine_queue_capacity_class_check,((class IN ('interactive', 'heavy', 'background')))"`
	Class            Text      `gorm:"column:class;primaryKey;not null;size:128;check:engine_queue_capacity_max_running_check,((max_running >= 0))"`
	MaxRunning       int32     `gorm:"column:max_running;not null;check:engine_queue_capacity_max_queued_check,((max_queued >= 0))"`
	MaxQueued        int32     `gorm:"column:max_queued;not null;check:engine_queue_capacity_queue_timeout_ms_check,((queue_timeout_ms > 0))"`
	QueueTimeoutMs   int64     `gorm:"column:queue_timeout_ms;not null;check:engine_queue_capacity_runtime_timeout_ms_check,(((runtime_timeout_ms IS NULL) OR (runtime_timeout_ms > 0)))"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	RuntimeTimeoutMs *int64    `gorm:"column:runtime_timeout_ms"`
	Engine           *Engine   `gorm:"foreignKey:EngineID;references:ID;constraint:OnDelete:CASCADE"`
}

func (EngineQueueCapacity) TableName() string { return "engine_queue_capacity" }

// TrinoCatalogRegistry is the trino_catalog_registry table.
type TrinoCatalogRegistry struct {
	ClusterID      UUID          `gorm:"column:cluster_id;primaryKey;not null;index:trino_catalog_registry_enabled_idx,priority:1,where:enabled"`
	CatalogName    Text          `gorm:"column:catalog_name;primaryKey;not null;size:128"`
	ConnectorName  string        `gorm:"column:connector_name;not null;check:registry_sync_status_valid,((sync_status IN ('pending', 'synced', 'error')))"`
	Properties     JSON          `gorm:"column:properties;not null;check:registry_updated_by_valid,((updated_by IN ('baleia', 'trino')))"`
	CatalogVersion *string       `gorm:"column:catalog_version;check:registry_catalog_name_not_reserved,((catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory', 'env', 'file')))"`
	Enabled        bool          `gorm:"column:enabled;not null;default:true"`
	SyncStatus     Text          `gorm:"column:sync_status;not null;size:255;default:'pending'"`
	SyncError      *string       `gorm:"column:sync_error"`
	UpdatedBy      Text          `gorm:"column:updated_by;not null;size:255;default:'baleia'"`
	UpdatedAt      time.Time     `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	ExpiresAt      *time.Time    `gorm:"column:expires_at;index:trino_catalog_registry_expires,priority:1,where:expires_at IS NOT NULL"`
	Cluster        *TrinoCluster `gorm:"foreignKey:ClusterID;references:ID;constraint:OnDelete:CASCADE"`
}

func (TrinoCatalogRegistry) TableName() string { return "trino_catalog_registry" }

// CatalogGrantRequest is the catalog_grant_requests table.
type CatalogGrantRequest struct {
	WorkspaceID     UUID       `gorm:"column:workspace_id;primaryKey;not null;check:catalog_grant_requests_initiated_by_side_check,((initiated_by_side IN ('catalog', 'workspace')))"`
	CatalogID       UUID       `gorm:"column:catalog_id;primaryKey;not null"`
	InitiatedBySide string     `gorm:"column:initiated_by_side;not null"`
	RequestedBy     *UUID      `gorm:"column:requested_by"`
	RequestedAt     time.Time  `gorm:"column:requested_at;not null;default:CURRENT_TIMESTAMP(3)"`
	RequestedByRef  *User      `gorm:"foreignKey:RequestedBy;references:ID;constraint:OnDelete:SET NULL"`
	Workspace       *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	Catalog         *Catalog   `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
}

func (CatalogGrantRequest) TableName() string { return "catalog_grant_requests" }

// WorkspaceCatalogGrant is the workspace_catalog_grants table.
type WorkspaceCatalogGrant struct {
	WorkspaceID  UUID       `gorm:"column:workspace_id;primaryKey;not null"`
	CatalogID    UUID       `gorm:"column:catalog_id;primaryKey;not null;index:idx_workspace_catalog_grants_catalog,priority:1"`
	GrantedBy    UUID       `gorm:"column:granted_by;not null"`
	GrantedAt    time.Time  `gorm:"column:granted_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Workspace    *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	Catalog      *Catalog   `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
	GrantedByRef *User      `gorm:"foreignKey:GrantedBy;references:ID;constraint:OnDelete:RESTRICT"`
}

func (WorkspaceCatalogGrant) TableName() string { return "workspace_catalog_grants" }
