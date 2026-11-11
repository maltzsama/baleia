package store

import (
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// legacyCursor reads audit_export_cursor before it gains the stream column.
type legacyCursor struct {
	Sink       string    `gorm:"column:sink"`
	OccurredAt time.Time `gorm:"column:occurred_at"`
	ID         int64     `gorm:"column:id"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

func (legacyCursor) TableName() string { return "audit_export_cursor" }

// keyExportCursor re-creates audit_export_cursor keyed by (stream, sink),
// backfilling the one existing cursor (OpenSearch) to the audit stream. The
// table is tiny, so dropping and re-creating it is cheaper than swapping its
// primary key. The steps run one at a time, not in a transaction: MySQL and
// MariaDB commit DDL implicitly and GORM's DropTable needs its own connection,
// so a transaction around it deadlocks there. A crash between the drop and the
// backfill only loses the export position, which re-exports idempotently.
func keyExportCursor(db *gorm.DB) error {
	var legacy []legacyCursor
	if err := db.Find(&legacy).Error; err != nil {
		return err
	}
	if err := db.Migrator().DropTable(&legacyCursor{}); err != nil {
		return err
	}
	if err := AutoMigrate(db, &model.AuditExportCursor{}); err != nil {
		return err
	}
	for _, old := range legacy {
		row := model.AuditExportCursor{
			// The only pre-stream sink is OpenSearch, which exports the
			// audit trail.
			Stream:     "audit",
			Sink:       model.Text(old.Sink),
			OccurredAt: old.OccurredAt,
			ID:         old.ID,
			UpdatedAt:  old.UpdatedAt,
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}
