package maintenance

import (
	"encoding/json"
	"math"
	"slices"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

// validPolicy is a CDC-like table: hourly compaction of cold files, daily
// expiry and weekly orphan cleanup.
func validPolicy() Policy {
	return Policy{
		Mode:                        ModeManaged,
		WriterCommitIntervalSeconds: 60,
		Optimize: Settings{
			Enabled:            true,
			Trigger:            Trigger{SmallFileRatio: ptr(0.3), DeleteFiles: ptr(int64(200))},
			MinIntervalSeconds: 3600,
			TimeoutSeconds:     1800,
			OnConflict:         RetryWithBackoff,
			Params: Params{
				TargetFileSizeBytes:       ptr(int64(256 << 20)),
				SkipFilesNewerThanSeconds: ptr(int64(3600)),
			},
		},
		ExpireSnapshots: Settings{
			Enabled:            true,
			Trigger:            Trigger{Snapshots: ptr(int64(100))},
			MinIntervalSeconds: 3600,
			MaxIntervalSeconds: ptr(int64(86400)),
			TimeoutSeconds:     1800,
			OnConflict:         RetryWithBackoff,
			Params:             Params{RetentionSeconds: ptr(int64(259200)), RetainLast: ptr(int64(10))},
		},
		RemoveOrphanFiles: Settings{
			Enabled:            true,
			MinIntervalSeconds: 604800,
			MaxIntervalSeconds: ptr(int64(604800)),
			TimeoutSeconds:     3600,
			OnConflict:         GiveUpAndReport,
			Params:             Params{OlderThanSeconds: ptr(int64(86400))},
		},
	}
}

var managedTable = Table{FormatVersion: ptr(2), RetentionFloor: time.Hour}

func TestPolicy_Operation(t *testing.T) {
	t.Parallel()
	p := validPolicy()
	if !p.Operation(OpOptimize).Enabled || p.Operation(OpOptimizeManifests).Enabled {
		t.Fatalf("Operation returned the wrong settings")
	}
	if p.Operation("vacuum").Enabled {
		t.Fatalf("an unknown operation must be disabled")
	}
}

func TestDecodeSettings(t *testing.T) {
	t.Parallel()
	empty, err := DecodeSettings([]byte(`{}`))
	if err != nil || empty.Enabled {
		t.Fatalf("an empty object is a disabled operation, got %+v, %v", empty, err)
	}
	raw, _ := json.Marshal(validPolicy().Optimize)
	got, err := DecodeSettings(raw)
	if err != nil || !got.Enabled || *got.Trigger.SmallFileRatio != 0.3 {
		t.Fatalf("round trip lost settings: %+v, %v", got, err)
	}
	if _, err := DecodeSettings([]byte(`{`)); err == nil {
		t.Fatalf("invalid JSON must fail")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*Policy)
		table  Table
		want   []string // "field code"
	}{
		{name: "valid", change: func(*Policy) {}, table: managedTable},
		{name: "unknown mode", change: func(p *Policy) { p.Mode = "auto" }, table: managedTable,
			want: []string{"mode not_allowed"}},
		{name: "writer interval", change: func(p *Policy) { p.WriterCommitIntervalSeconds = 0 }, table: managedTable,
			want: []string{
				"writer_commit_interval_seconds out_of_range",
			}},
		{name: "writer interval above the column", change: func(p *Policy) { p.WriterCommitIntervalSeconds = math.MaxInt32 + 1 }, table: managedTable,
			want: []string{
				"writer_commit_interval_seconds out_of_range",
			}},
		{name: "min interval below the floor", change: func(p *Policy) { p.Optimize.MinIntervalSeconds = 30 }, table: managedTable,
			want: []string{"optimize.min_interval_seconds out_of_range"}},
		{name: "max interval below min", change: func(p *Policy) { p.ExpireSnapshots.MaxIntervalSeconds = ptr(int64(60)) }, table: managedTable,
			want: []string{"expire_snapshots.max_interval_seconds out_of_range"}},
		{name: "timeout and conflict policy", change: func(p *Policy) {
			p.Optimize.TimeoutSeconds = 0
			p.Optimize.OnConflict = "ignore"
		}, table: managedTable, want: []string{"optimize.timeout_seconds out_of_range", "optimize.on_conflict not_allowed"}},
		{name: "window", change: func(p *Policy) {
			p.Optimize.Window = &Window{StartHour: 24, EndHour: -1, TZ: "Mars/Olympus"}
		}, table: managedTable, want: []string{
			"optimize.window.start_hour out_of_range", "optimize.window.end_hour out_of_range", "optimize.window.tz invalid_format",
		}},
		{name: "enabled without trigger or max interval", change: func(p *Policy) {
			p.Optimize.Trigger = Trigger{}
		}, table: managedTable, want: []string{"optimize.trigger required"}},
		{name: "trigger of another operation", change: func(p *Policy) {
			p.Optimize.Trigger.Snapshots = ptr(int64(5))
		}, table: managedTable, want: []string{"optimize.trigger.snapshots not_allowed"}},
		{name: "small-file ratio needs a target size", change: func(p *Policy) {
			p.Optimize.Trigger.SmallFileRatio = ptr(1.5)
			p.Optimize.Params.TargetFileSizeBytes = nil
		}, table: managedTable, want: []string{
			"optimize.trigger.small_file_ratio out_of_range", "optimize.params.target_file_size_bytes required",
		}},
		{name: "delete files on format v1", change: func(*Policy) {}, table: Table{FormatVersion: ptr(1), RetentionFloor: time.Hour},
			want: []string{"optimize.trigger.delete_files not_allowed"}},
		{name: "hot files shorter than the writer interval", change: func(p *Policy) {
			p.Optimize.Params.SkipFilesNewerThanSeconds = ptr(int64(30))
		}, table: managedTable, want: []string{"optimize.params.skip_files_newer_than_seconds out_of_range"}},
		{name: "partition filter with a statement separator", change: func(p *Policy) {
			p.Optimize.Params.PartitionFilter = ptr("day < DATE '2026-01-01'; DROP TABLE t")
		}, table: managedTable, want: []string{"optimize.params.partition_filter invalid_format"}},
		{name: "param of another operation", change: func(p *Policy) {
			p.Optimize.Params.RetainLast = ptr(int64(3))
		}, table: managedTable, want: []string{"optimize.params.retain_last not_allowed"}},
		{name: "expiry needs a retention at or above the floor", change: func(p *Policy) {
			p.ExpireSnapshots.Params.RetentionSeconds = ptr(int64(600))
			p.ExpireSnapshots.Params.RetainLast = ptr(int64(0))
		}, table: managedTable, want: []string{
			"expire_snapshots.params.retention_seconds out_of_range", "expire_snapshots.params.retain_last out_of_range",
		}},
		{name: "unmanaged catalog keeps Trino's 7-day floor", change: func(*Policy) {},
			table: Table{FormatVersion: ptr(2), RetentionFloor: UnmanagedRetentionFloor},
			want: []string{
				"expire_snapshots.params.retention_seconds out_of_range",
				"remove_orphan_files.params.older_than_seconds out_of_range",
			}},
		{name: "orphan cleanup must outlast the writer", change: func(p *Policy) {
			p.WriterCommitIntervalSeconds = 90000
			p.Optimize.Params.SkipFilesNewerThanSeconds = ptr(int64(90000))
		}, table: managedTable, want: []string{"remove_orphan_files.params.older_than_seconds out_of_range"}},
		{name: "orphan cleanup without retention", change: func(p *Policy) {
			p.RemoveOrphanFiles.Params.OlderThanSeconds = nil
		}, table: managedTable, want: []string{"remove_orphan_files.params.older_than_seconds required"}},
		{name: "a disabled operation is not checked", change: func(p *Policy) {
			p.OptimizeManifests = Settings{Enabled: false, MinIntervalSeconds: 1}
		}, table: managedTable},
		{name: "manifest rewrite", change: func(p *Policy) {
			p.OptimizeManifests = Settings{Enabled: true, Trigger: Trigger{Manifests: ptr(int64(0))}, MinIntervalSeconds: 3600,
				TimeoutSeconds: 60, OnConflict: RetryWithBackoff}
		}, table: managedTable, want: []string{"optimize_manifests.trigger.manifests out_of_range"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := validPolicy()
			tt.change(&p)
			var got []string
			for _, e := range Validate(p, tt.table) {
				got = append(got, e.Field+" "+e.Code)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Validate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidate_ParamsNameTheLimit(t *testing.T) {
	t.Parallel()
	p := validPolicy()
	p.Optimize.MinIntervalSeconds = 1
	errs := Validate(p, managedTable)
	if len(errs) != 1 || errs[0].Params["min"] != "60" {
		t.Fatalf("Validate() = %+v, want the floor in params", errs)
	}
}
