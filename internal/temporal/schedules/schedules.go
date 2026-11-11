// Package schedules ensures recurring Temporal workflows run on Schedules —
// the only recurring-job mechanism in the codebase. Workflows driven by a
// schedule must do a single pass and return; infinite loops are forbidden.
package schedules

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// Spec describes one recurring job: a single-pass workflow launched on an
// interval or on a calendar. Exactly one of Every and Cron is set.
type Spec struct {
	ID string
	// Workflow is the workflow function to run on each schedule tick.
	Workflow any
	// Args are the workflow arguments, if any.
	Args []any
	// Every is the interval between runs.
	Every time.Duration
	// Cron is a five-field cron expression (minute, hour, day of month, month,
	// day of week) read in UTC, for a job that has to run at a given weekday and
	// hour, which a fixed interval cannot pin (ADR 0026).
	Cron string
}

// validate rejects a spec with neither or both ways of saying when, and a Cron
// that does not parse, so a bad value fails the worker at boot instead of
// leaving a job that never runs.
func (s Spec) validate() error {
	if s.ID == "" || s.Workflow == nil {
		return errors.New("schedule: id and workflow are required")
	}
	switch {
	case s.Every > 0 && s.Cron != "":
		return fmt.Errorf("schedule %s: set every or cron, not both", s.ID)
	case s.Every <= 0 && s.Cron == "":
		return fmt.Errorf("schedule %s: every or cron is required", s.ID)
	case s.Cron != "":
		if _, err := cron.ParseStandard(s.Cron); err != nil {
			return fmt.Errorf("schedule %s: cron %q: %w", s.ID, s.Cron, err)
		}
	}
	return nil
}

// scheduleSpec is the Temporal form of when the spec runs.
func (s Spec) scheduleSpec() client.ScheduleSpec {
	if s.Cron != "" {
		return client.ScheduleSpec{CronExpressions: []string{s.Cron}, TimeZoneName: "UTC"}
	}
	return client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: s.Every}}}
}

// Ensure creates each schedule if it is absent and updates it when its
// interval, calendar or workflow differs. It is safe to call on every worker boot and
// from several workers concurrently: a create collision is treated as success
// and followed by an idempotent update.
func Ensure(ctx context.Context, c client.Client, taskQueue string, specs []Spec) error {
	for _, s := range specs {
		if err := s.validate(); err != nil {
			return err
		}

		action := &client.ScheduleWorkflowAction{
			ID:        s.ID + "-run",
			Workflow:  s.Workflow,
			Args:      s.Args,
			TaskQueue: taskQueue,
		}
		spec := s.scheduleSpec()

		_, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
			ID:      s.ID,
			Spec:    spec,
			Action:  action,
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		})
		if err == nil {
			continue
		}
		if !errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
			return err
		}

		handle := c.ScheduleClient().GetHandle(ctx, s.ID)
		if err := handle.Update(ctx, client.ScheduleUpdateOptions{
			DoUpdate: func(_ client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
				return &client.ScheduleUpdate{
					Schedule: &client.Schedule{
						Spec:   &spec,
						Action: action,
						// convertToPBSchedule dereferences these; leaving them
						// nil panics the worker.
						Policy: &client.SchedulePolicies{
							Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
						},
						State: &client.ScheduleState{},
					},
				}, nil
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

// IDs of the recurring jobs the worker ensures; metrics label them by these
// (baleia_schedule_last_success_timestamp_seconds{schedule}).
const (
	LensQueryPoller       = "lens-query-poller"
	LensClusterPoller     = "lens-cluster-poller"
	LensTableHealth       = "lens-table-health"
	RetrieverScheduler    = "retriever-scheduler"
	MetadataSync          = "metadata-sync"
	AuditPartitions       = "audit-partitions"
	AuditExport           = "audit-export"
	AuditOffload          = "audit-offload"
	AuditRetention        = "audit-retention"
	QueryHistoryOffload   = "query-history-offload"
	QueryHistoryRetention = "query-history-retention"
	CatalogReconcile      = "catalog-reconcile"
	// EphemeralCatalogSweeper drops expired connection-test catalogs (#12).
	EphemeralCatalogSweeper = "ephemeral-catalog-sweeper"
	// TokenCleanup prunes expired refresh and password-reset tokens (#51).
	TokenCleanup = "token-cleanup"
	// DirectoryReconcile suspends/ reinstates users from the LDAP directory
	// (#257).
	DirectoryReconcile = "directory-reconcile"
	// TablePopularity recomputes the popular tables of every workspace (#562).
	TablePopularity = "table-popularity"
)

// Remove deletes a schedule an earlier configuration created; a schedule
// that does not exist is not an error. Opt-in jobs use it when turned off.
func Remove(ctx context.Context, c client.Client, id string) error {
	err := c.ScheduleClient().GetHandle(ctx, id).Delete(ctx)
	var nf *serviceerror.NotFound
	if err == nil || errors.As(err, &nf) {
		return nil
	}
	return err
}
