package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/monitoring"
	"github.com/galileostudio/baleia/internal/queryengine"
	"go.temporal.io/sdk/activity"
)

// healthProbeTimeout bounds each read of a table's metadata tables.
const healthProbeTimeout = time.Minute

// maxTableProperties bounds the property rows read for one table.
const maxTableProperties = 10_000

// engineOnlyProperties are the rows Trino's $properties adds to the table's
// own properties. They are dropped so a table stores the same properties
// whether its catalog or the engine was read; format-version and
// current-snapshot-id are read before that.
var engineOnlyProperties = []string{"format", "format-version", "provider", "current-snapshot-id", "location", "created-at"}

// engineTableHealth records the health of a metastore catalog's Iceberg
// tables (#997). The provider lists the tables and tells their format; the
// engine that binds the catalog with the Iceberg connector reads each one's
// metadata tables, read-only and as the service identity. A pass reads at
// most the configured number of tables, in the provider's order; the tables
// past it are skipped and logged.
func (a *Activities) engineTableHealth(ctx context.Context, h *healthPass, reader connections.MetadataReader, catalogs []connections.CatalogRef, target string, params LensAnalyzeTableHealthParams) error {
	cat, err := a.monitoring().GetCatalogByID(ctx, h.catalogID)
	if err != nil {
		return fmt.Errorf("get catalog: %w", err)
	}
	if cat.Connector == nil || *cat.Connector != connections.IcebergConnector {
		return fmt.Errorf("table health reads catalog %s through the engine only with the Iceberg connector: %w", cat.Name, connections.ErrUnsupported)
	}
	walker, ok := reader.(connections.CatalogWalker)
	if !ok {
		return fmt.Errorf("catalog %s cannot list its tables with their format: %w", cat.Name, connections.ErrUnsupported)
	}
	bindings, err := a.monitoring().ListBindingsByCatalog(ctx, h.catalogID)
	if err != nil {
		return fmt.Errorf("list bindings: %w", err)
	}
	b, ok := monitoring.MaintenanceBinding(ctx, bindings, a.capableEngine)
	if !ok {
		return fmt.Errorf("catalog %s has no enabled binding on an engine with Iceberg maintenance: %w", cat.Name, connections.ErrUnsupported)
	}
	eng, err := a.engines.Get(ctx, b.EngineID)
	if err != nil {
		return fmt.Errorf("get engine: %w", err)
	}
	maintainer, ok := queryengine.As[queryengine.IcebergMaintainer](eng)
	if !ok {
		return fmt.Errorf("engine of catalog %s cannot read Iceberg metadata tables: %w", cat.Name, connections.ErrUnsupported)
	}
	// The binding that reads the tables also counts small files and
	// manifests for the tables with parameters.
	h.engine, h.binding = eng, b

	limit := a.cfg.TableHealthEngineTableLimit()
	var read atomic.Int64
	defer tableHealthHeartbeats(ctx, &read)()
	var recorded, skipped int
	var firstErr, storeErr error
	recordErr := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, c := range catalogs {
		if target != "" && c.Name != target {
			continue
		}
		err := walker.WalkCatalog(ctx, c.Name, func(t connections.TableMeta) error {
			if t.Format != "iceberg" || t.Ref.Unsupported.Is() {
				return nil // Hive and Delta Lake tables of the same metastore get no health
			}
			namespace := strings.Join(t.Ref.Namespace, ".")
			if (params.Namespace != "" && namespace != params.Namespace) || (params.Table != "" && t.Ref.Name != params.Table) {
				return nil
			}
			if int(read.Load()) >= limit {
				skipped++
				return nil
			}
			read.Add(1)
			table := append(append([]string{b.EngineCatalogName}, t.Ref.Namespace...), t.Ref.Name)
			count, m, err := a.engineSnapshotMetrics(ctx, eng, maintainer, table, h.now)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				recordErr(fmt.Errorf("table %s.%s.%s: %w", c.Name, namespace, t.Ref.Name, err))
				return nil
			}
			if err := h.save(ctx, c.Name, namespace, t.Ref.Name, count, m); err != nil {
				storeErr = err
				return err
			}
			recorded++
			return nil
		})
		if storeErr != nil {
			return storeErr
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			recordErr(fmt.Errorf("tables of catalog %s: %w", c.Name, err))
		}
	}

	// As for a catalog read: a pass that failed everywhere is an error, and a
	// partial one is logged so the failed tables are not silently dropped.
	if recorded == 0 && firstErr != nil {
		return fmt.Errorf("table health pass recorded no tables: %w", firstErr)
	}
	if firstErr != nil {
		a.logger.WarnContext(ctx, "table health pass had failures", "catalog", params.Catalog, "err", firstErr)
	}
	if skipped > 0 {
		a.logger.WarnContext(ctx, "table health pass reached its table limit",
			"catalog", params.Catalog, "limit", limit, "skipped", skipped)
	}
	return nil
}

// tableHealthHeartbeats keeps the activity heartbeating every heartbeatInterval
// until the returned stop is called, also while one table's metadata reads
// block: each can take up to healthProbeTimeout, longer than the workflow's
// heartbeat timeout. The details are the number of tables read so far.
func tableHealthHeartbeats(ctx context.Context, read *atomic.Int64) (stop func()) {
	if !activity.IsActivity(ctx) {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				recordHeartbeat(ctx, read.Load())
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// engineSnapshotMetrics reads one Iceberg table's health from its metadata
// tables through the engine: the same metrics a catalog's metadata gives.
func (a *Activities) engineSnapshotMetrics(ctx context.Context, eng queryengine.Engine, maintainer queryengine.IcebergMaintainer, table []string, now time.Time) (int32, snapshotMetrics, error) {
	stmts, err := maintainer.IcebergHealthStatements(table, now)
	if err != nil {
		return 0, snapshotMetrics{}, err
	}
	spec := queryengine.SessionSpec{ReadOnly: true, Tags: queryengine.Tags{Source: "table_health"}}
	read := func(sql string, maxRows int64) ([][]string, error) {
		qctx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
		defer cancel()
		return queryengine.CollectStringsWith(qctx, eng, spec, sql, maxRows, queryengine.Callbacks{})
	}

	rows, err := read(stmts.Properties, maxTableProperties)
	if err != nil {
		return 0, snapshotMetrics{}, fmt.Errorf("properties: %w", err)
	}
	props := make(map[string]string, len(rows))
	for _, r := range rows {
		if len(r) >= 2 {
			props[r[0]] = r[1]
		}
	}
	formatVersion, _ := strconv.Atoi(props["format-version"])
	for _, k := range engineOnlyProperties {
		delete(props, k)
	}

	rows, err = read(stmts.Current, 1)
	if err != nil {
		return 0, snapshotMetrics{}, fmt.Errorf("current snapshot: %w", err)
	}
	var summary map[string]string
	if len(rows) > 0 && len(rows[0]) >= 2 {
		if err := json.Unmarshal([]byte(rows[0][1]), &summary); err != nil {
			return 0, snapshotMetrics{}, fmt.Errorf("current snapshot summary: %w", err)
		}
	}

	rows, err = read(stmts.Snapshots, 1)
	if err != nil {
		return 0, snapshotMetrics{}, fmt.Errorf("snapshots: %w", err)
	}
	if len(rows) != 1 || len(rows[0]) < 4 {
		return 0, snapshotMetrics{}, errors.New("snapshots: the engine returned no statistics")
	}
	count, err := strconv.ParseInt(rows[0][0], 10, 64)
	if err != nil {
		return 0, snapshotMetrics{}, fmt.Errorf("snapshot count %q: %w", rows[0][0], err)
	}

	m := summaryMetrics(props, formatVersion, summary)
	if count == 0 {
		return 0, m, nil // like a catalog read: no snapshot log, no commits or age
	}
	oldestMs, errOldest := strconv.ParseInt(rows[0][1], 10, 64)
	hour, errHour := strconv.ParseInt(rows[0][2], 10, 64)
	day, errDay := strconv.ParseInt(rows[0][3], 10, 64)
	if err := errors.Join(errOldest, errHour, errDay); err != nil {
		return 0, snapshotMetrics{}, fmt.Errorf("snapshot statistics %v: %w", rows[0], err)
	}
	commitsHour, commitsDay := clampInt32(hour), clampInt32(day)
	oldest := int32(now.Sub(time.UnixMilli(oldestMs)).Hours())
	m.commitsHour, m.commitsDay, m.oldestAgeHours = &commitsHour, &commitsDay, &oldest
	return clampInt32(count), m, nil
}
