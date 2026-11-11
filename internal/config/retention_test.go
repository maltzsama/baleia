package config

import (
	"strings"
	"testing"
	"time"
)

// retentionEnv is every variable the retention settings read, cleared so a test
// starts from the defaults.
var retentionEnv = []string{
	"BALEIA_AUDIT_RETENTION", "BALEIA_QUERY_HISTORY_RETENTION",
	"BALEIA_AUDIT_COLD_GRACE", "BALEIA_QUERY_HISTORY_COLD_GRACE",
	"BALEIA_PRUNE_MAX_PARTITIONS",
	"BALEIA_AUDIT_RETENTION_CADENCE", "BALEIA_QUERY_HISTORY_RETENTION_CADENCE",
	"BALEIA_AUDIT_OFFLOAD_BEFORE_DROP", "BALEIA_QUERY_HISTORY_OFFLOAD_BEFORE_DROP",
}

func loadWith(t *testing.T, env map[string]string) *Config {
	t.Helper()
	for _, k := range retentionEnv {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	return Load()
}

// validate reports Validate's error with a valid JWT secret, so only the
// retention settings can make it fail.
func validate(c *Config) error {
	c.JWTSecret = strings.Repeat("s", MinJWTSecretLen)
	return c.Validate()
}

//nolint:paralleltest // t.Setenv
func TestRetentionDefaults(t *testing.T) {
	c := loadWith(t, nil)
	if err := validate(c); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	if got := c.AuditRetentionWindow(); got != 45*24*time.Hour {
		t.Errorf("audit window = %s, want 45d", got)
	}
	if got := c.QueryHistoryRetentionWindow(); got != 45*24*time.Hour {
		t.Errorf("query history window = %s, want 45d", got)
	}
	if c.AuditGrace() != 24*time.Hour || c.QueryHistoryGrace() != 24*time.Hour {
		t.Errorf("grace = %s / %s, want 1d", c.AuditGrace(), c.QueryHistoryGrace())
	}
	if c.PruneCap() != 14 {
		t.Errorf("cap = %d, want 14", c.PruneCap())
	}
	if c.AuditRetentionCadence != "weekly" || c.QueryHistoryRetentionCadence != "weekly" {
		t.Errorf("cadence = %q / %q, want weekly", c.AuditRetentionCadence, c.QueryHistoryRetentionCadence)
	}
	if c.AuditDropWithoutOffload || c.QueryHistoryDropWithoutOffload {
		t.Error("the offload gate must be on by default")
	}
}

//nolint:paralleltest // t.Setenv
func TestRetentionWindowsAcceptDays(t *testing.T) {
	c := loadWith(t, map[string]string{
		"BALEIA_AUDIT_RETENTION": "400d", "BALEIA_QUERY_HISTORY_RETENTION": "720h",
		"BALEIA_AUDIT_COLD_GRACE": "3d", "BALEIA_PRUNE_MAX_PARTITIONS": "30",
	})
	if err := validate(c); err != nil {
		t.Fatal(err)
	}
	if c.AuditRetentionWindow() != 400*24*time.Hour || c.QueryHistoryRetentionWindow() != 720*time.Hour {
		t.Errorf("windows = %s / %s", c.AuditRetentionWindow(), c.QueryHistoryRetentionWindow())
	}
	if c.AuditGrace() != 72*time.Hour || c.PruneCap() != 30 {
		t.Errorf("grace = %s, cap = %d", c.AuditGrace(), c.PruneCap())
	}
}

// TestRetentionRejectsWhatItCannotRead: a setting that decides when data is
// deleted must stop the worker rather than fall back to a default on a typo.
//
//nolint:paralleltest // t.Setenv
func TestRetentionRejectsWhatItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"not a duration", map[string]string{"BALEIA_AUDIT_RETENTION": "forty-five"}, "BALEIA_AUDIT_RETENTION"},
		{"half a day", map[string]string{"BALEIA_AUDIT_RETENTION": "12h"}, "BALEIA_AUDIT_RETENTION must be at least 1d"},
		{"zero is not unset", map[string]string{"BALEIA_QUERY_HISTORY_RETENTION": "0"}, "BALEIA_QUERY_HISTORY_RETENTION must be at least 1d"},
		{"negative", map[string]string{"BALEIA_AUDIT_RETENTION": "-5d"}, "BALEIA_AUDIT_RETENTION"},
		{"bad days", map[string]string{"BALEIA_AUDIT_COLD_GRACE": "xd"}, "BALEIA_AUDIT_COLD_GRACE"},
		{"bad cadence", map[string]string{"BALEIA_AUDIT_RETENTION_CADENCE": "sometimes"}, "BALEIA_AUDIT_RETENTION_CADENCE"},
		{"six-field cron", map[string]string{"BALEIA_QUERY_HISTORY_RETENTION_CADENCE": "0 0 3 * * 0"}, "BALEIA_QUERY_HISTORY_RETENTION_CADENCE"},
		{"cron out of range", map[string]string{"BALEIA_AUDIT_RETENTION_CADENCE": "0 30 * * *"}, "BALEIA_AUDIT_RETENTION_CADENCE"},
		{"cap that is not a number", map[string]string{"BALEIA_PRUNE_MAX_PARTITIONS": "1x"}, "BALEIA_PRUNE_MAX_PARTITIONS"},
		{"cap of zero", map[string]string{"BALEIA_PRUNE_MAX_PARTITIONS": "0"}, "BALEIA_PRUNE_MAX_PARTITIONS must be at least 1"},
		{"negative cap", map[string]string{"BALEIA_PRUNE_MAX_PARTITIONS": "-3"}, "BALEIA_PRUNE_MAX_PARTITIONS must be at least 1"},
		{"zero grace is not unset", map[string]string{"BALEIA_AUDIT_COLD_GRACE": "0"}, "BALEIA_AUDIT_COLD_GRACE"},
		{"days that overflow a duration", map[string]string{"BALEIA_AUDIT_RETENTION": "213505d"}, "BALEIA_AUDIT_RETENTION"},
		{"days just past what a duration holds", map[string]string{"BALEIA_QUERY_HISTORY_RETENTION": "106752d"}, "BALEIA_QUERY_HISTORY_RETENTION"},
		{"both day fields restricted, impossible date", map[string]string{"BALEIA_AUDIT_RETENTION_CADENCE": "0 3 31 2 1"}, "BALEIA_AUDIT_RETENTION_CADENCE"},
		{"both day fields restricted", map[string]string{"BALEIA_QUERY_HISTORY_RETENTION_CADENCE": "0 3 1 * 1"}, "BALEIA_QUERY_HISTORY_RETENTION_CADENCE"},
		{"offload flag is not a bool", map[string]string{"BALEIA_AUDIT_OFFLOAD_BEFORE_DROP": "maybe"}, "BALEIA_AUDIT_OFFLOAD_BEFORE_DROP must be true or false"},
		{"offload flag in capitals", map[string]string{"BALEIA_QUERY_HISTORY_OFFLOAD_BEFORE_DROP": "FALSE"}, "BALEIA_QUERY_HISTORY_OFFLOAD_BEFORE_DROP"},
	} {
		t.Run(tc.name, func(t *testing.T) { //nolint:paralleltest // t.Setenv
			err := validate(loadWith(t, tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

//nolint:paralleltest // t.Setenv
func TestOffloadOptOut(t *testing.T) {
	c := loadWith(t, map[string]string{"BALEIA_AUDIT_OFFLOAD_BEFORE_DROP": "false"})
	if err := validate(c); err != nil {
		t.Fatal(err)
	}
	if !c.AuditDropWithoutOffload || c.QueryHistoryDropWithoutOffload {
		t.Errorf("opt-out = audit %v, query history %v; want audit only", c.AuditDropWithoutOffload, c.QueryHistoryDropWithoutOffload)
	}
	// An explicit true is the default.
	c = loadWith(t, map[string]string{"BALEIA_AUDIT_OFFLOAD_BEFORE_DROP": "true"})
	if c.AuditDropWithoutOffload {
		t.Error("true must keep the gate")
	}
}

func TestRetentionCron(t *testing.T) {
	t.Parallel()
	for cadence, want := range map[string]string{
		"": "0 3 * * 0", "weekly": "0 3 * * 0", "daily": "0 3 * * *", "30 2 * * 1,4": "30 2 * * 1,4",
	} {
		got, err := RetentionCron(cadence)
		if err != nil || got != want {
			t.Errorf("RetentionCron(%q) = %q, %v; want %q", cadence, got, err, want)
		}
	}
}

// TestRetentionPeriod: the longest gap between two runs, which the stale
// retention alert allows for.
func TestRetentionPeriod(t *testing.T) {
	t.Parallel()
	day := 24 * time.Hour
	for cadence, want := range map[string]time.Duration{
		"daily": day, "weekly": 7 * day, "": 7 * day,
		"0 3 * * 1,4": 4 * day,  // Mon, Thu: gaps of 3 and 4 days
		"0 3 1 * *":   31 * day, // the first of the month: the longest gap is 31 days
		"0 */6 * * *": 6 * time.Hour,
		// A firing exactly on the sample's first instant must still count: the
		// gap that wraps from January to December is the longest one.
		"0 0 1 1,12 *": 334 * day,
	} {
		got, err := RetentionPeriod(cadence)
		if err != nil || got != want {
			t.Errorf("RetentionPeriod(%q) = %s, %v; want %s", cadence, got, err, want)
		}
	}
	if _, err := RetentionPeriod("nope"); err == nil {
		t.Error("RetentionPeriod accepted a bad cadence")
	}
}

// TestRetentionAcceptsOneRestrictedDayField: a cron that restricts the day of
// month or the day of week, one of them, means the same in every implementation.
func TestRetentionAcceptsOneRestrictedDayField(t *testing.T) {
	t.Parallel()
	for _, cadence := range []string{"0 3 1 * *", "0 3 * * 1", "0 3 * * 1,4", "0 3 1,15 * *", "30 2 * 6 *"} {
		if _, err := RetentionCron(cadence); err != nil {
			t.Errorf("RetentionCron(%q) = %v, want it accepted", cadence, err)
		}
	}
}

// TestRetentionDaysDoNotOverflow: a day count a duration cannot hold must be
// refused, not wrapped into a window of about a day.
func TestRetentionDaysDoNotOverflow(t *testing.T) {
	t.Parallel()
	if _, err := parseRetention("213505d"); err == nil {
		t.Error("parseRetention accepted 213505d, which wraps a duration")
	}
	if d, err := parseRetention("100000d"); err != nil || d != 100000*24*time.Hour {
		t.Errorf("parseRetention(100000d) = %s, %v; want it exact", d, err)
	}
}

// TestRetentionPeriodOfADenseCadence: a cadence that fires every minute must not
// hold the worker up at startup.
func TestRetentionPeriodOfADenseCadence(t *testing.T) {
	t.Parallel()
	start := time.Now()
	got, err := RetentionPeriod("* * * * *")
	if err != nil || got != time.Minute {
		t.Fatalf("RetentionPeriod(* * * * *) = %s, %v; want 1m", got, err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s, want well under a few seconds", took)
	}
}
