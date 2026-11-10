package model

import "time"

// BIGatewaySetting is the singleton global BI gateway switch (ADR 0017). A
// global admin can turn the gateway off at runtime; the listener stays up and
// rejects every request until it is turned back on.
type BIGatewaySetting struct {
	ID        bool      `gorm:"column:id;primaryKey;not null;default:true;check:bi_gateway_settings_id_check,((id = TRUE))"`
	Enabled   bool      `gorm:"column:enabled;not null;default:true"`
	UpdatedBy *UUID     `gorm:"column:updated_by"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
}

func (BIGatewaySetting) TableName() string { return "bi_gateway_settings" }
