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
// primary key, and PostgreSQL makes the transition transactional.
func keyExportCursor(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var legacy []legacyCursor
		if err := tx.Find(&legacy).Error; err != nil {
			return err
		}
		if err := tx.Migrator().DropTable(&legacyCursor{}); err != nil {
			return err
		}
		if err := AutoMigrate(tx, &model.AuditExportCursor{}); err != nil {
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
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
