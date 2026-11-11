package activities

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/persistence/partitioning"
)

// storeWith is a store that lists partitions and does nothing else.
type storeWith struct {
	db.Querier
	parts map[string][]partitioning.Partition
	err   error
}

func (s storeWith) Partitions(_ context.Context, table string) ([]partitioning.Partition, error) {
	return s.parts[table], s.err
}

func day(d int) partitioning.Partition {
	from := time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC)
	return partitioning.Partition{Name: "t", From: from, Until: from.AddDate(0, 0, 1)}
}

func scrapeBody(t *testing.T, reg *metrics.Registry) string {
	t.Helper()
	families, err := reg.Prometheus().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, f := range families {
		for _, m := range f.GetMetric() {
			b.WriteString(f.GetName())
			for _, l := range m.GetLabel() {
				b.WriteString("|" + l.GetName() + "=" + l.GetValue())
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// TestObserveRetention: the oldest partition of a stream is exported with its
// allowance, read from the store so a stream whose retention is not wired is
// still seen; a stream with no allowance, no partitions or an unreadable listing
// exports nothing and never fails the pass.
func TestObserveRetention(t *testing.T) {
	t.Parallel()

	t.Run("exports both streams, the oldest partition of each", func(t *testing.T) {
		t.Parallel()
		reg := metrics.New("worker")
		a := &Activities{store: storeWith{parts: map[string][]partitioning.Partition{
			// Listed out of order: the oldest is still Sep 3.
			"audit_events":     {day(9), day(3), day(5)},
			"query_executions": {day(20)},
		}}}
		a.SetMetrics(reg)
		a.SetRetentionAllowance("audit", 53*24*time.Hour)
		a.SetRetentionAllowance("query_history", 53*24*time.Hour)
		a.observeAllRetention(context.Background())
		body := scrapeBody(t, reg)
		for _, want := range []string{
			"baleia_retention_oldest_partition_start_timestamp_seconds|stream=audit",
			"baleia_retention_oldest_partition_start_timestamp_seconds|stream=query_history",
			"baleia_retention_allowed_age_seconds|stream=audit",
			"baleia_retention_allowed_age_seconds|stream=query_history",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("no %s in\n%s", want, body)
			}
		}
	})
	for name, tc := range map[string]struct {
		allowance bool
		store     storeWith
	}{
		"no allowance for the stream": {false, storeWith{parts: map[string][]partitioning.Partition{"audit_events": {day(1)}}}},
		"no partitions":               {true, storeWith{}},
		"an unreadable listing":       {true, storeWith{err: errors.New("db down")}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reg := metrics.New("worker")
			a := &Activities{store: tc.store}
			a.SetMetrics(reg)
			if tc.allowance {
				a.SetRetentionAllowance("audit", time.Hour)
			}
			a.observeRetention(context.Background(), "audit", "audit_events")
			if body := scrapeBody(t, reg); strings.Contains(body, "baleia_retention_") {
				t.Errorf("exported a gauge it should not have:\n%s", body)
			}
		})
	}
}
