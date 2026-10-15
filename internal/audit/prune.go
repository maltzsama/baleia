package audit

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"time"
)

var partitionName = regexp.MustCompile(`^audit_events_(\d{6})$`)

// Prune detaches and drops the audit partitions of the months before
// before (YYYY-MM), oldest first, and returns their names. It is the
// operator's explicit retention command (`migrate -cmd=audit-prune`); the
// row trigger does not block dropping a partition, by design. The current
// month and later are never pruned.
func Prune(ctx context.Context, db *sql.DB, before string, now time.Time) ([]string, error) {
	cut, err := time.Parse("2006-01", before)
	if err != nil {
		return nil, fmt.Errorf("-before must look like 2026-01: %w", err)
	}
	if !cut.Before(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)) {
		return nil, fmt.Errorf("refusing to prune the current month or later (before=%s)", before)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = 'audit_events'::regclass`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		if m := partitionName.FindStringSubmatch(n); m != nil && m[1] < cut.Format("200601") {
			names = append(names, n)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(names)
	for i, n := range names {
		// Names come from pg_class and match partitionName: safe to splice.
		if _, err := db.ExecContext(ctx, `ALTER TABLE audit_events DETACH PARTITION `+n); err != nil {
			return names[:i], err
		}
		if _, err := db.ExecContext(ctx, `DROP TABLE `+n); err != nil {
			return names[:i], err
		}
	}
	return names, nil
}
