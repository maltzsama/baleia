package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/catalog_connections"
	credcat "github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/sqlsplitter"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/activity"
)

type LensPollTrinoQueriesParams struct{}

func LensPollTrinoQueries(ctx context.Context, params LensPollTrinoQueriesParams) error {
	return defaultActivities.lensPollTrinoQueries(ctx)
}

func (a *Activities) lensPollTrinoQueries(ctx context.Context) error {
	logger := activity.GetLogger(ctx)

	queries, err := a.trino.ListQueries(ctx)
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
		if sqlsplitter.IsCatalogDDL(q.Query) {
			continue
		}
		// The cluster poller's own node-count statement is not user activity.
		if q.Query == trino.ClusterNodesSQL {
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
	return defaultActivities.lensPollTrinoCluster(ctx)
}

func (a *Activities) lensPollTrinoCluster(ctx context.Context) error {
	stats, err := a.trino.GetClusterStats(ctx)
	if err != nil {
		return err
	}

	var totalMem *int64
	var freeMem *int64

	rawPayload, _ := json.Marshal(stats)

	_, err = a.queries.InsertClusterSnapshot(ctx, db.InsertClusterSnapshotParams{
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
	CatalogConnectionID string
	Catalog             string
}

type ListEnabledIcebergCatalogsParams struct{}

// EnabledIcebergCatalog is one target for the table-health fan-out.
type EnabledIcebergCatalog struct {
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
	var rawCreds *string
	if plain != nil {
		s := string(plain)
		rawCreds = &s
	}
	client, err := catalog_connections.NewClient(conn.Type, conn.Url, rawCreds)
	if err != nil {
		return err
	}

	catalogs, err := client.ListCatalogs(ctx)
	if err != nil {
		return fmt.Errorf("list catalogs: %w", err)
	}

	for _, cat := range catalogs {
		if params.Catalog != "" && cat.Name != params.Catalog {
			continue
		}

		namespaces, err := client.ListNamespaces(ctx, cat.Name)
		if err != nil {
			continue
		}

		for _, ns := range namespaces {
			nsName := strings.Join(ns, ".")
			tables, err := client.ListTables(ctx, cat.Name, nsName)
			if err != nil {
				continue
			}

			for _, tbl := range tables {
				meta, err := client.LoadTable(ctx, cat.Name, nsName, tbl.Name)
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
}

func LensRunMaintenance(ctx context.Context, params LensRunMaintenanceParams) error {
	return defaultActivities.lensRunMaintenance(ctx, params)
}

func (a *Activities) lensRunMaintenance(ctx context.Context, params LensRunMaintenanceParams) error {
	logger := activity.GetLogger(ctx)

	commands := []string{
		fmt.Sprintf("ALTER TABLE %s.%s.%s EXECUTE optimize",
			params.Catalog, params.Namespace, params.Table),
		fmt.Sprintf("ALTER TABLE %s.%s.%s EXECUTE expire_snapshots(retention_threshold => '%s')",
			params.Catalog, params.Namespace, params.Table, params.Retention),
		fmt.Sprintf("ALTER TABLE %s.%s.%s EXECUTE remove_orphan_files(retention_threshold => '%s')",
			params.Catalog, params.Namespace, params.Table, params.Retention),
	}

	// Heartbeats make the run cancellable: without them a long OPTIMIZE is
	// declared dead after the heartbeat timeout and re-executed.
	var lastBeat time.Time
	opts := trino.ExecuteOptions{
		Catalog: params.Catalog,
		Schema:  params.Namespace,
		OnProgress: func() {
			now := time.Now()
			if now.Sub(lastBeat) >= heartbeatInterval {
				lastBeat = now
				activity.RecordHeartbeat(ctx)
			}
		},
	}

	for _, cmd := range commands {
		logger.Info("running maintenance", "sql", cmd)

		result, err := a.trino.ExecuteWithOptions(ctx, cmd, opts)
		if err != nil {
			recordMaintenanceSnapshot(a.queries, ctx, params.ExecutionID, cmd, "failed", 0, 0, err.Error())
			return fmt.Errorf("maintenance %q: %w", cmd, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		recordMaintenanceSnapshot(a.queries, ctx, params.ExecutionID, cmd, "finished", result.RowCount, 0, "")
	}

	return nil
}

func recordMaintenanceSnapshot(queries db.Querier, ctx context.Context, executionID, sql, state string, rows int64, elapsedMs int32, errMsg string) {
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
