package workerapp

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/persistence/partitioning"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
)

// ids lists the IDs of the specs.
func ids(specs []schedules.Spec) []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.ID)
	}
	slices.Sort(out)
	return out
}

func TestColdSchedules(t *testing.T) {
	t.Parallel()
	all := func(extra ...string) []string { return append([]string{}, extra...) }
	for _, tc := range []struct {
		name       string
		cfg        config.Config
		wantEnsure []string
		wantRemove []string
		// crons maps a schedule ID to the cron expression it must carry.
		crons map[string]string
	}{
		{
			name:       "no bucket and no opt-out: nothing runs, retention stays fail-closed",
			cfg:        config.Config{},
			wantEnsure: nil,
			wantRemove: all("audit-offload", "audit-retention", "query-history-offload", "query-history-retention"),
		},
		{
			name:       "an audit bucket: offload and retention on the weekly default",
			cfg:        config.Config{AuditColdBucket: "cold"},
			wantEnsure: all("audit-offload", "audit-retention"),
			wantRemove: all("query-history-offload", "query-history-retention"),
			crons:      map[string]string{"audit-retention": "0 3 * * 0"},
		},
		{
			name:       "an opt-out without a bucket: retention runs, nothing is offloaded",
			cfg:        config.Config{AuditDropWithoutOffload: true},
			wantEnsure: all("audit-retention"),
			wantRemove: all("audit-offload", "query-history-offload", "query-history-retention"),
			crons:      map[string]string{"audit-retention": "0 3 * * 0"},
		},
		{
			name: "both streams, each on its own cadence",
			cfg: config.Config{
				AuditColdBucket: "a", QueryHistoryColdBucket: "q",
				AuditRetentionCadence: "daily", QueryHistoryRetentionCadence: "30 2 * * 1,4",
			},
			wantEnsure: all("audit-offload", "audit-retention", "query-history-offload", "query-history-retention"),
			crons:      map[string]string{"audit-retention": "0 3 * * *", "query-history-retention": "30 2 * * 1,4"},
		},
		{
			name:       "a bucket with the opt-out: still offloaded, retention still runs",
			cfg:        config.Config{QueryHistoryColdBucket: "q", QueryHistoryDropWithoutOffload: true},
			wantEnsure: all("query-history-offload", "query-history-retention"),
			wantRemove: all("audit-offload", "audit-retention"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ensure, remove, err := coldSchedules(&tc.cfg)
			if err != nil {
				t.Fatalf("coldSchedules: %v", err)
			}
			if got := ids(ensure); !slices.Equal(got, tc.wantEnsure) && (len(got) != 0 || len(tc.wantEnsure) != 0) {
				t.Errorf("ensure = %v, want %v", got, tc.wantEnsure)
			}
			slices.Sort(remove)
			slices.Sort(tc.wantRemove)
			if !slices.Equal(remove, tc.wantRemove) && (len(remove) != 0 || len(tc.wantRemove) != 0) {
				t.Errorf("remove = %v, want %v", remove, tc.wantRemove)
			}
			for _, s := range ensure {
				want, ok := tc.crons[s.ID]
				if ok && s.Cron != want {
					t.Errorf("%s cron = %q, want %q", s.ID, s.Cron, want)
				}
				if s.Cron != "" && s.Every != 0 {
					t.Errorf("%s has both a cron and an interval", s.ID)
				}
				// Retention is on a calendar; offload stays on a fixed interval.
				isRetention := s.ID == "audit-retention" || s.ID == "query-history-retention"
				if isRetention != (s.Cron != "") {
					t.Errorf("%s: cron = %q, want a cron only for retention", s.ID, s.Cron)
				}
			}
		})
	}

	t.Run("a bad cadence is an error", func(t *testing.T) {
		t.Parallel()
		cfg := config.Config{AuditColdBucket: "cold", AuditRetentionCadence: "sometimes"}
		if _, _, err := coldSchedules(&cfg); err == nil {
			t.Fatal("coldSchedules accepted a bad cadence")
		}
	})
}

type recordingAudit struct{ events []audit.Event }

func (r *recordingAudit) Record(_ context.Context, e audit.Event) error {
	r.events = append(r.events, e)
	return nil
}

func TestDroppedWithoutOffload(t *testing.T) {
	t.Parallel()
	rec := &recordingAudit{}
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	p := partitioning.Partition{Name: "audit_events_20260901", From: day, Until: day.AddDate(0, 0, 1)}

	droppedWithoutOffload(rec, "audit")(context.Background(), p)
	if len(rec.events) != 1 {
		t.Fatalf("events = %d, want 1", len(rec.events))
	}
	e := rec.events[0]
	if e.Action != audit.PartitionDrop || e.ResourceType != "partition" || e.ResourceID != "audit_events_20260901" ||
		e.Outcome != audit.Allowed || e.Reason != audit.ReasonNoOffload {
		t.Errorf("event = %+v", e)
	}
	if e.Changes["stream"].To != "audit" || e.Changes["day"].To != "2026-09-01" {
		t.Errorf("changes = %+v, want the stream and the day", e.Changes)
	}

	// No recorder: a drop is never held up by the audit write.
	droppedWithoutOffload(nil, "audit")(context.Background(), p)
}
