package model

import "time"

// SemanticDoc is the semantic_docs table.
type SemanticDoc struct {
	ID             UUID        `gorm:"column:id;primaryKey;not null;check:semantic_docs_description_check,((char_length(description) <= 20000))"`
	WorkspaceID    UUID        `gorm:"column:workspace_id;not null;uniqueIndex:semantic_docs_workspace_id_catalog_id_namespace_table_name__key,priority:1;index:semantic_docs_lookup,priority:1"`
	CatalogID      UUID        `gorm:"column:catalog_id;not null;uniqueIndex:semantic_docs_workspace_id_catalog_id_namespace_table_name__key,priority:2;index:semantic_docs_lookup,priority:2"`
	Namespace      StringArray `gorm:"column:namespace;not null;size:128;uniqueIndex:semantic_docs_workspace_id_catalog_id_namespace_table_name__key,priority:3;index:semantic_docs_lookup,priority:3"`
	TableNameValue Text        `gorm:"column:table_name;not null;size:128;uniqueIndex:semantic_docs_workspace_id_catalog_id_namespace_table_name__key,priority:4;index:semantic_docs_lookup,priority:4"`
	ColumnName     *Text       `gorm:"column:column_name;size:128;uniqueIndex:semantic_docs_workspace_id_catalog_id_namespace_table_name__key,priority:5"`
	Description    string      `gorm:"column:description;not null"`
	Tags           StringArray `gorm:"column:tags;not null;size:128;default:'{}';index:semantic_docs_tags,priority:1"`
	OwnerUserID    *UUID       `gorm:"column:owner_user_id"`
	UpdatedBy      UUID        `gorm:"column:updated_by;not null"`
	UpdatedAt      time.Time   `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Certified      bool        `gorm:"column:certified;not null;default:false"`
	CertifiedBy    *UUID       `gorm:"column:certified_by"`
	CertifiedAt    *time.Time  `gorm:"column:certified_at"`
	Workspace      *Workspace  `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	Catalog        *Catalog    `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
	OwnerUser      *User       `gorm:"foreignKey:OwnerUserID;references:ID;constraint:OnDelete:SET NULL"`
	UpdatedByRef   *User       `gorm:"foreignKey:UpdatedBy;references:ID"`
	CertifiedByRef *User       `gorm:"foreignKey:CertifiedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (SemanticDoc) TableName() string { return "semantic_docs" }

// SemanticMetric is the semantic_metrics table.
type SemanticMetric struct {
	ID             UUID            `gorm:"column:id;primaryKey;not null"`
	WorkspaceID    UUID            `gorm:"column:workspace_id;not null;uniqueIndex:semantic_metrics_workspace_id_slug_key,priority:1;index:semantic_metrics_table,priority:1"`
	Slug           Text            `gorm:"column:slug;not null;size:128;uniqueIndex:semantic_metrics_workspace_id_slug_key,priority:2"`
	Label          string          `gorm:"column:label;not null"`
	Description    Text            `gorm:"column:description;not null;size:255;default:''"`
	Certified      bool            `gorm:"column:certified;not null;default:false"`
	CatalogID      UUID            `gorm:"column:catalog_id;not null;index:semantic_metrics_table,priority:2"`
	Namespace      StringArray     `gorm:"column:namespace;not null;size:128;index:semantic_metrics_table,priority:3"`
	TableNameValue Text            `gorm:"column:table_name;not null;size:128;index:semantic_metrics_table,priority:4"`
	BindingID      *UUID           `gorm:"column:binding_id"`
	Definition     JSON            `gorm:"column:definition;not null"`
	FormatVersion  int32           `gorm:"column:format_version;not null;default:1"`
	OwnerUserID    *UUID           `gorm:"column:owner_user_id"`
	UpdatedBy      UUID            `gorm:"column:updated_by;not null"`
	UpdatedAt      time.Time       `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	SpaceID        UUID            `gorm:"column:space_id;not null;index:semantic_metrics_space,priority:1"`
	CertifiedBy    *UUID           `gorm:"column:certified_by"`
	CertifiedAt    *time.Time      `gorm:"column:certified_at"`
	Workspace      *Workspace      `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	Catalog        *Catalog        `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
	Binding        *CatalogBinding `gorm:"foreignKey:BindingID;references:ID;constraint:OnDelete:SET NULL"`
	OwnerUser      *User           `gorm:"foreignKey:OwnerUserID;references:ID;constraint:OnDelete:SET NULL"`
	UpdatedByRef   *User           `gorm:"foreignKey:UpdatedBy;references:ID"`
	CertifiedByRef *User           `gorm:"foreignKey:CertifiedBy;references:ID;constraint:OnDelete:SET NULL"`
	SpaceRef       *Space          `gorm:"foreignKey:WorkspaceID,SpaceID;references:WorkspaceID,ID;constraint:OnDelete:CASCADE"`
}

func (SemanticMetric) TableName() string { return "semantic_metrics" }

// SemanticModel is the semantic_models table.
type SemanticModel struct {
	ID             UUID            `gorm:"column:id;primaryKey;not null"`
	WorkspaceID    UUID            `gorm:"column:workspace_id;not null;uniqueIndex:semantic_models_workspace_id_slug_key,priority:1"`
	SpaceID        UUID            `gorm:"column:space_id;not null;index:semantic_models_space,priority:1"`
	Slug           Text            `gorm:"column:slug;not null;size:128;uniqueIndex:semantic_models_workspace_id_slug_key,priority:2"`
	Label          string          `gorm:"column:label;not null"`
	Description    Text            `gorm:"column:description;not null;size:255;default:''"`
	Certified      bool            `gorm:"column:certified;not null;default:false"`
	CertifiedBy    *UUID           `gorm:"column:certified_by"`
	CertifiedAt    *time.Time      `gorm:"column:certified_at"`
	CatalogID      UUID            `gorm:"column:catalog_id;not null;index:semantic_models_catalog,priority:1"`
	BindingID      *UUID           `gorm:"column:binding_id"`
	Definition     JSON            `gorm:"column:definition;not null"`
	FormatVersion  int32           `gorm:"column:format_version;not null;default:1"`
	OwnerUserID    *UUID           `gorm:"column:owner_user_id"`
	UpdatedBy      UUID            `gorm:"column:updated_by;not null"`
	UpdatedAt      time.Time       `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Workspace      *Workspace      `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	CertifiedByRef *User           `gorm:"foreignKey:CertifiedBy;references:ID;constraint:OnDelete:SET NULL"`
	Catalog        *Catalog        `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
	Binding        *CatalogBinding `gorm:"foreignKey:BindingID;references:ID;constraint:OnDelete:SET NULL"`
	OwnerUser      *User           `gorm:"foreignKey:OwnerUserID;references:ID;constraint:OnDelete:SET NULL"`
	UpdatedByRef   *User           `gorm:"foreignKey:UpdatedBy;references:ID"`
	SpaceRef       *Space          `gorm:"foreignKey:WorkspaceID,SpaceID;references:WorkspaceID,ID;constraint:OnDelete:CASCADE"`
}

func (SemanticModel) TableName() string { return "semantic_models" }

// MetadataNamespace is the metadata_namespaces table.
type MetadataNamespace struct {
	CatalogID  UUID        `gorm:"column:catalog_id;primaryKey;not null"`
	Namespace  StringArray `gorm:"column:namespace;primaryKey;not null;size:128"`
	LastSeenAt time.Time   `gorm:"column:last_seen_at;not null"`
	RemovedAt  *time.Time  `gorm:"column:removed_at"`
	Catalog    *Catalog    `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
}

func (MetadataNamespace) TableName() string { return "metadata_namespaces" }

// MetadataTable is the metadata_tables table.
type MetadataTable struct {
	ID            UUID        `gorm:"column:id;primaryKey;not null;check:metadata_tables_kind_check,((kind IN ('table', 'view', 'materialized_view')))"`
	CatalogID     UUID        `gorm:"column:catalog_id;not null;uniqueIndex:metadata_tables_catalog_id_namespace_name_key,priority:1"`
	Namespace     StringArray `gorm:"column:namespace;not null;size:128;uniqueIndex:metadata_tables_catalog_id_namespace_name_key,priority:2"`
	Name          Text        `gorm:"column:name;not null;size:128;uniqueIndex:metadata_tables_catalog_id_namespace_name_key,priority:3;index:metadata_tables_name_trgm,priority:1,where:removed_at IS NULL"`
	Kind          string      `gorm:"column:kind;not null"`
	Fingerprint   string      `gorm:"column:fingerprint;not null"`
	Properties    JSON        `gorm:"column:properties;not null;default:('{}')"`
	Comment       *string     `gorm:"column:comment"`
	LastSeenAt    time.Time   `gorm:"column:last_seen_at;not null"`
	LastChangedAt time.Time   `gorm:"column:last_changed_at;not null"`
	RemovedAt     *time.Time  `gorm:"column:removed_at"`
	Catalog       *Catalog    `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
}

func (MetadataTable) TableName() string { return "metadata_tables" }

// MetadataColumn is the metadata_columns table.
type MetadataColumn struct {
	TableID    UUID           `gorm:"column:table_id;primaryKey;not null"`
	Ordinal    int32          `gorm:"column:ordinal;primaryKey;not null"`
	Name       Text           `gorm:"column:name;not null;size:128;index:metadata_columns_name_trgm,priority:1"`
	NativeType string         `gorm:"column:native_type;not null"`
	TypeSystem string         `gorm:"column:type_system;not null"`
	Nullable   *bool          `gorm:"column:nullable"`
	Comment    *string        `gorm:"column:comment"`
	Table      *MetadataTable `gorm:"foreignKey:TableID;references:ID;constraint:OnDelete:CASCADE"`
}

func (MetadataColumn) TableName() string { return "metadata_columns" }

// MetadataForeignKey is the metadata_foreign_keys table.
type MetadataForeignKey struct {
	CatalogID    UUID           `gorm:"column:catalog_id;not null;index:metadata_foreign_keys_catalog,priority:1;index:metadata_foreign_keys_ref,priority:1"`
	TableID      UUID           `gorm:"column:table_id;primaryKey;not null"`
	ColumnName   Text           `gorm:"column:column_name;primaryKey;not null;size:128"`
	RefNamespace StringArray    `gorm:"column:ref_namespace;primaryKey;not null;size:128;index:metadata_foreign_keys_ref,priority:2"`
	RefTable     Text           `gorm:"column:ref_table;primaryKey;not null;size:128;index:metadata_foreign_keys_ref,priority:3"`
	RefColumn    Text           `gorm:"column:ref_column;primaryKey;not null;size:128"`
	LastSeenAt   time.Time      `gorm:"column:last_seen_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Catalog      *Catalog       `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
	Table        *MetadataTable `gorm:"foreignKey:TableID;references:ID;constraint:OnDelete:CASCADE"`
}

func (MetadataForeignKey) TableName() string { return "metadata_foreign_keys" }

// MetadataSyncRun is the metadata_sync_runs table.
type MetadataSyncRun struct {
	ID            UUID       `gorm:"column:id;primaryKey;not null;check:metadata_sync_runs_status_check,((status IN ('running', 'finished', 'failed')))"`
	CatalogID     UUID       `gorm:"column:catalog_id;not null;index:metadata_sync_runs_catalog,priority:1;check:metadata_sync_runs_source_path_check,((source_path IN ('provider', 'engine')))"`
	StartedAt     time.Time  `gorm:"column:started_at;not null;default:CURRENT_TIMESTAMP(3);index:metadata_sync_runs_catalog,priority:2"`
	FinishedAt    *time.Time `gorm:"column:finished_at"`
	HeartbeatAt   *time.Time `gorm:"column:heartbeat_at"`
	Status        string     `gorm:"column:status;not null"`
	TablesSeen    *int32     `gorm:"column:tables_seen"`
	TablesChanged *int32     `gorm:"column:tables_changed"`
	TablesRemoved *int32     `gorm:"column:tables_removed"`
	ErrorCode     *string    `gorm:"column:error_code"`
	ErrorMessage  *string    `gorm:"column:error_message"`
	SourcePath    string     `gorm:"column:source_path;not null"`
	Catalog       *Catalog   `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
}

func (MetadataSyncRun) TableName() string { return "metadata_sync_runs" }

// TablePopularity is the table_popularity table.
type TablePopularity struct {
	WorkspaceID    UUID       `gorm:"column:workspace_id;primaryKey;not null;index:table_popularity_workspace,priority:1"`
	CatalogID      UUID       `gorm:"column:catalog_id;primaryKey;not null"`
	Namespace      Text       `gorm:"column:namespace;primaryKey;not null;size:128;default:''"`
	TableNameValue Text       `gorm:"column:table_name;primaryKey;not null;size:128"`
	Uses           int64      `gorm:"column:uses;not null;default:0"`
	Score          float64    `gorm:"column:score;not null;type:double precision;default:0;index:table_popularity_workspace,priority:2"`
	LastUsedAt     time.Time  `gorm:"column:last_used_at;not null"`
	ComputedAt     time.Time  `gorm:"column:computed_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Workspace      *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	Catalog        *Catalog   `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
}

func (TablePopularity) TableName() string { return "table_popularity" }
