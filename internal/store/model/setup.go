package model

// SetupLock is the setup_locks table: one stable row per one-time
// initialization task. ADR 0020 forbids advisory locks, so the first-admin
// setup serializes on a row lock of this table instead.
type SetupLock struct {
	Name string `gorm:"column:name;primaryKey;size:64;not null"`
}

func (SetupLock) TableName() string { return "setup_locks" }
