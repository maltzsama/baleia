package bigateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/bikeys"
)

// authorize authenticates the key and applies the global and workspace BI
// switches, both of which take effect at the next request.
func (g *Gateway) authorize(w http.ResponseWriter, r *http.Request) (bikeys.Credential, bool) {
	cred, ok := g.authenticate(w, r)
	if !ok {
		return bikeys.Credential{}, false
	}
	setting, err := g.deps.Settings.GetBIGatewaySetting(r.Context())
	if err != nil && !errors.Is(err, db.ErrNoRows) {
		g.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "the BI gateway setting is unavailable")
		return bikeys.Credential{}, false
	}
	if err == nil && !setting.Enabled {
		g.writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "the BI gateway is disabled")
		return bikeys.Credential{}, false
	}
	ws, err := g.deps.Workspaces.GetWorkspaceByID(r.Context(), cred.Key.WorkspaceID)
	if err != nil {
		g.writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "the key's workspace is unavailable")
		return bikeys.Credential{}, false
	}
	if !ws.BIEnabled {
		g.writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "BI is disabled for this workspace")
		return bikeys.Credential{}, false
	}
	return cred, true
}

// withinLimits applies the per-key and per-workspace concurrent statement
// limits. The count and the insert are not one atomic operation across
// replicas; a small overshoot is possible, never a bypass.
func (g *Gateway) withinLimits(w http.ResponseWriter, r *http.Request, cred bikeys.Credential) bool {
	if g.deps.MaxPerKey > 0 {
		running, err := g.deps.Executions.CountRunningBIExecutionsByKey(r.Context(), cred.Key.ID)
		if err != nil {
			g.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "the BI concurrency limit is unavailable")
			return false
		}
		if running >= int64(g.deps.MaxPerKey) {
			w.Header().Set("Retry-After", "1")
			g.writeError(w, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "the BI key has too many running statements")
			return false
		}
	}
	if g.deps.MaxPerWorkspace > 0 {
		running, err := g.deps.Executions.CountRunningBIExecutionsByWorkspace(r.Context(), cred.Key.WorkspaceID)
		if err != nil {
			g.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "the BI concurrency limit is unavailable")
			return false
		}
		if running >= int64(g.deps.MaxPerWorkspace) {
			w.Header().Set("Retry-After", "1")
			g.writeError(w, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "the workspace has too many running BI statements")
			return false
		}
	}
	return true
}

// createExecution records the statement as an origin=bi execution before it
// reaches Trino, so history, audit and monitoring see it even if the client
// disappears.
func (g *Gateway) createExecution(w http.ResponseWriter, r *http.Request, cred bikeys.Credential, sql string) (db.UUID, bool) {
	var catalog, schema, requestID *string
	if v := r.Header.Get("X-Trino-Catalog"); v != "" {
		catalog = &v
	}
	if v := r.Header.Get("X-Trino-Schema"); v != "" {
		schema = &v
	}
	if v := r.Header.Get("X-Request-ID"); v != "" {
		requestID = &v
	}
	execution, err := g.deps.Executions.CreateQueryExecution(r.Context(), db.CreateQueryExecutionParams{
		WorkspaceID:  cred.Key.WorkspaceID,
		UserID:       cred.User.ID,
		Status:       "running",
		Origin:       "bi",
		BIKeyID:      cred.Key.ID,
		Sql:          sql,
		TrinoCatalog: catalog,
		RequestID:    requestID,
		EngineID:     cred.Key.EngineID,
		TargetSchema: schema,
	})
	if err != nil {
		g.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "the execution could not be recorded")
		return db.UUID{}, false
	}
	if g.deps.Audit != nil {
		ev := audit.Event{
			Action:       audit.ExecutionCreate,
			ResourceType: "execution",
			ResourceID:   execution.ID.String(),
			WorkspaceID:  cred.Key.WorkspaceID.String(),
			Changes: map[string]audit.Change{
				"origin":    {To: "bi"},
				"engine_id": {To: cred.Key.EngineID.String()},
			},
		}
		_ = g.deps.Audit.Record(r.Context(), ev.Allowed())
	}
	return execution.ID, true
}

// pageMeta is the part of a Trino page the execution record needs.
type pageMeta struct {
	ID    string `json:"id"`
	Stats struct {
		State     string `json:"state"`
		Rows      int64  `json:"processedRows"`
		ElapsedMs int64  `json:"elapsedTimeMillis"`
	} `json:"stats"`
	Error *struct {
		ErrorName string `json:"errorName"`
	} `json:"error"`
}

// recordPage closes the execution when the page is terminal, or marks it
// cancelled after a successful DELETE. Best effort, on a detached context.
func (g *Gateway) recordPage(ctx context.Context, execID db.UUID, body []byte, method string, status int) {
	if !execID.Valid {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if method == http.MethodDelete && status < http.StatusMultipleChoices {
		g.finish(ctx, execID, "cancelled", "", 0, 0)
		return
	}
	var meta pageMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return
	}
	switch strings.ToUpper(meta.Stats.State) {
	case "FINISHED":
		g.finish(ctx, execID, "finished", meta.ID, meta.Stats.Rows, meta.Stats.ElapsedMs)
	case "FAILED":
		name := "engine_error"
		if meta.Error != nil && meta.Error.ErrorName != "" {
			name = strings.ToLower(meta.Error.ErrorName)
		}
		g.fail(ctx, execID, meta.ID, name)
	case "CANCELED":
		g.finish(ctx, execID, "cancelled", meta.ID, 0, 0)
	}
}

// finish moves a running execution to a terminal status. A row that already
// left running (a cancel raced the final page) is not an error.
func (g *Gateway) finish(ctx context.Context, execID db.UUID, status, engineID string, rows, duration int64) {
	var engine *string
	if engineID != "" {
		engine = &engineID
	}
	var rowsPtr, durationPtr *int64
	if status == "finished" {
		rowsPtr, durationPtr = &rows, &duration
	}
	_, err := g.deps.Executions.UpdateQueryExecution(ctx, db.UpdateQueryExecutionParams{
		ID:            execID,
		Status:        status,
		EngineQueryID: engine,
		RowsReturned:  rowsPtr,
		DurationMs:    durationPtr,
		FinishedAt:    db.Timestamptz{Time: g.now(), Valid: true},
	})
	if err != nil && !errors.Is(err, db.ErrNoRows) && g.deps.Logger != nil {
		g.deps.Logger.WarnContext(ctx, "bi gateway: could not finish execution", "execution_id", execID.String(), "err", err)
	}
}

// fail moves a running execution to failed with a stable code.
func (g *Gateway) fail(ctx context.Context, execID db.UUID, engineID, code string) {
	var engine *string
	if engineID != "" {
		engine = &engineID
	}
	_, err := g.deps.Executions.FailQueryExecution(ctx, db.FailQueryExecutionParams{ID: execID, ErrorCode: &code, EngineQueryID: engine})
	if err != nil && !errors.Is(err, db.ErrNoRows) && g.deps.Logger != nil {
		g.deps.Logger.WarnContext(ctx, "bi gateway: could not fail execution", "execution_id", execID.String(), "err", err)
	}
}

// failExecution marks the execution failed when the coordinator cannot be
// reached before a page is returned.
func (g *Gateway) failExecution(ctx context.Context, execID db.UUID, code string) {
	if !execID.Valid {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	g.fail(ctx, execID, "", code)
}
