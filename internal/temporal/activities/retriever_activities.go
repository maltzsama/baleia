package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/robfig/cron"
	"go.temporal.io/sdk/activity"
)

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

	retriever, err := a.queries.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{
		ID:          retrieverID,
		WorkspaceID: pgtype.UUID{},
	})
	if err != nil {
		return fmt.Errorf("get retriever: %w", err)
	}

	start := time.Now()
	sql := fmt.Sprintf("REFRESH MATERIALIZED VIEW %s.%s.%s",
		retriever.DestCatalog, retriever.DestNamespace, retriever.DestTable)

	logger.Info("refreshing materialized view", "sql", sql)

	result, err := a.trino.Execute(ctx, sql, retriever.DestCatalog, retriever.DestNamespace)
	durationMs := time.Since(start).Milliseconds()

	status := "finished"
	var rowsProcessed *int64
	var errorMsg *string

	if err != nil {
		status = "failed"
		msg := err.Error()
		errorMsg = &msg
	} else {
		rowsProcessed = &result.RowCount
	}

	schedule, cronErr := cron.Parse(retriever.CronSchedule)
	if cronErr == nil {
		nextRun := schedule.Next(time.Now())
		a.queries.UpdateRetrieverAfterRefresh(ctx, db.UpdateRetrieverAfterRefreshParams{
			ID:            retrieverID,
			Status:        status,
			NextRefreshAt: pgtype.Timestamptz{Time: nextRun, Valid: true},
		})
	}

	history, err := a.queries.CreateRetrieverRefreshHistory(ctx, db.CreateRetrieverRefreshHistoryParams{
		RetrieverID:         retrieverID,
		TemporalWorkflowID:  activity.GetInfo(ctx).WorkflowExecution.ID,
		RefreshType:         params.RefreshType,
		Status:              status,
	})
	if err != nil {
		return fmt.Errorf("create refresh history: %w", err)
	}

	a.queries.UpdateRetrieverRefreshHistory(ctx, db.UpdateRetrieverRefreshHistoryParams{
		ID:            history.ID,
		Status:        status,
		RowsProcessed: rowsProcessed,
		DurationMs:    &durationMs,
		ErrorMessage:  errorMsg,
	})

	if err != nil {
		return fmt.Errorf("refresh failed: %w", err)
	}

	return nil
}

type RetrieverPollParams struct{}

func RetrieverPoll(ctx context.Context, params RetrieverPollParams) error {
	return defaultActivities.retrieverPoll(ctx)
}

func (a *Activities) retrieverPoll(ctx context.Context) error {
	logger := activity.GetLogger(ctx)

	due, err := a.queries.ListRetrieversDue(ctx)
	if err != nil {
		return fmt.Errorf("list due: %w", err)
	}

	for _, r := range due {
		logger.Info("retriever due for refresh", "name", r.Name, "id", r.ID)
	}

	return nil
}
