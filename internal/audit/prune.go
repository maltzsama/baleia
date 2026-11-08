package audit

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	"gorm.io/gorm"
)

var partitionName = regexp.MustCompile(`^audit_events_(\d{6})$`)

// Prune drops the audit partitions of the months before before (YYYY-MM),
// oldest first, and returns their names. It is the operator's explicit
// retention command (`migrate -cmd=audit-prune`); the row trigger does not
// block dropping a partition, by design. The current month and later are
// never pruned. Partition discovery and removal go through GORM's Migrator,
// so the command authors no SQL (ADR 0020).
func Prune(ctx context.Context, db *gorm.DB, before string, now time.Time) ([]string, error) {
	cut, err := time.Parse("2006-01", before)
	if err != nil {
		return nil, fmt.Errorf("-before must look like 2026-01: %w", err)
	}
	if !cut.Before(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)) {
		return nil, fmt.Errorf("refusing to prune the current month or later (before=%s)", before)
	}
	tables, err := db.WithContext(ctx).Migrator().GetTables()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, name := range tables {
		if m := partitionName.FindStringSubmatch(name); m != nil && m[1] < cut.Format("200601") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for i, name := range names {
		// Names come from the migrator and match partitionName: safe to drop.
		if err := db.WithContext(ctx).Table(name).Migrator().DropTable(name); err != nil {
			return names[:i], err
		}
	}
	return names, nil
}
