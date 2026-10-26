package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	credcat "github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/policy"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/galileostudio/baleia/internal/trino/monitor"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/activity"
)

// lensMonitor reads the Trino coordinator's runtime state for Lens;
// *trino/monitor.Monitor implements it.
type lensMonitor interface {
	ListQueries(ctx context.Context) ([]monitor.QueryInfo, error)
	ClusterStats(ctx context.Context) (*monitor.ClusterStats, error)
}

type LensPollTrinoQueriesParams struct{}

func LensPollTrinoQueries(ctx context.Context, params LensPollTrinoQueriesParams) error {
	err := defaultActivities.lensPollTrinoQueries(ctx)
	defaultActivities.passDone(schedules.LensQueryPoller, err)
	return err
}

func (a *Activities) lensPollTrinoQueries(ctx context.Context) error {
	logger := activity.GetLogger(ctx)

	eng, err := a.engines.DefaultTrino(ctx)
	if err != nil {
		return err
	}
	engineID := engineUUID(eng.ID())

	queries, err := a.monitor.ListQueries(ctx)
	if err != nil {
		logger.Error("failed to list trino queries", "err", err)
		return err
	}

	for _, q := range queries {
		// I4 (CR-017): discard catalog-management DDL from observability.
		// These statements are infrastructure, not user queries, and (until
		// the placeholder plumbing of I2 is in place) they leak credentials
		// in clear. Even after I2 the discard stays: it is defense in depth
		// and the DDL carries no user-observability value.
		if policy.IsCatalogDDL(q.Query) {
			continue
		}
		// The cluster poller's own node-count statement is not user activity.
		if q.Query == monitor.ClusterNodesSQL {
			continue
		}

		var elapsedMs *int32
		var cpuMs *int32
		var errMsg *string

		if v := q.ElapsedMs(); v != nil {
			val := int32(*v)
			elapsedMs = &val
		}
		if v := q.CPUMs(); v != nil {
			val := int32(*v)
			cpuMs = &val
		}
		physBytes := q.PhysicalInputBytes()
		if msg := q.FailureMessage(); msg != "" {
			errMsg = &msg
		}

		rawPayload, _ := json.Marshal(q)

		createdAt, ok := q.CreatedAt()
		if !ok {
			createdAt = time.Now()
		}

		state := mapTrinoState(q.State)
		origin := "trino_direct"

		_, err := a.queries.UpsertTrinoQuerySnapshot(ctx, db.UpsertTrinoQuerySnapshotParams{
			TrinoQueryID:       q.QueryID,
			EngineID:           engineID,
			Origin:             origin,
			User:               q.User(),
			State:              state,
			Sql:                q.Query,
			Catalog:            strPtr(q.Catalog()),
			Schema:             strPtr(q.Schema()),
			CreatedAt:          pgtype.Timestamptz{Time: createdAt, Valid: true},
			ElapsedMs:          elapsedMs,
			CpuMs:              cpuMs,
			PhysicalInputBytes: physBytes,
			OutputRows:         nil, // not exposed by Trino's query list
			ErrorMessage:       errMsg,
			RawTrinoPayload:    rawPayload,
		})
		if err != nil {
			logger.Error("failed to upsert query snapshot", "query_id", q.QueryID, "err", err)
		}
	}

	return nil
}

type LensPollTrinoClusterParams struct{}

func LensPollTrinoCluster(ctx context.Context, params LensPollTrinoClusterParams) error {
	err := defaultActivities.lensPollTrinoCluster(ctx)
	defaultActivities.passDone(schedules.LensClusterPoller, err)
	return err
}

func (a *Activities) lensPollTrinoCluster(ctx context.Context) error {
	eng, err := a.engines.DefaultTrino(ctx)
	if err != nil {
		return err
	}
	stats, err := a.monitor.ClusterStats(ctx)
	if err != nil {
		return err
	}

	var totalMem *int64
	var freeMem *int64

	rawPayload, _ := json.Marshal(stats)

	_, err = a.queries.InsertClusterSnapshot(ctx, db.InsertClusterSnapshotParams{
		EngineID:         engineUUID(eng.ID()),
		ActiveWorkers:    int32(stats.ActiveWorkers),
		RunningQueries:   int32(stats.RunningQueries),
		QueuedQueries:    int32(stats.QueuedQueries),
		BlockedQueries:   int32(stats.BlockedQueries),
		TotalMemoryBytes: totalMem,
		FreeMemoryBytes:  freeMem,
		RawPayload:       rawPayload,
	})
	return err
}

type LensAnalyzeTableHealthParams struct {
	CatalogID           string
	CatalogConnectionID string
	Catalog             string
}

type ListEnabledIcebergCatalogsParams struct{}

// EnabledIcebergCatalog is one target for the table-health fan-out.
type EnabledIcebergCatalog struct {
	ID                  string
	CatalogConnectionID string
	Catalog             string
	TrinoCatalogName    string
}

func ListEnabledIcebergCatalogs(ctx context.Context, params ListEnabledIcebergCatalogsParams) ([]EnabledIcebergCatalog, error) {
	return defaultActivities.listEnabledIcebergCatalogs(ctx)
}

func (a *Activities) listEnabledIcebergCatalogs(ctx context.Context) ([]EnabledIcebergCatalog, error) {
	rows, err := a.queries.ListEnabledIcebergCatalogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enabled iceberg catalogs: %w", err)
	}

	out := make([]EnabledIcebergCatalog, 0, len(rows))
	for _, r := range rows {
		out = append(out, EnabledIcebergCatalog{
			ID:                  r.ID.String(),
			CatalogConnectionID: r.ConnectionID.String(),
			Catalog:             r.Name,
			TrinoCatalogName:    r.TrinoCatalogName,
		})
	}
	return out, nil
}

func LensAnalyzeTableHealth(ctx context.Context, params LensAnalyzeTableHealthParams) error {
	return defaultActivities.lensAnalyzeTableHealth(ctx, params)
}

func (a *Activities) lensAnalyzeTableHealth(ctx context.Context, params LensAnalyzeTableHealthParams) error {
	var connID pgtype.UUID
	if err := connID.Scan(params.CatalogConnectionID); err != nil {
		return err
	}
	var catalogID pgtype.UUID
	if err := catalogID.Scan(params.CatalogID); err != nil {
		return err
	}

	conn, err := a.queries.GetCatalogConnectionByID(ctx, connID)
	if err != nil {
		return fmt.Errorf("get catalog connection: %w", err)
	}

	// Open the stored envelope; the raw column value is ciphertext and must
	// never reach the catalog client.
	plain, err := credcat.OpenCredentials(conn, a.keyring)
	if err != nil {
		return fmt.Errorf("connection %s: %w", params.CatalogConnectionID, err)
	}
	provider, err := a.connections.Get(connections.Kind(conn.Type))
	if err != nil {
		return err
	}
	if provider.Family() != connections.FamilyIceberg {
		return fmt.Errorf("table health needs an Iceberg catalog, connection is %q: %w", conn.Type, connections.ErrUnsupported)
	}
	cfg, err := providers.FromStored(a.connections, connections.Kind(conn.Type), conn.ID, conn.Url, conn.Config, plain)
	if err != nil {
		return err
	}
	reader, err := provider.OpenMetadata(ctx, cfg)
	if err != nil {
		return err
	}
	defer reader.Close()

	catalogs, err := reader.ListCatalogs(ctx)
	if err != nil {
		return fmt.Errorf("list catalogs: %w", err)
	}

	for _, cat := range catalogs {
		if params.Catalog != "" && cat.Name != params.Catalog {
			continue
		}

		namespaces, err := allNamespaces(ctx, reader, cat.Name)
		if err != nil {
			continue
		}

		for _, ns := range namespaces {
			nsName := strings.Join(ns, ".")
			tables, err := allTables(ctx, reader, cat.Name, ns)
			if err != nil {
				continue
			}

			for _, tbl := range tables {
				meta, err := reader.LoadTable(ctx, tbl)
				if err != nil {
					continue
				}

				snapshotCount := int32(len(meta.Snapshots))
				healthScore := int32(100)
				needsCompaction := false
				needsExpiry := false
				var fileCount *int32
				var totalSize *int64

				if snapshotCount > 50 {
					healthScore -= 30
					needsExpiry = true
				} else if snapshotCount > 20 {
					healthScore -= 10
				}

				if len(meta.Snapshots) > 0 {
					latest := meta.Snapshots[len(meta.Snapshots)-1]

					if totalFilesStr, ok := latest.Summary["total-data-files"]; ok {
						if n, err := strconv.Atoi(totalFilesStr); err == nil {
							fc := int32(n)
							fileCount = &fc
							if fc > 100 {
								healthScore -= 30
								needsCompaction = true
							} else if fc > 50 {
								healthScore -= 10
							}
						}
					}

					if totalSizeStr, ok := latest.Summary["total-files-size"]; ok {
						if n, err := strconv.ParseInt(totalSizeStr, 10, 64); err == nil {
							totalSize = &n
						}
					}
				}

				var oldestAgeHours *int32
				if len(meta.Snapshots) > 0 {
					oldest := meta.Snapshots[0]
					oldestTime := time.UnixMilli(oldest.TimestampMs)
					ageHours := int32(time.Since(oldestTime).Hours())
					oldestAgeHours = &ageHours
					if ageHours > 168 {
						healthScore -= 20
					}
				}

				if healthScore < 0 {
					healthScore = 0
				}

				_, err = a.queries.UpsertTableHealthSnapshot(ctx, db.UpsertTableHealthSnapshotParams{
					CatalogID:              catalogID,
					CatalogConnectionID:    connID,
					Catalog:                cat.Name,
					Namespace:              nsName,
					TableName:              tbl.Name,
					SnapshotCount:          snapshotCount,
					FileCount:              fileCount,
					TotalSizeBytes:         totalSize,
					OldestSnapshotAgeHours: oldestAgeHours,
					HealthScore:            healthScore,
					NeedsCompaction:        needsCompaction,
					NeedsSnapshotExpiry:    needsExpiry,
					NeedsOrphanCleanup:     false,
				})
				if err != nil {
					return fmt.Errorf("upsert table health: %w", err)
				}
			}
		}
	}

	return nil
}

// maxNamespaceDepth bounds the walk of nested Iceberg namespaces.
const maxNamespaceDepth = 8

// allNamespaces lists every namespace of a catalog, nested ones included.
func allNamespaces(ctx context.Context, r connections.MetadataReader, catalog string) ([]connections.Namespace, error) {
	var out []connections.Namespace
	var walk func(parent connections.Namespace, depth int) error
	walk = func(parent connections.Namespace, depth int) error {
		if depth > maxNamespaceDepth {
			return nil
		}
		children, err := r.ListNamespaces(ctx, catalog, parent)
		if err != nil {
			return err
		}
		for _, ns := range children {
			out = append(out, ns)
			if err := walk(ns, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return out, walk(nil, 1)
}

// allTables lists every table of a namespace, following pages.
func allTables(ctx context.Context, r connections.MetadataReader, catalog string, ns connections.Namespace) ([]connections.TableRef, error) {
	var out []connections.TableRef
	token := ""
	for {
		page, next, err := r.ListTables(ctx, catalog, ns, token)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if next == "" || next == token {
			return out, nil
		}
		token = next
	}
}

func engineUUID(id string) pgtype.UUID {
	// An unparsable id stays invalid and matches no row.
	var out pgtype.UUID
	_ = out.Scan(id)
	return out
}

func mapTrinoState(s string) string {
	switch s {
	case "RUNNING", "QUEUED", "PLANNING", "STARTING":
		return "running"
	case "FINISHED":
		return "finished"
	case "FAILED":
		return "failed"
	default:
		return "running"
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type LensRunMaintenanceParams struct {
	ExecutionID string
	Catalog     string
	Namespace   string
	Table       string
	Retention   string
	// RunID and EngineID are set by the engine-aware /maintenance endpoint;
	// empty for the legacy Lens route, which records no maintenance_runs row.
	RunID      string
	EngineID   string
	Operations []string
}

func LensRunMaintenance(ctx context.Context, params LensRunMaintenanceParams) error {
	return defaultActivities.lensRunMaintenance(ctx, params)
}

func (a *Activities) lensRunMaintenance(ctx context.Context, params LensRunMaintenanceParams) error {
	logger := activity.GetLogger(ctx)

	eng, commands, selected, err := a.planMaintenance(ctx, params)
	if err != nil {
		// Nothing ran: the run row must not stay "running".
		finishMaintenanceRun(a.queries, ctx, params.RunID, "failed", "maintenance_failed", err.Error())
		return err
	}
	engineID := engineUUID(params.EngineID)

	// Heartbeats make the run cancellable: without them a long OPTIMIZE is
	// declared dead after the heartbeat timeout and re-executed.
	var lastBeat time.Time
	cb := queryengine.Callbacks{OnProgress: func(queryengine.Progress) {
		now := time.Now()
		if now.Sub(lastBeat) >= heartbeatInterval {
			lastBeat = now
			activity.RecordHeartbeat(ctx)
		}
	}}
	spec := queryengine.SessionSpec{Tags: queryengine.Tags{Source: "maintenance", ExecutionID: params.ExecutionID}}

	names := []string{"optimize", "expire_snapshots", "remove_orphan_files"}

	for i, cmd := range commands {
		if len(selected) > 0 && i < len(names) && !selected[names[i]] {
			continue
		}
		logger.Info("running maintenance", "sql", cmd)

		result, err := queryengine.CollectStringsWith(ctx, eng, spec, cmd, maintenanceMaxResultRows, cb)
		if err != nil {
			recordMaintenanceSnapshot(a.queries, ctx, engineID, params.ExecutionID, cmd, "failed", 0, 0, err.Error())
			finishMaintenanceRun(a.queries, ctx, params.RunID, "failed", "maintenance_failed", err.Error())
			return fmt.Errorf("maintenance %q: %w", cmd, err)
		}
		if ctx.Err() != nil {
			finishMaintenanceRun(a.queries, ctx, params.RunID, "failed", "cancelled", "the run was cancelled")
			return ctx.Err()
		}

		recordMaintenanceSnapshot(a.queries, ctx, engineID, params.ExecutionID, cmd, "finished", int64(len(result)), 0, "")
	}

	finishMaintenanceRun(a.queries, ctx, params.RunID, "finished", "", "")
	return nil
}

// planMaintenance resolves the run's engine and renders its statements.
// The identifiers and retention reach this activity from an HTTP request:
// rendering validates them again (the API checks first, but a workflow
// input is not a trust boundary) and quotes instead of concatenating.
func (a *Activities) planMaintenance(ctx context.Context, params LensRunMaintenanceParams) (queryengine.Engine, []string, map[string]bool, error) {
	eng, err := a.maintenanceEngine(ctx, params.EngineID)
	if err != nil {
		return nil, nil, nil, err
	}
	maintainer, ok := queryengine.As[queryengine.IcebergMaintainer](eng)
	if !ok {
		return nil, nil, nil, fmt.Errorf("engine %s has no Iceberg maintenance", eng.Kind())
	}
	selected := map[string]bool{}
	for _, op := range params.Operations {
		selected[op] = true
	}
	// No selection is the legacy Lens route, which runs all three.
	if params.Retention == "" && (len(selected) == 0 || selected["expire_snapshots"] || selected["remove_orphan_files"]) {
		return nil, nil, nil, errors.New("invalid retention: expire_snapshots and remove_orphan_files need one")
	}
	commands, err := maintainer.MaintenanceStatements([]string{params.Catalog, params.Namespace, params.Table}, params.Retention)
	if err != nil {
		return nil, nil, nil, err
	}
	return eng, commands, selected, nil
}

// maintenanceEngine is the engine a run was planned on; the legacy Lens route
// names none and runs on the default Trino.
func (a *Activities) maintenanceEngine(ctx context.Context, engineID string) (queryengine.Engine, error) {
	if engineID == "" {
		return a.engines.DefaultTrino(ctx)
	}
	id := engineUUID(engineID)
	if !id.Valid {
		return nil, fmt.Errorf("invalid engine id %q", engineID)
	}
	return a.engines.Get(ctx, id)
}

// finishMaintenanceRun records the terminal state of an engine-aware run.
func finishMaintenanceRun(queries db.Querier, ctx context.Context, runID, status, code, message string) {
	if runID == "" {
		return
	}
	var id pgtype.UUID
	if err := id.Scan(runID); err != nil {
		return
	}
	var codePtr, messagePtr *string
	if code != "" {
		codePtr = &code
	}
	if message != "" {
		messagePtr = &message
	}
	// Best effort: the run's own outcome is returned to the workflow.
	_, _ = queries.UpdateMaintenanceRun(context.WithoutCancel(ctx), db.UpdateMaintenanceRunParams{
		ID: id, Status: status, ErrorCode: codePtr, ErrorMessage: messagePtr,
	})
}

// maintenanceMaxResultRows bounds what a maintenance statement may return.
const maintenanceMaxResultRows = 10000

func recordMaintenanceSnapshot(queries db.Querier, ctx context.Context, engineID pgtype.UUID, executionID, sql, state string, rows int64, elapsedMs int32, errMsg string) {
	var execID pgtype.UUID
	execID.Scan(executionID)

	rawPayload, _ := json.Marshal(map[string]string{"sql": sql, "state": state})

	trinoQueryID := fmt.Sprintf("lens-maint-%s-%d", executionID[:8], time.Now().UnixMilli())

	var errorMsg *string
	if errMsg != "" {
		errorMsg = &errMsg
	}

	var outputRows *int64
	if rows > 0 {
		outputRows = &rows
	}

	var elapsed *int32
	if elapsedMs > 0 {
		elapsed = &elapsedMs
	}

	queries.UpsertTrinoQuerySnapshot(ctx, db.UpsertTrinoQuerySnapshotParams{
		TrinoQueryID:    trinoQueryID,
		EngineID:        engineID,
		ExecutionID:     execID,
		Origin:          "lens_maintenance",
		User:            "lens",
		State:           state,
		Sql:             sql,
		Catalog:         strPtr(""),
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
		ElapsedMs:       elapsed,
		OutputRows:      outputRows,
		ErrorMessage:    errorMsg,
		RawTrinoPayload: rawPayload,
	})
}
