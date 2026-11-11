package store

import (
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
)

func TestCompareDigests(t *testing.T) {
	t.Parallel()
	a := map[string]dayDigest{"2026-11-01": {count: 2, sum: "aa"}, "2026-11-02": {count: 1, sum: "bb"}}
	for _, tc := range []struct {
		name string
		got  map[string]dayDigest
		want string // substring of the error; empty means no error
	}{
		{"equal", map[string]dayDigest{"2026-11-01": {count: 2, sum: "aa"}, "2026-11-02": {count: 1, sum: "bb"}}, ""},
		{"lost row", map[string]dayDigest{"2026-11-01": {count: 1, sum: "aa"}, "2026-11-02": {count: 1, sum: "bb"}}, "day 2026-11-01 has 1 rows"},
		{"lost day", map[string]dayDigest{"2026-11-01": {count: 2, sum: "aa"}}, "day 2026-11-02 has 0 rows"},
		{"extra day", map[string]dayDigest{"2026-11-01": {count: 2, sum: "aa"}, "2026-11-02": {count: 1, sum: "bb"}, "2026-11-03": {count: 1, sum: "cc"}}, "day 2026-11-03 has 1 rows"},
		{"rewritten id", map[string]dayDigest{"2026-11-01": {count: 2, sum: "aa"}, "2026-11-02": {count: 1, sum: "xx"}}, "day 2026-11-02 has different ids"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := compareDigests("audit_events", a, tc.got)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestValidateCopy compares a table with a copy of itself, then breaks the copy
// in the three ways a bad migration could: a lost row, a duplicate day shift and
// a rewritten id.
func TestValidateCopy(t *testing.T) {
	t.Parallel()
	db := newPostgresStore(t)
	if err := AutoMigrate(db, &model.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	days := []time.Time{
		time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 23, 59, 59, 0, time.UTC),
		time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
	}
	for _, at := range days {
		mustInsert(t, db, "audit_events", map[string]any{
			"occurred_at": at, "actor_kind": "user", "action": "x", "resource_type": "r", "outcome": "allowed",
		})
	}
	// audit_events stands for the set-aside table and audit_events_check for the
	// partitioned copy that is validated against it.
	tbl := partitionedTable{spec: rangeSpec("audit_events_check", "occurred_at")}
	reset := func() {
		t.Helper()
		if err := db.Exec(`DROP TABLE IF EXISTS audit_events_check`).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`CREATE TABLE audit_events_check AS TABLE audit_events`).Error; err != nil {
			t.Fatal(err)
		}
	}

	reset()
	if err := validateCopy(db, tbl, "audit_events"); err != nil {
		t.Fatalf("an exact copy failed validation: %v", err)
	}
	for _, tc := range []struct {
		name, stmt, want string
	}{
		{"lost row", `DELETE FROM audit_events_check WHERE id = 2`, "day 2026-11-01 has 1 rows after the copy, want 2"},
		{"rewritten id", `UPDATE audit_events_check SET id = 99 WHERE id = 3`, "day 2026-11-02 has different ids"},
		{"moved row", `UPDATE audit_events_check SET occurred_at = occurred_at + interval '1 day' WHERE id = 1`, "has 1 rows"},
	} {
		reset()
		if err := db.Exec(tc.stmt).Error; err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		err := validateCopy(db, tbl, "audit_events")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}
