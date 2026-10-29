// Package maintenance holds the per-table Iceberg maintenance parameters and
// decides which operation is due on a table (#500). It is pure: callers load
// the parameters, the table's metrics and its last runs, and act on the plan.
package maintenance

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Operations are Trino's Iceberg procedures, in the priority a round runs
// them: orphan cleanup comes last because expiry frees the files it removes.
const (
	OpOptimize          = "optimize"
	OpOptimizeManifests = "optimize_manifests"
	OpExpireSnapshots   = "expire_snapshots"
	OpRemoveOrphanFiles = "remove_orphan_files"
)

// Priority is the order in which due operations are picked.
var Priority = []string{OpOptimize, OpOptimizeManifests, OpExpireSnapshots, OpRemoveOrphanFiles}

// Modes of a table's parameters.
const (
	ModeManaged             = "managed"
	ModeOff                 = "off"
	ModeMaintainedElsewhere = "maintained_elsewhere"
)

// Conflict policies for a commit conflict with a concurrent writer.
const (
	RetryWithBackoff = "retry_with_backoff"
	GiveUpAndReport  = "give_up_and_report"
)

// MinIntervalFloor is the shortest min_interval_seconds accepted.
const MinIntervalFloor = 60

// UnmanagedRetentionFloor is Trino's default minimum retention, which applies
// to catalogs whose properties Baleia does not project (adopted, native).
const UnmanagedRetentionFloor = 7 * 24 * time.Hour

// Window is the time of day an operation may run in: hours in [0, 23], both
// ends included, wrapping past midnight when Start > End.
type Window struct {
	StartHour int    `json:"start_hour"`
	EndHour   int    `json:"end_hour"`
	TZ        string `json:"tz"`
}

// Trigger holds the measured conditions that call for an operation. Each
// operation reads only its own fields; a nil field is not a condition.
type Trigger struct {
	// optimize: share of data files smaller than target_file_size_bytes.
	SmallFileRatio *float64 `json:"small_file_ratio,omitempty"`
	// optimize: delete files (format v2+).
	DeleteFiles *int64 `json:"delete_files,omitempty"`
	// optimize_manifests: manifest count.
	Manifests *int64 `json:"manifests,omitempty"`
	// expire_snapshots: snapshot count and age of the oldest snapshot.
	Snapshots        *int64 `json:"snapshots,omitempty"`
	OldestAgeSeconds *int64 `json:"oldest_age_seconds,omitempty"`
}

// Params are the procedure arguments, per operation.
type Params struct {
	// optimize
	TargetFileSizeBytes       *int64   `json:"target_file_size_bytes,omitempty"`
	PartitionFilter           *string  `json:"partition_filter,omitempty"`
	SkipFilesNewerThanSeconds *int64   `json:"skip_files_newer_than_seconds,omitempty"`
	SortedBy                  []string `json:"sorted_by,omitempty"`
	// expire_snapshots
	RetentionSeconds     *int64 `json:"retention_seconds,omitempty"`
	RetainLast           *int64 `json:"retain_last,omitempty"`
	CleanExpiredMetadata *bool  `json:"clean_expired_metadata,omitempty"`
	// remove_orphan_files
	OlderThanSeconds *int64 `json:"older_than_seconds,omitempty"`
}

// Settings are one operation's parameters on one table.
type Settings struct {
	Enabled            bool    `json:"enabled"`
	Trigger            Trigger `json:"trigger"`
	MinIntervalSeconds int64   `json:"min_interval_seconds"`
	// MaxIntervalSeconds runs the operation once it has passed, even when
	// no trigger fired. Orphan cleanup has no trigger and relies on it.
	MaxIntervalSeconds *int64 `json:"max_interval_seconds,omitempty"`
	// Window nil means any time.
	Window         *Window `json:"window,omitempty"`
	TimeoutSeconds int64   `json:"timeout_seconds"`
	OnConflict     string  `json:"on_conflict"`
	Params         Params  `json:"params"`
}

// Policy is a table's maintenance parameters.
type Policy struct {
	Mode                        string   `json:"mode"`
	WriterCommitIntervalSeconds int64    `json:"writer_commit_interval_seconds"`
	Optimize                    Settings `json:"optimize"`
	OptimizeManifests           Settings `json:"optimize_manifests"`
	ExpireSnapshots             Settings `json:"expire_snapshots"`
	RemoveOrphanFiles           Settings `json:"remove_orphan_files"`
}

// Operation returns the settings of op.
func (p Policy) Operation(op string) Settings {
	switch op {
	case OpOptimize:
		return p.Optimize
	case OpOptimizeManifests:
		return p.OptimizeManifests
	case OpExpireSnapshots:
		return p.ExpireSnapshots
	case OpRemoveOrphanFiles:
		return p.RemoveOrphanFiles
	}
	return Settings{}
}

// DecodeSettings reads one operation's stored JSON; an empty object is a
// disabled operation.
func DecodeSettings(raw []byte) (Settings, error) {
	var s Settings
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Table is what validation needs to know about the table and its catalog.
type Table struct {
	// FormatVersion is the Iceberg format version; nil when unknown.
	FormatVersion *int
	// RetentionFloor is the shortest retention the engine accepts for
	// expire_snapshots and remove_orphan_files on this catalog.
	RetentionFloor time.Duration
}

// FieldError is a rejected field, named by its JSON path. Code is one of the
// API's field codes: required, out_of_range, invalid_format, not_allowed.
type FieldError struct {
	Field  string
	Code   string
	Params map[string]string
}

// Field codes, as in the API's FieldCode enum.
const (
	CodeRequired      = "required"
	CodeOutOfRange    = "out_of_range"
	CodeInvalidFormat = "invalid_format"
	CodeNotAllowed    = "not_allowed"
)

// Validate checks p against the rules of #500 and returns every rejected
// field. Disabled operations are not checked: their settings may be partial.
func Validate(p Policy, t Table) []FieldError {
	v := &validator{}
	switch p.Mode {
	case ModeManaged, ModeOff, ModeMaintainedElsewhere:
	default:
		v.add("mode", CodeNotAllowed, nil)
	}
	if p.WriterCommitIntervalSeconds <= 0 {
		v.add("writer_commit_interval_seconds", CodeOutOfRange, map[string]string{"min": "1"})
	}
	for _, op := range Priority {
		s := p.Operation(op)
		if !s.Enabled {
			continue
		}
		v.common(op, s)
		switch op {
		case OpOptimize:
			v.optimize(s, p.WriterCommitIntervalSeconds, t)
		case OpOptimizeManifests:
			v.onlyTriggers(op, s.Trigger, "manifests")
			v.positive(op+".trigger.manifests", s.Trigger.Manifests)
		case OpExpireSnapshots:
			v.expire(s, t)
		case OpRemoveOrphanFiles:
			v.orphans(s, p.WriterCommitIntervalSeconds, t)
		}
	}
	return v.errs
}

type validator struct{ errs []FieldError }

func (v *validator) add(field, code string, params map[string]string) {
	v.errs = append(v.errs, FieldError{Field: field, Code: code, Params: params})
}

func (v *validator) common(op string, s Settings) {
	if s.MinIntervalSeconds < MinIntervalFloor {
		v.add(op+".min_interval_seconds", CodeOutOfRange, map[string]string{"min": strconv.Itoa(MinIntervalFloor)})
	}
	if s.MaxIntervalSeconds != nil && *s.MaxIntervalSeconds < s.MinIntervalSeconds {
		v.add(op+".max_interval_seconds", CodeOutOfRange, map[string]string{"min": strconv.FormatInt(s.MinIntervalSeconds, 10)})
	}
	if s.TimeoutSeconds <= 0 {
		v.add(op+".timeout_seconds", CodeOutOfRange, map[string]string{"min": "1"})
	}
	if s.OnConflict != RetryWithBackoff && s.OnConflict != GiveUpAndReport {
		v.add(op+".on_conflict", CodeNotAllowed, nil)
	}
	if w := s.Window; w != nil {
		if w.StartHour < 0 || w.StartHour > 23 {
			v.add(op+".window.start_hour", CodeOutOfRange, map[string]string{"min": "0", "max": "23"})
		}
		if w.EndHour < 0 || w.EndHour > 23 {
			v.add(op+".window.end_hour", CodeOutOfRange, map[string]string{"min": "0", "max": "23"})
		}
		if _, err := time.LoadLocation(w.TZ); err != nil || w.TZ == "" {
			v.add(op+".window.tz", CodeInvalidFormat, nil)
		}
	}
	// An enabled operation with neither a trigger nor a maximum interval
	// would never be due.
	if !hasTrigger(op, s.Trigger) && s.MaxIntervalSeconds == nil {
		v.add(op+".trigger", CodeRequired, nil)
	}
}

func (v *validator) optimize(s Settings, writerInterval int64, t Table) {
	const op = OpOptimize
	v.onlyTriggers(op, s.Trigger, "small_file_ratio", "delete_files")
	if r := s.Trigger.SmallFileRatio; r != nil {
		if *r <= 0 || *r > 1 {
			v.add(op+".trigger.small_file_ratio", CodeOutOfRange, map[string]string{"min": "0", "max": "1"})
		}
		if s.Params.TargetFileSizeBytes == nil {
			v.add(op+".params.target_file_size_bytes", CodeRequired, nil)
		}
	}
	v.positive(op+".params.target_file_size_bytes", s.Params.TargetFileSizeBytes)
	if s.Trigger.DeleteFiles != nil {
		v.positive(op+".trigger.delete_files", s.Trigger.DeleteFiles)
		if t.FormatVersion != nil && *t.FormatVersion < 2 {
			v.add(op+".trigger.delete_files", CodeNotAllowed, map[string]string{"format_version": strconv.Itoa(*t.FormatVersion)})
		}
	}
	if n := s.Params.SkipFilesNewerThanSeconds; n != nil && *n < writerInterval {
		v.add(op+".params.skip_files_newer_than_seconds", CodeOutOfRange, map[string]string{"min": strconv.FormatInt(writerInterval, 10)})
	}
	if f := s.Params.PartitionFilter; f != nil && (strings.TrimSpace(*f) == "" || strings.Contains(*f, ";")) {
		v.add(op+".params.partition_filter", CodeInvalidFormat, nil)
	}
	v.onlyParams(op, s.Params, "target_file_size_bytes", "partition_filter", "skip_files_newer_than_seconds", "sorted_by")
}

func (v *validator) expire(s Settings, t Table) {
	const op = OpExpireSnapshots
	v.onlyTriggers(op, s.Trigger, "snapshots", "oldest_age_seconds")
	v.positive(op+".trigger.snapshots", s.Trigger.Snapshots)
	v.positive(op+".trigger.oldest_age_seconds", s.Trigger.OldestAgeSeconds)
	v.retention(op+".params.retention_seconds", s.Params.RetentionSeconds, t)
	if n := s.Params.RetainLast; n != nil && *n < 1 {
		v.add(op+".params.retain_last", CodeOutOfRange, map[string]string{"min": "1"})
	}
	v.onlyParams(op, s.Params, "retention_seconds", "retain_last", "clean_expired_metadata")
}

func (v *validator) orphans(s Settings, writerInterval int64, t Table) {
	const op = OpRemoveOrphanFiles
	v.onlyTriggers(op, s.Trigger)
	field := op + ".params.older_than_seconds"
	v.retention(field, s.Params.OlderThanSeconds, t)
	if n := s.Params.OlderThanSeconds; n != nil && *n <= writerInterval {
		v.add(field, CodeOutOfRange, map[string]string{"min": strconv.FormatInt(writerInterval+1, 10)})
	}
	v.onlyParams(op, s.Params, "older_than_seconds")
}

// retention requires a value at or above the engine's floor.
func (v *validator) retention(field string, value *int64, t Table) {
	if value == nil {
		v.add(field, CodeRequired, nil)
		return
	}
	floor := int64(t.RetentionFloor / time.Second)
	if *value < floor || *value <= 0 {
		v.add(field, CodeOutOfRange, map[string]string{"min": strconv.FormatInt(max(floor, 1), 10)})
	}
}

func (v *validator) positive(field string, value *int64) {
	if value != nil && *value <= 0 {
		v.add(field, CodeOutOfRange, map[string]string{"min": "1"})
	}
}

// field is a named optional value, listed in a fixed order so errors come
// back in the same order every time.
type field struct {
	name string
	set  bool
}

// onlyTriggers rejects trigger fields that do not belong to op.
func (v *validator) onlyTriggers(op string, t Trigger, allowed ...string) {
	for _, f := range []field{
		{"small_file_ratio", t.SmallFileRatio != nil},
		{"delete_files", t.DeleteFiles != nil},
		{"manifests", t.Manifests != nil},
		{"snapshots", t.Snapshots != nil},
		{"oldest_age_seconds", t.OldestAgeSeconds != nil},
	} {
		if f.set && !slices.Contains(allowed, f.name) {
			v.add(op+".trigger."+f.name, CodeNotAllowed, nil)
		}
	}
}

// onlyParams rejects params that do not belong to op.
func (v *validator) onlyParams(op string, p Params, allowed ...string) {
	for _, f := range []field{
		{"target_file_size_bytes", p.TargetFileSizeBytes != nil},
		{"partition_filter", p.PartitionFilter != nil},
		{"skip_files_newer_than_seconds", p.SkipFilesNewerThanSeconds != nil},
		{"sorted_by", len(p.SortedBy) > 0},
		{"retention_seconds", p.RetentionSeconds != nil},
		{"retain_last", p.RetainLast != nil},
		{"clean_expired_metadata", p.CleanExpiredMetadata != nil},
		{"older_than_seconds", p.OlderThanSeconds != nil},
	} {
		if f.set && !slices.Contains(allowed, f.name) {
			v.add(op+".params."+f.name, CodeNotAllowed, nil)
		}
	}
}

// hasTrigger reports whether op has at least one of its own triggers set.
func hasTrigger(op string, t Trigger) bool {
	switch op {
	case OpOptimize:
		return t.SmallFileRatio != nil || t.DeleteFiles != nil
	case OpOptimizeManifests:
		return t.Manifests != nil
	case OpExpireSnapshots:
		return t.Snapshots != nil || t.OldestAgeSeconds != nil
	}
	return false
}
