package metrics

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
)

// WatchDBPool exports the store's connection pool: acquired (in use), idle and
// max connections, labelled by process. BaleiaDBPoolSaturated divides acquired
// by max, so both must keep their names. stats is sql.DB.Stats, read on every
// scrape.
func (r *Registry) WatchDBPool(stats func() sql.DBStats) {
	r.reg.MustRegister(&dbPoolCollector{stats: stats, process: r.process})
}

var (
	dbPoolAcquired = prometheus.NewDesc(Namespace+"_db_pool_acquired_conns", "Store connections in use.", []string{"process"}, nil)
	dbPoolIdle     = prometheus.NewDesc(Namespace+"_db_pool_idle_conns", "Idle store connections.", []string{"process"}, nil)
	dbPoolMax      = prometheus.NewDesc(Namespace+"_db_pool_max_conns", "Store connection limit (0 is unlimited).", []string{"process"}, nil)
)

type dbPoolCollector struct {
	stats   func() sql.DBStats
	process string
}

func (c *dbPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- dbPoolAcquired
	ch <- dbPoolIdle
	ch <- dbPoolMax
}

func (c *dbPoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.stats()
	ch <- prometheus.MustNewConstMetric(dbPoolAcquired, prometheus.GaugeValue, float64(s.InUse), c.process)
	ch <- prometheus.MustNewConstMetric(dbPoolIdle, prometheus.GaugeValue, float64(s.Idle), c.process)
	ch <- prometheus.MustNewConstMetric(dbPoolMax, prometheus.GaugeValue, float64(s.MaxOpenConnections), c.process)
}
