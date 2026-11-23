package model

import "time"

// OpenLineageDestination stores a workspace's outbound event destination.
type OpenLineageDestination struct {
	WorkspaceID  UUID       `gorm:"column:workspace_id;primaryKey;not null"`
	Endpoint     string     `gorm:"column:endpoint;not null"`
	Namespace    string     `gorm:"column:namespace;not null"`
	TokenEnc     string     `gorm:"column:token_enc;not null"`
	UpdatedBy    *UUID      `gorm:"column:updated_by"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	Workspace    *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	UpdatedByRef *User      `gorm:"foreignKey:UpdatedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (OpenLineageDestination) TableName() string { return "openlineage_destinations" }
