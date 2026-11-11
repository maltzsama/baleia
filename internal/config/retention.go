package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron"
)

// Retention defaults (ADR 0026): both streams keep 45 days hot, a day of grace
// past the window, and drop at most two weeks of daily partitions per run.
const (
	DefaultRetention          = 45 * 24 * time.Hour
	DefaultColdGrace          = 24 * time.Hour
	DefaultPruneMaxPartitions = 14
	// MinRetention is the shortest window accepted: today's partition and later
	// ones are never dropped, and a partition is a day wide.
	MinRetention = 24 * time.Hour
)

// Retention cadences: how often a stream's retention schedule runs.
const (
	// CadenceDaily runs at 03:00 UTC every day.
	CadenceDaily = "daily"
	// CadenceWeekly runs at 03:00 UTC on Sunday. It is the default.
	CadenceWeekly = "weekly"
)

// RetentionCron returns the five-field cron expression, read in UTC, that a
// cadence stands for. Anything but "daily" and "weekly" has to be a five-field
// cron expression itself.
func RetentionCron(cadence string) (string, error) {
	switch cadence {
	case "", CadenceWeekly:
		return "0 3 * * 0", nil
	case CadenceDaily:
		return "0 3 * * *", nil
	}
	if len(strings.Fields(cadence)) != 5 {
		return "", fmt.Errorf("must be daily, weekly or a five-field cron expression (got %q)", cadence)
	}
	if _, err := cron.ParseStandard(cadence); err != nil {
		return "", fmt.Errorf("is not a valid cron expression (%q): %w", cadence, err)
	}
	// robfig/cron fires when either day field matches if both are restricted,
	// and Temporal, which runs the schedule, needs both to match. The two read
	// the same expression as different dates (or, for "0 3 31 2 1", as none at
	// all), so the longest gap computed here would not be the real one.
	if f := strings.Fields(cadence); restricted(f[2]) && restricted(f[4]) {
		return "", fmt.Errorf("must not restrict both the day of month and the day of week (got %q)", cadence)
	}
	return cadence, nil
}

// restricted reports whether a cron field names particular values: one that
// starts with * or ? matches every value or every Nth.
func restricted(field string) bool {
	return !strings.HasPrefix(field, "*") && !strings.HasPrefix(field, "?")
}

// maxFirings bounds the walk over a dense cadence: a cron that fires this often
// has no long gap worth finding.
const maxFirings = 200_000

// RetentionPeriod is the longest gap between two runs of a cadence, which is
// what a stale-retention alert has to allow for. For a cron expression it is
// measured over two years of firings, starting a second before the first
// instant so a firing on it counts, so an uneven expression (the first of the
// month, January and December) is covered by its longest gap, wrap included.
func RetentionPeriod(cadence string) (time.Duration, error) {
	expr, err := RetentionCron(cadence)
	if err != nil {
		return 0, err
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return 0, err
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)
	end := start.AddDate(2, 0, 0)
	var longest time.Duration
	prev := sched.Next(start)
	for i := 0; i < maxFirings && !prev.IsZero() && prev.Before(end); i++ {
		next := sched.Next(prev)
		if next.IsZero() || !next.Before(end) {
			break
		}
		if gap := next.Sub(prev); gap > longest {
			longest = gap
		}
		prev = next
	}
	if longest == 0 {
		return 0, fmt.Errorf("cadence %q fires at most once a year", cadence)
	}
	return longest, nil
}

// parseRetention reads a duration that may be written in days ("45d"), which
// time.ParseDuration does not accept, or in any unit it does ("1080h").
func parseRetention(v string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("not a number of days: %q", v)
		}
		// A duration holds about 292 years: more would wrap into a short window
		// that passes the minimum and deletes data early.
		const maxDays = math.MaxInt64 / int64(24*time.Hour)
		if int64(n) > maxDays || int64(n) < -maxDays {
			return 0, fmt.Errorf("too many days: %q", v)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(v)
}

// settings collects what Load could not read, so Validate can refuse to start
// on it: for a setting that decides when data is deleted, falling back to a
// default on a typo would be worse than stopping.
type settings struct{ problems []string }

func (s *settings) duration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := parseRetention(v)
	if err != nil {
		s.problems = append(s.problems, fmt.Sprintf("%s must be a duration such as 45d or 1080h (got %q)", key, v))
		return fallback
	}
	return d
}

// retention is a duration that has to be a window: at least a day. An explicit
// 0 is refused too, because it would otherwise read as "unset" and become the
// default without a word.
func (s *settings) retention(key string, fallback time.Duration) time.Duration {
	d := s.duration(key, fallback)
	if os.Getenv(key) != "" && d < MinRetention {
		s.problems = append(s.problems, fmt.Sprintf("%s must be at least 1d (got %q)", key, os.Getenv(key)))
		return fallback
	}
	return d
}

// positiveDuration is a duration that has to be above zero when it is set: an
// explicit 0 would otherwise read as "unset" and become the default unannounced.
func (s *settings) positiveDuration(key string, fallback time.Duration) time.Duration {
	d := s.duration(key, fallback)
	if v := os.Getenv(key); v != "" && d <= 0 {
		s.problems = append(s.problems, fmt.Sprintf("%s must be greater than zero (got %q)", key, v))
		return fallback
	}
	return d
}

// positiveInt is a whole number of at least 1 when it is set; anything else, an
// unreadable value or 0 or less, stops the worker instead of becoming the default.
func (s *settings) positiveInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		s.problems = append(s.problems, fmt.Sprintf("%s must be at least 1 (got %q)", key, v))
		return fallback
	}
	return n
}

// boolean reads true or false and nothing else.
func (s *settings) boolean(key string, fallback bool) bool {
	switch v := os.Getenv(key); v {
	case "":
		return fallback
	case "true":
		return true
	case "false":
		return false
	default:
		s.problems = append(s.problems, fmt.Sprintf("%s must be true or false (got %q)", key, v))
		return fallback
	}
}

// AuditRetentionWindow is the hot window of the audit trail: 45 days unless set.
func (c *Config) AuditRetentionWindow() time.Duration {
	if c.AuditRetention <= 0 {
		return DefaultRetention
	}
	return c.AuditRetention
}

// QueryHistoryRetentionWindow is the hot window of the query history.
func (c *Config) QueryHistoryRetentionWindow() time.Duration {
	if c.QueryHistoryRetention <= 0 {
		return DefaultRetention
	}
	return c.QueryHistoryRetention
}

// AuditGrace is the margin past the window before an audit day is droppable.
func (c *Config) AuditGrace() time.Duration {
	if c.AuditColdGrace <= 0 {
		return DefaultColdGrace
	}
	return c.AuditColdGrace
}

// QueryHistoryGrace is the same margin for the query history.
func (c *Config) QueryHistoryGrace() time.Duration {
	if c.QueryHistoryColdGrace <= 0 {
		return DefaultColdGrace
	}
	return c.QueryHistoryColdGrace
}

// PruneCap is the number of daily partitions one retention run may drop, per
// stream: 14 unless set.
func (c *Config) PruneCap() int {
	if c.PruneMaxPartitions <= 0 {
		return DefaultPruneMaxPartitions
	}
	return c.PruneMaxPartitions
}

// validateRetention checks the retention settings: a window of at least a day,
// cadences that parse, and anything Load could not read.
func (c *Config) validateRetention() error {
	for _, w := range []struct {
		key string
		d   time.Duration
	}{
		{"BALEIA_AUDIT_RETENTION", c.AuditRetention},
		{"BALEIA_QUERY_HISTORY_RETENTION", c.QueryHistoryRetention},
	} {
		if w.d < 0 || (w.d != 0 && w.d < MinRetention) {
			return fmt.Errorf("%s must be at least 1d (got %s)", w.key, w.d)
		}
	}
	for _, g := range []struct {
		key string
		d   time.Duration
	}{
		{"BALEIA_AUDIT_COLD_GRACE", c.AuditColdGrace},
		{"BALEIA_QUERY_HISTORY_COLD_GRACE", c.QueryHistoryColdGrace},
	} {
		if g.d < 0 {
			return fmt.Errorf("%s must not be negative (got %s)", g.key, g.d)
		}
	}
	if c.PruneMaxPartitions < 0 {
		return fmt.Errorf("BALEIA_PRUNE_MAX_PARTITIONS must be at least 1 (got %d)", c.PruneMaxPartitions)
	}
	for _, cad := range []struct{ key, value string }{
		{"BALEIA_AUDIT_RETENTION_CADENCE", c.AuditRetentionCadence},
		{"BALEIA_QUERY_HISTORY_RETENTION_CADENCE", c.QueryHistoryRetentionCadence},
	} {
		if _, err := RetentionPeriod(cad.value); err != nil {
			return fmt.Errorf("%s %w", cad.key, err)
		}
	}
	if len(c.problems) > 0 {
		return fmt.Errorf("%s", c.problems[0])
	}
	return nil
}
