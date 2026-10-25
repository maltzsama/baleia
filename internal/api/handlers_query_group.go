package api

import (
	"github.com/galileostudio/baleia/internal/workbench/executions"
	"github.com/galileostudio/baleia/internal/workbench/folders"
	"github.com/galileostudio/baleia/internal/workbench/savedqueries"
	"github.com/galileostudio/baleia/internal/wsticket"
)

// QueryHandlers serves the query editor, saved queries, results and retrievers (#365).
type QueryHandlers struct {
	*core
	// wsTickets issues the single-use WebSocket tickets (#26); the WS handler
	// shares this store.
	wsTickets *wsticket.Store
	// folders organizes saved queries in a space.
	folders *folders.Service
	// saved manages saved queries; writes go through content (#34).
	saved *savedqueries.Service
	// executions runs SQL through the execution workflow and reads the runs.
	executions *executions.Service
}

// newQueryHandlers builds the group; a nil ticket store builds one.
func newQueryHandlers(c *core, tickets *wsticket.Store) *QueryHandlers {
	if tickets == nil {
		tickets = wsticket.New()
	}
	f := folders.New(c.queries)
	return &QueryHandlers{
		core:      c,
		wsTickets: tickets,
		folders:   f,
		saved:     savedqueries.NewService(c.queries, f, c.contentService()),
		executions: executions.New(c.queries, c.temporalClient, c.engines, c.dialects, executions.Config{
			StatementTimeout: c.settings.Execution.StatementTimeout,
			ExecutionTimeout: c.settings.Execution.ExecutionTimeout,
		}),
	}
}
