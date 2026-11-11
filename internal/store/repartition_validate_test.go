package store

import (
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
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

// digestSource and digestCopy are two plain tables with the shape validateCopy
// reads (an id and the partition column): the set-aside table and the copy.
type digestSource struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement:false"`
	OccurredAt time.Time `gorm:"column:occurred_at;not null"`
}

func (digestSource) TableName() string { return "digest_source" }

type digestCopy struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement:false"`
	OccurredAt time.Time `gorm:"column:occurred_at;not null"`
}

func (digestCopy) TableName() string { return "digest_copy" }

// TestValidateCopy compares a table with a copy of itself, then breaks the copy
// in the three ways a bad migration could: a lost row, a rewritten id and a row
// moved to another day. Everything goes through the ORM.
func TestValidateCopy(t *testing.T) {
	t.Parallel()
	db := newPostgresStore(t)
	if err := AutoMigrate(db, &digestSource{}, &digestCopy{}); err != nil {
		t.Fatal(err)
	}
	at := []time.Time{
		time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 23, 59, 59, 0, time.UTC),
		time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
	}
	for i, when := range at {
		if err := db.Create(&digestSource{ID: int64(i + 1), OccurredAt: when}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// digest_source stands for the set-aside table and digest_copy for the
	// partitioned copy that is validated against it.
	tbl := partitionedTable{spec: rangeSpec("digest_copy", "occurred_at")}
	reset := func() {
		t.Helper()
		if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&digestCopy{}).Error; err != nil {
			t.Fatal(err)
		}
		for i, when := range at {
			if err := db.Create(&digestCopy{ID: int64(i + 1), OccurredAt: when}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	reset()
	if err := validateCopy(db, tbl, "digest_source"); err != nil {
		t.Fatalf("an exact copy failed validation: %v", err)
	}
	for _, tc := range []struct {
		name   string
		damage func() error
		want   string
	}{
		{"lost row", func() error { return db.Delete(&digestCopy{}, 2).Error }, "day 2026-11-01 has 1 rows after the copy, want 2"},
		{"rewritten id", func() error { return db.Model(&digestCopy{}).Where("id = ?", 3).Update("id", 99).Error }, "day 2026-11-02 has different ids"},
		{"moved row", func() error {
			return db.Model(&digestCopy{}).Where("id = ?", 1).Update("occurred_at", at[0].AddDate(0, 0, 1)).Error
		}, "has 1 rows"},
	} {
		reset()
		if err := tc.damage(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		err := validateCopy(db, tbl, "digest_source")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}
