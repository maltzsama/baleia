package audit

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/migrator"
)

var partitionName = regexp.MustCompile(`^audit_events_(\d{6})$`)

// Prune drops the audit partitions of the months before before (YYYY-MM),
// oldest first, and returns their names. It is the operator's explicit
// retention command (`migrate -cmd=audit-prune`); the row trigger does not
// block dropping a partition, by design. The current month and later are
// never pruned. Partition discovery and removal go through GORM's Migrator,
// so the command authors no SQL (ADR 0020). The drop is non-cascading, so a
// dependent object makes it fail instead of being silently removed.
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
	// The dialect migrators hardcode DROP TABLE ... CASCADE, which would
	// remove a dependent view. GORM's generic migrator drops the same table
	// with the default RESTRICT, so a dependent object makes the command fail
	// and stay intact.
	drop := migrator.Migrator{Config: migrator.Config{DB: db.WithContext(ctx), Dialector: db.Dialector}}
	for i, name := range names {
		// Names come from the migrator and match partitionName: safe to drop.
		if err := drop.DropTable(name); err != nil {
			return names[:i], err
		}
	}
	return names, nil
}
