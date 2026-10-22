package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/client"
)

// RegisterLensRoutes mounts the Lens routes. The caller wraps them in the
// shared auth middleware; handlers read the caller from the request context.
func RegisterLensRoutes(mux chi.Router, queries db.Querier, cfg *config.Config, temporalClient client.Client, engines engineSource, rec audit.Recorder) {
	if rec == nil {
		rec = audit.Nop{}
	}
	mux.Group(func(r chi.Router) {
		h := &lensHandlers{queries: queries, temporalClient: temporalClient, identityMode: cfg.TrinoUserIdentity, engines: engines, audit: rec}

		r.Get("/lens/queries", h.listQueries)
		r.Get("/lens/queries/{id}", h.getQuery)
		r.Get("/lens/cluster", h.getCluster)
		r.Get("/lens/cluster/history", h.listClusterHistory)
		r.Get("/lens/tables/health", h.listTableHealth)
		r.Get("/lens/tables/health/{conn_id}", h.listTableHealthByConnection)
		r.Get("/lens/analytics/queries", h.queryAnalytics)
		r.Post("/lens/tables/{conn_id}/{catalog}/{namespace}/{table}/maintain", h.triggerMaintenance)
	})
}

type lensHandlers struct {
	queries        db.Querier
	temporalClient client.Client
	identityMode   string // BALEIA_TRINO_USER_IDENTITY
	engines        engineSource
	audit          audit.Recorder // #43
}

// internalError logs err and answers a generic 500: database errors are not
// for clients.
func (h *lensHandlers) internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("lens request failed", "path", r.URL.Path, "request_id", middleware.GetReqID(r.Context()), "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error", "message": "internal error", "request_id": middleware.GetReqID(r.Context())})
}

// visibleUser reports whose queries the caller may see: "" means everyone's
// (global admin), otherwise only the caller's own Trino identity. ok is false
// when the request carries no usable identity (a 401 was written).
func (h *lensHandlers) visibleUser(w http.ResponseWriter, r *http.Request) (user string, ok bool) {
	p, err := authz.PrincipalFromContext(r.Context())
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized", "message": "invalid user"})
		return "", false
	}
	if p.IsAdmin() {
		return "", true
	}
	id, err := trinoIdentity(r.Context(), h.queries, h.identityMode, p.UserID)
	if err != nil {
		h.internalError(w, r, err)
		return "", false
	}
	return id, true
}

func (h *lensHandlers) listQueries(w http.ResponseWriter, r *http.Request) {
	page := 1
	perPage := 20

	user, ok := h.visibleUser(w, r)
	if !ok {
		return
	}
	var snapshots []db.TrinoQuerySnapshot
	var err error
	if user == "" {
		snapshots, err = h.queries.ListTrinoQuerySnapshots(r.Context(), db.ListTrinoQuerySnapshotsParams{
			Limit:  int32(perPage),
			Offset: int32((page - 1) * perPage),
		})
	} else {
		snapshots, err = h.queries.ListTrinoQuerySnapshotsByUser(r.Context(), db.ListTrinoQuerySnapshotsByUserParams{
			User:   user,
			Limit:  int32(perPage),
			Offset: int32((page - 1) * perPage),
		})
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}

	result := make([]map[string]any, len(snapshots))
	for i, s := range snapshots {
		result[i] = snapshotToMap(s)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data":       result,
		"pagination": map[string]int{"page": page, "per_page": perPage, "total": len(result)},
	})
}

func (h *lensHandlers) getQuery(w http.ResponseWriter, r *http.Request) {
	user, ok := h.visibleUser(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	snapshot, err := h.queries.GetTrinoQuerySnapshot(r.Context(), id)
	// Someone else's query is indistinguishable from a missing one.
	if err != nil || (user != "" && snapshot.User != user) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "query not found"})
		return
	}

	writeJSON(w, http.StatusOK, snapshotToMap(snapshot))
}

func (h *lensHandlers) getCluster(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.queries.GetLatestClusterSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "no cluster data available"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active_workers":     snapshot.ActiveWorkers,
		"running_queries":    snapshot.RunningQueries,
		"queued_queries":     snapshot.QueuedQueries,
		"blocked_queries":    snapshot.BlockedQueries,
		"total_memory_bytes": snapshot.TotalMemoryBytes,
		"free_memory_bytes":  snapshot.FreeMemoryBytes,
		"captured_at":        snapshot.CapturedAt,
	})
}

func (h *lensHandlers) listClusterHistory(w http.ResponseWriter, r *http.Request) {
	snapshots, err := h.queries.ListClusterSnapshots(r.Context(), db.ListClusterSnapshotsParams{
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		h.internalError(w, r, err)
		return
	}

	result := make([]map[string]any, len(snapshots))
	for i, s := range snapshots {
		result[i] = map[string]any{
			"id":              s.ID,
			"captured_at":     s.CapturedAt,
			"active_workers":  s.ActiveWorkers,
			"running_queries": s.RunningQueries,
			"queued_queries":  s.QueuedQueries,
			"blocked_queries": s.BlockedQueries,
		}
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *lensHandlers) listTableHealth(w http.ResponseWriter, r *http.Request) {
	snapshots, err := h.queries.ListTableHealthSnapshots(r.Context(), db.ListTableHealthSnapshotsParams{
		Limit:  50,
		Offset: 0,
	})
	if err != nil {
		h.internalError(w, r, err)
		return
	}

	result := make([]map[string]any, len(snapshots))
	for i, s := range snapshots {
		result[i] = healthToMap(s)
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *lensHandlers) listTableHealthByConnection(w http.ResponseWriter, r *http.Request) {
	connID := chi.URLParam(r, "conn_id")
	var uid pgtype.UUID
	if err := uid.Scan(connID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid connection id"})
		return
	}

	snapshots, err := h.queries.ListTableHealthByConnection(r.Context(), db.ListTableHealthByConnectionParams{
		CatalogConnectionID: uid,
		Limit:               50,
		Offset:              0,
	})
	if err != nil {
		h.internalError(w, r, err)
		return
	}

	result := make([]map[string]any, len(snapshots))
	for i, s := range snapshots {
		result[i] = healthToMap(s)
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *lensHandlers) triggerMaintenance(w http.ResponseWriter, r *http.Request) {
	p, err := authz.PrincipalFromContext(r.Context())
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized", "message": "invalid user"})
		return
	}
	ev := audit.Event{Action: audit.MaintenanceTrigger, ResourceType: "connection", ResourceID: chi.URLParam(r, "conn_id")}
	record := func(e audit.Event) { _ = h.audit.Record(r.Context(), e) }
	catalog := chi.URLParam(r, "catalog")
	namespace := chi.URLParam(r, "namespace")
	table := chi.URLParam(r, "table")

	// The connection owner (or a global admin) may maintain its tables; anyone
	// else is told the connection does not exist. Checked before any validation
	// that would reveal more.
	var connID pgtype.UUID
	if err := connID.Scan(chi.URLParam(r, "conn_id")); err != nil || !connID.Valid {
		record(ev.Denied(audit.ReasonNotFound))
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "catalog connection not found"})
		return
	}
	if _, err := authz.New(h.queries).Connection(r.Context(), p, connID, authz.ActWrite); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			record(ev.Denied(audit.ReasonNotFound))
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "catalog connection not found"})
			return
		}
		h.internalError(w, r, err)
		return
	}

	// {catalog} names a catalog of THIS connection (its Trino name, else its
	// display name); its stored Trino name is what reaches the SQL.
	cats, err := h.queries.ListCatalogsByConnection(r.Context(), connID)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	trinoName := ""
	for _, c := range cats {
		if c.TrinoName() != "" && c.TrinoName() == catalog {
			trinoName = c.TrinoName()
			break
		}
	}
	if trinoName == "" {
		for _, c := range cats {
			if c.Name == catalog {
				trinoName = c.TrinoName()
				break
			}
		}
	}
	if trinoName == "" {
		record(ev.Failed(audit.ReasonNotFound))
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "catalog not found on this connection"})
		return
	}

	eng, err := h.engines.DefaultTrino(r.Context())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	maintainer, ok := queryengine.As[queryengine.IcebergMaintainer](eng)
	if !ok || !eng.Capabilities().IcebergMaintenance {
		record(ev.Failed(audit.ReasonValidation))
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "engine_unsupported", "message": "this engine has no Iceberg maintenance"})
		return
	}
	if _, err := eng.Dialect().QualifiedName(trinoName, namespace, table); err != nil {
		record(ev.Failed(audit.ReasonValidation))
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_identifier", "message": "invalid namespace or table"})
		return
	}

	var body struct {
		Retention string `json:"retention"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Retention == "" {
		body.Retention = "7d"
	}
	// Rendering the statements validates the retention before anything runs;
	// the worker renders them again (a workflow input is not a trust boundary).
	if _, err := maintainer.MaintenanceStatements([]string{trinoName, namespace, table}, body.Retention); err != nil {
		record(ev.Failed(audit.ReasonValidation))
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "validation_error", "message": err.Error()})
		return
	}

	executionID := uuid.New().String()

	wo := client.StartWorkflowOptions{
		ID:        "lens-maint-" + executionID,
		TaskQueue: "baleia-task-queue",
	}

	_, err = h.temporalClient.ExecuteWorkflow(r.Context(), wo, workflows.LensMaintenanceWorkflow,
		executionID, trinoName, namespace, table, body.Retention,
	)
	if err != nil {
		h.internalError(w, r, err)
		return
	}

	ev.Changes = map[string]audit.Change{"table": {To: trinoName + "." + namespace + "." + table}, "retention": {To: body.Retention}}
	record(ev.Allowed())
	writeJSON(w, http.StatusAccepted, map[string]string{
		"execution_id": executionID,
		"status":       "running",
		"retention":    body.Retention,
	})
}

func (h *lensHandlers) queryAnalytics(w http.ResponseWriter, r *http.Request) {
	user, ok := h.visibleUser(w, r)
	if !ok {
		return
	}
	var snapshots []db.TrinoQuerySnapshot
	var err error
	if user == "" {
		snapshots, err = h.queries.ListTrinoQuerySnapshots(r.Context(), db.ListTrinoQuerySnapshotsParams{Limit: 1000, Offset: 0})
	} else {
		snapshots, err = h.queries.ListTrinoQuerySnapshotsByUser(r.Context(), db.ListTrinoQuerySnapshotsByUserParams{User: user, Limit: 1000, Offset: 0})
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}

	userCounts := map[string]int{}
	var totalDuration int64
	var errorCount int

	for _, s := range snapshots {
		userCounts[s.User]++
		if s.ElapsedMs != nil {
			totalDuration += int64(*s.ElapsedMs)
		}
		if s.State == "failed" {
			errorCount++
		}
	}

	avgDuration := int64(0)
	if len(snapshots) > 0 {
		avgDuration = totalDuration / int64(len(snapshots))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_queries":   len(snapshots),
		"error_count":     errorCount,
		"avg_duration_ms": avgDuration,
		"users_active":    len(userCounts),
	})
}

func snapshotToMap(s db.TrinoQuerySnapshot) map[string]any {
	return map[string]any{
		"id":                   s.ID,
		"trino_query_id":       s.TrinoQueryID,
		"origin":               s.Origin,
		"user":                 s.User,
		"state":                s.State,
		"sql":                  s.Sql,
		"catalog":              s.Catalog,
		"created_at":           s.CreatedAt,
		"elapsed_ms":           s.ElapsedMs,
		"cpu_ms":               s.CpuMs,
		"physical_input_bytes": s.PhysicalInputBytes,
		"output_rows":          s.OutputRows,
		"error_message":        s.ErrorMessage,
	}
}

func healthToMap(s db.TableHealthSnapshot) map[string]any {
	return map[string]any{
		"id":                    s.ID,
		"catalog_connection_id": s.CatalogConnectionID,
		"catalog":               s.Catalog,
		"namespace":             s.Namespace,
		"table_name":            s.TableName,
		"snapshot_count":        s.SnapshotCount,
		"file_count":            s.FileCount,
		"total_size_bytes":      s.TotalSizeBytes,
		"oldest_snapshot_age_h": s.OldestSnapshotAgeHours,
		"health_score":          s.HealthScore,
		"needs_compaction":      s.NeedsCompaction,
		"needs_snapshot_expiry": s.NeedsSnapshotExpiry,
		"needs_orphan_cleanup":  s.NeedsOrphanCleanup,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
