package api

import "github.com/galileostudio/baleia/internal/wsticket"

// QueryHandlers serves the query editor, saved queries, results and retrievers (#365).
type QueryHandlers struct {
	*core
	// wsTickets issues the single-use WebSocket tickets (#26); the WS handler
	// shares this store.
	wsTickets *wsticket.Store
}

// newQueryHandlers builds the group; a nil ticket store builds one.
func newQueryHandlers(c *core, tickets *wsticket.Store) *QueryHandlers {
	if tickets == nil {
		tickets = wsticket.New()
	}
	return &QueryHandlers{core: c, wsTickets: tickets}
}
