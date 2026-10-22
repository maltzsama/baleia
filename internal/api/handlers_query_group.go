package api

import "github.com/galileostudio/baleia/internal/wsticket"

// QueryHandlers serves the query editor, saved queries, results and retrievers (#365).
type QueryHandlers struct {
	*core
	// wsTickets issues the single-use WebSocket tickets (#26); the WS handler
	// shares this store.
	wsTickets *wsticket.Store
}
