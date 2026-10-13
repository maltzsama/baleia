package activities

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/robfig/cron"
	"go.temporal.io/sdk/activity"
)

// RetrieverDue is one retriever the dispatcher must start a refresh for.
type RetrieverDue struct {
	ID                string
	NextRefreshAtUnix int64
}

type RetrieverDispatchParams struct{}

// RetrieverDispatch lists retrievers whose next run is due. It is the single
// pass the retriever-scheduler Temporal Schedule drives.
func RetrieverDispatch(ctx context.Context, params RetrieverDispatchParams) ([]RetrieverDue, error) {
	return defaultActivities.retrieverDispatch(ctx)
}

func (a *Activities) retrieverDispatch(ctx context.Context) ([]RetrieverDue, error) {
	due, err := a.queries.ListRetrieversDue(ctx)
	if err != nil {
		return nil, fmt.Errorf("list due retrievers: %w", err)
	}

	out := make([]RetrieverDue, 0, len(due))
	for _, r := range due {
		d := RetrieverDue{ID: r.ID.String()}
		if r.NextRefreshAt.Valid {
			d.NextRefreshAtUnix = r.NextRefreshAt.Time.Unix()
		}
		out = append(out, d)
	}
	return out, nil
}

type AdvanceRetrieverScheduleParams struct {
	RetrieverID string
}

// AdvanceRetrieverSchedule computes the next run from the retriever's cron and
// stores it, so a long refresh does not cause a burst of runs once it finishes.
func AdvanceRetrieverSchedule(ctx context.Context, params AdvanceRetrieverScheduleParams) error {
	return defaultActivities.advanceRetrieverSchedule(ctx, params)
}

func (a *Activities) advanceRetrieverSchedule(ctx context.Context, params AdvanceRetrieverScheduleParams) error {
	var retrieverID pgtype.UUID
	if err := retrieverID.Scan(params.RetrieverID); err != nil {
		return err
	}

	retriever, err := a.queries.GetRetrieverForRefresh(ctx, retrieverID)
	if err != nil {
		return fmt.Errorf("get retriever: %w", err)
	}

	schedule, err := cron.Parse(retriever.CronSchedule)
	if err != nil {
		return fmt.Errorf("parse cron %q: %w", retriever.CronSchedule, err)
	}
	next := schedule.Next(time.Now())

	return a.queries.AdvanceRetrieverSchedule(ctx, db.AdvanceRetrieverScheduleParams{
		ID:            retrieverID,
		NextRefreshAt: pgtype.Timestamptz{Time: next, Valid: true},
	})
}

type RetrieverRefreshParams struct {
	RetrieverID string
	RefreshType string
}

func RetrieverRefresh(ctx context.Context, params RetrieverRefreshParams) error {
	return defaultActivities.retrieverRefresh(ctx, params)
}

func (a *Activities) retrieverRefresh(ctx context.Context, params RetrieverRefreshParams) error {
	logger := activity.GetLogger(ctx)

	var retrieverID pgtype.UUID
	if err := retrieverID.Scan(params.RetrieverID); err != nil {
		return err
	}

	retriever, err := a.queries.GetRetrieverForRefresh(ctx, retrieverID)
	if err != nil {
		return fmt.Errorf("get retriever: %w", err)
	}

	// Record the run as started before executing; any failure after this point
	// is reflected by the failed-status update below (or Temporal retries with
	// MaximumAttempts=1, leaving a visible running row).
	history, err := a.queries.CreateRetrieverRefreshHistory(ctx, db.CreateRetrieverRefreshHistoryParams{
		RetrieverID:        retrieverID,
		TemporalWorkflowID: activity.GetInfo(ctx).WorkflowExecution.ID,
		RefreshType:        params.RefreshType,
		Status:             "running",
	})
	if err != nil {
		return fmt.Errorf("create refresh history: %w", err)
	}

	// v1 refresh: one atomic statement replaced under a new Iceberg snapshot.
	sql, err := buildRefreshSQL(retriever.DestCatalog, retriever.DestNamespace, retriever.DestTable, retriever.DefinitionSql)
	if err != nil {
		return fmt.Errorf("retriever %s has an invalid destination: %w", retriever.ID, err)
	}
	logger.Info("refreshing retriever", "sql", sql)

	start := time.Now()
	// Heartbeats let a long refresh receive cancellation and never be re-run.
	var lastBeat time.Time
	opts := trino.ExecuteOptions{
		Catalog: retriever.DestCatalog,
		Schema:  retriever.DestNamespace,
		OnProgress: func() {
			now := time.Now()
			if now.Sub(lastBeat) >= heartbeatInterval {
				lastBeat = now
				activity.RecordHeartbeat(ctx)
			}
		},
	}

	result, err := a.trino.ExecuteWithOptions(ctx, sql, opts)
	durationMs := time.Since(start).Milliseconds()

	status := "finished"
	var rowsProcessed *int64
	var errorMsg *string
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		status = "failed"
		msg := err.Error()
		errorMsg = &msg
	} else {
		rowsProcessed = &result.RowCount
	}

	// A failed refresh flips the retriever to 'error' (the schema only allows
	// active/paused/error); the dispatcher leaves failed retrievers alone until
	// the owner edits them.
	retrieverStatus := "active"
	if errorMsg != nil {
		retrieverStatus = "error"
	}
	if err := a.queries.UpdateRetrieverAfterRefresh(ctx, db.UpdateRetrieverAfterRefreshParams{
		ID:     retrieverID,
		Status: retrieverStatus,
	}); err != nil {
		return fmt.Errorf("update retriever after refresh: %w", err)
	}

	if err := a.queries.UpdateRetrieverRefreshHistory(ctx, db.UpdateRetrieverRefreshHistoryParams{
		ID:            history.ID,
		Status:        status,
		RowsProcessed: rowsProcessed,
		DurationMs:    &durationMs,
		ErrorMessage:  errorMsg,
	}); err != nil {
		return fmt.Errorf("update refresh history: %w", err)
	}

	if errorMsg != nil {
		return fmt.Errorf("refresh failed: %s", *errorMsg)
	}
	return nil
}

// buildRefreshSQL renders the atomic full-refresh statement
// `CREATE OR REPLACE TABLE <dest> AS <definition>` with each identifier
// double-quoted. The definition is a single SELECT/WITH validated at create.
func buildRefreshSQL(catalog, namespace, table, definition string) (string, error) {
	dest, err := trino.QualifiedName(catalog, namespace, table)
	if err != nil {
		return "", err
	}
	def := strings.TrimSuffix(strings.TrimSpace(definition), ";")
	return fmt.Sprintf("CREATE OR REPLACE TABLE %s AS %s", dest, def), nil
}
