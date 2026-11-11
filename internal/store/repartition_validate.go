package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// validateBatch is the number of rows read per round trip when a copy is
// validated.
const validateBatch = 5000

// dayDigest summarizes the rows of one UTC day: how many there are and a
// checksum over their ids in ascending order.
type dayDigest struct {
	count int64
	sum   string
}

// validateCopy compares the set-aside table with the partitioned table that
// replaced it, day by day (row count and a checksum over the ids), so a copy
// that lost, duplicated or rewrote a row never lets the old table be dropped
// (ADR 0026). It reads through the query builder and keeps one running hash per
// day, so the memory it needs does not grow with the history.
func validateCopy(db *gorm.DB, t partitionedTable, legacy string) error {
	want, err := digestByDay(db, legacy, t.spec.Column)
	if err != nil {
		return fmt.Errorf("validate %s: read %s: %w", t.spec.Table, legacy, err)
	}
	got, err := digestByDay(db, t.spec.Table, t.spec.Column)
	if err != nil {
		return fmt.Errorf("validate %s: read the copy: %w", t.spec.Table, err)
	}
	return compareDigests(t.spec.Table, want, got)
}

// compareDigests reports the first day, in order, on which the copy differs.
func compareDigests(table string, want, got map[string]dayDigest) error {
	days := make([]string, 0, len(want)+len(got))
	for d := range want {
		days = append(days, d)
	}
	for d := range got {
		if _, ok := want[d]; !ok {
			days = append(days, d)
		}
	}
	sort.Strings(days)
	for _, d := range days {
		w, g := want[d], got[d]
		switch {
		case w.count != g.count:
			return fmt.Errorf("validate %s: day %s has %d rows after the copy, want %d", table, d, g.count, w.count)
		case w.sum != g.sum:
			return fmt.Errorf("validate %s: day %s has different ids after the copy", table, d)
		}
	}
	return nil
}

// digestByDay reads every row of table in id order, in keyset batches, and
// digests the ids by the UTC day of tsColumn.
func digestByDay(db *gorm.DB, table, tsColumn string) (map[string]dayDigest, error) {
	counts := map[string]int64{}
	hashes := map[string]hash.Hash{}
	var last any
	for {
		q := db.Session(&gorm.Session{NewDB: true}).Table(table).Select("id", tsColumn).Order("id").Limit(validateBatch)
		if last != nil {
			q = q.Where("id > ?", last)
		}
		n, err := scanBatch(q, func(id any, at time.Time) {
			day := at.UTC().Format("2006-01-02")
			h, ok := hashes[day]
			if !ok {
				h = sha256.New()
				hashes[day] = h
			}
			_, _ = h.Write([]byte(keyString(id)))
			_, _ = h.Write([]byte{0})
			counts[day]++
			last = id
		})
		if err != nil {
			return nil, err
		}
		if n < validateBatch {
			break
		}
	}
	out := make(map[string]dayDigest, len(counts))
	for day, n := range counts {
		out[day] = dayDigest{count: n, sum: hex.EncodeToString(hashes[day].Sum(nil))}
	}
	return out, nil
}

// scanBatch reads one batch and closes its cursor before returning, so no
// cursor is open while the caller goes on.
func scanBatch(q *gorm.DB, visit func(id any, at time.Time)) (int, error) {
	rows, err := q.Rows()
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var id any
		var at sql.NullTime
		if err := rows.Scan(&id, &at); err != nil {
			return n, err
		}
		if raw, ok := id.([]byte); ok {
			id = string(raw)
		}
		visit(id, at.Time)
		n++
	}
	return n, rows.Err()
}

// keyString renders a scanned id (an integer or a UUID) for hashing.
func keyString(id any) string {
	switch v := id.(type) {
	case []byte:
		return string(v)
	case string:
		return v
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
