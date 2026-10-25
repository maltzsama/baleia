package api

import (
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/retrievers"
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
	// retrievers materializes definitions on a schedule (#118).
	retrievers *retrievers.Service
}

// newQueryHandlers builds the group; a nil ticket store builds one.
func newQueryHandlers(c *core, tickets *wsticket.Store) *QueryHandlers {
	if tickets == nil {
		tickets = wsticket.New()
	}
	// Retrievers refresh on the default Trino engine. A nil registry (some
	// tests) must reach the services as a nil interface, not a typed nil.
	var (
		trino    dialect.Dialect
		dialects executions.Dialects
	)
	if c.dialects != nil {
		trino, _ = c.dialects.Get(dialect.Trino)
		dialects = c.dialects
	}
	f := folders.New(c.queries)
	return &QueryHandlers{
		core:       c,
		wsTickets:  tickets,
		folders:    f,
		saved:      savedqueries.NewService(c.queries, f, c.contentService()),
		retrievers: retrievers.New(c.queries, c.temporalClient, c.engines, trino),
		executions: executions.New(c.queries, c.temporalClient, c.engines, dialects, executions.Config{
			StatementTimeout: c.settings.Execution.StatementTimeout,
			ExecutionTimeout: c.settings.Execution.ExecutionTimeout,
		}),
	}
}
