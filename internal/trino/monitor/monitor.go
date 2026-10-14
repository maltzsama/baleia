// Package monitor is the Trino cluster monitor Lens reads (#20): the query
// list and cluster stats of a coordinator. It is a Trino-specific extension,
// not part of the engine contract.
package monitor

import (
	"context"

	"github.com/galileostudio/baleia/internal/trino"
)

// QueryInfo is one entry of Trino's query list.
type QueryInfo = trino.QueryInfo

// ClusterStats is the coordinator's cluster summary.
type ClusterStats = trino.ClusterStats

// ClusterNodesSQL is the statement the cluster poller runs; Lens excludes it
// from user activity.
const ClusterNodesSQL = trino.ClusterNodesSQL

// Monitor reads a coordinator's runtime state.
type Monitor struct{ c *trino.Client }

// New returns a monitor over a client built at a wiring site.
func New(c *trino.Client) *Monitor { return &Monitor{c: c} }

// ListQueries returns the coordinator's query list.
func (m *Monitor) ListQueries(ctx context.Context) ([]QueryInfo, error) { return m.c.ListQueries(ctx) }

// ClusterStats returns the coordinator's cluster summary.
func (m *Monitor) ClusterStats(ctx context.Context) (*ClusterStats, error) {
	return m.c.GetClusterStats(ctx)
}
