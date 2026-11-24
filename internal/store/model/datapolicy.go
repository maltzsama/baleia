package model

import "time"

// DataPolicy is one row filter or column mask as authored (ADR 0028). The row
// is the editable draft; what is published lives in DataPolicyRevision.
type DataPolicy struct {
	ID             UUID   `gorm:"column:id;primaryKey;not null"`
	CatalogID      UUID   `gorm:"column:catalog_id;not null;uniqueIndex:data_policies_target_name_key,priority:1;index:data_policies_catalog,priority:1;check:data_policies_kind_check,((kind IN ('row_filter', 'column_mask')))"`
	SchemaName     Text   `gorm:"column:schema_name;not null;size:255;uniqueIndex:data_policies_target_name_key,priority:2;check:data_policies_mode_check,(((kind = 'row_filter') AND (mode IN ('permissive', 'restrictive'))) OR ((kind = 'column_mask') AND (mode = '')))"`
	TableNameValue Text   `gorm:"column:table_name;not null;size:255;uniqueIndex:data_policies_target_name_key,priority:3;check:data_policies_column_check,(((kind = 'row_filter') AND (column_name = '')) OR ((kind = 'column_mask') AND (column_name <> '')))"`
	Name           Text   `gorm:"column:name;not null;size:64;uniqueIndex:data_policies_target_name_key,priority:4;check:data_policies_state_check,((state IN ('enabled', 'disabled')))"`
	Kind           Text   `gorm:"column:kind;not null;size:16;check:data_policies_validation_check,((validation IN ('pending', 'valid', 'invalid')))"`
	ColumnName     Text   `gorm:"column:column_name;not null;size:255;default:'';check:data_policies_template_check,((template IN ('', 'null', 'constant', 'partial', 'sha256', 'custom')))"`
	Mode           Text   `gorm:"column:mode;not null;size:16;default:''"`
	Priority       int32  `gorm:"column:priority;not null;default:0"`
	Subjects       JSON   `gorm:"column:subjects;not null"`
	Expression     string `gorm:"column:expression;not null"`
	Template       Text   `gorm:"column:template;not null;size:16;default:''"`
	State          Text   `gorm:"column:state;not null;size:16;default:'enabled'"`
	Description    string `gorm:"column:description;not null"`
	// Compiled is the checked canonical expression; empty unless Validation
	// is valid.
	Compiled      string    `gorm:"column:compiled;not null"`
	Validation    Text      `gorm:"column:validation;not null;size:16;default:'pending'"`
	InvalidReason Text      `gorm:"column:invalid_reason;not null;size:64;default:''"`
	InvalidDetail Text      `gorm:"column:invalid_detail;not null;size:255;default:''"`
	Version       int64     `gorm:"column:version;not null;default:1"`
	CreatedBy     UUID      `gorm:"column:created_by;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Catalog       *Catalog  `gorm:"foreignKey:CatalogID;references:ID;constraint:OnDelete:CASCADE"`
	CreatedByRef  *User     `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:RESTRICT"`
}

// TableName is the table of DataPolicy.
func (DataPolicy) TableName() string { return "data_policies" }

// DataPolicyRevision is one published state of every data policy, built once
// and never rewritten (ADR 0028). Re-issuing a revision with a fresh lease, or
// rolling back to an earlier one, creates a new revision whose SourceID names
// the revision it copies.
type DataPolicyRevision struct {
	ID     UUID  `gorm:"column:id;primaryKey;not null"`
	Number int64 `gorm:"column:number;not null;uniqueIndex:data_policy_revisions_number_key;check:data_policy_revisions_reason_check,((reason IN ('publish', 'reissue', 'rollback', 'subjects')))"`
	Reason Text  `gorm:"column:reason;not null;size:16"`
	// SourceID is the revision whose content this one re-issues.
	SourceID *UUID `gorm:"column:source_id"`
	// Content is the published policies, the input of the bundle's tables:
	// JSON, stored as text so its bytes, and therefore its digest, survive
	// the round trip (jsonb would normalize them).
	Content string `gorm:"column:content;not null"`
	// Digest is the SHA-256 of Content, hex.
	Digest       Text                `gorm:"column:digest;not null;size:64"`
	NotAfter     time.Time           `gorm:"column:not_after;not null"`
	CreatedBy    *UUID               `gorm:"column:created_by"`
	CreatedAt    time.Time           `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Source       *DataPolicyRevision `gorm:"foreignKey:SourceID;references:ID;constraint:OnDelete:RESTRICT"`
	CreatedByRef *User               `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:SET NULL"`
}

// TableName is the table of DataPolicyRevision.
func (DataPolicyRevision) TableName() string { return "data_policy_revisions" }
