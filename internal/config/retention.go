package config

import (
	"fmt"
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
	return cadence, nil
}

// RetentionPeriod is the longest gap between two runs of a cadence, which is
// what a stale-retention alert has to allow for. For a cron expression it is
// measured over a year of firings from a fixed date, so an uneven expression
// (the first of the month, for instance) is covered by its longest gap.
func RetentionPeriod(cadence string) (time.Duration, error) {
	expr, err := RetentionCron(cadence)
	if err != nil {
		return 0, err
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return 0, err
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := at.AddDate(1, 0, 0)
	var longest time.Duration
	for prev := sched.Next(at); !prev.IsZero() && prev.Before(end); {
		next := sched.Next(prev)
		if next.IsZero() {
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
