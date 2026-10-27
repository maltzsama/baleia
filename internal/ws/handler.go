package ws

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/wsticket"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	writeTimeout  = 10 * time.Second
	pingInterval  = 15 * time.Second
	pongTimeout   = 30 * time.Second
	pollInterval  = 1 * time.Second
	maxDuration   = 30 * time.Minute
	maxWriteRetry = 3
)

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Querier interface {
	CheckWorkspaceAccess(ctx context.Context, arg QuerierCheckWorkspaceAccessParams) (bool, error)
	GetQueryExecutionByIDAndWorkspace(ctx context.Context, id pgtype.UUID, workspaceID pgtype.UUID) (QuerierQueryExecution, error)
	ListQueryStatementsByWorkspace(ctx context.Context, executionID pgtype.UUID, workspaceID pgtype.UUID) ([]QuerierQueryStatement, error)
}

type QuerierCheckWorkspaceAccessParams struct {
	ID     pgtype.UUID
	UserID pgtype.UUID
}

type QuerierQueryExecution struct {
	ID             pgtype.UUID
	WorkspaceID    pgtype.UUID
	Status         string
	Sql            string
	RowsReturned   *int64
	DurationMs     *int64
	ErrorCode      *string
	StartedAt      pgtype.Timestamptz
	TargetSnapshot []byte
}

type QuerierQueryStatement struct {
	ID           pgtype.UUID
	ExecutionID  pgtype.UUID
	Position     int32
	Sql          string
	Status       string
	RowsReturned *int64
	DurationMs   *int64
	ErrorMessage *string
	ErrorCode    *string
	ResultSets   []byte
	Truncated    bool
	StartedAt    pgtype.Timestamptz
	FinishedAt   pgtype.Timestamptz
}

type Handler struct {
	logger      *slog.Logger
	queries     Querier
	jwtSecret   string
	tickets     *wsticket.Store
	s3Endpoint  string
	s3AccessKey string
	s3SecretKey string
	s3Bucket    string
	s3UseSSL    bool
}

// SetTickets wires the shared single-use ticket store (#26).
func (h *Handler) SetTickets(store *wsticket.Store) { h.tickets = store }

func New(logger *slog.Logger, queries Querier, jwtSecret, s3Endpoint, s3AccessKey, s3SecretKey, s3Bucket string, s3UseSSL bool) *Handler {
	return &Handler{
		logger:      cmp.Or(logger, slog.New(slog.DiscardHandler)),
		queries:     queries,
		jwtSecret:   jwtSecret,
		tickets:     nil,
		s3Endpoint:  s3Endpoint,
		s3AccessKey: s3AccessKey,
		s3SecretKey: s3SecretKey,
		s3Bucket:    s3Bucket,
		s3UseSSL:    s3UseSSL,
	}
}

// ExecutionSnapshot is the Execution shape the first event of a connection
// carries (only the fields the run panel needs).
type ExecutionSnapshot struct {
	ID           string          `json:"id"`
	WorkspaceID  string          `json:"workspace_id"`
	Status       string          `json:"status"`
	Sql          string          `json:"sql"`
	CreatedAt    string          `json:"created_at"`
	RowsReturned *int64          `json:"rows_returned,omitempty"`
	DurationMs   *int64          `json:"duration_ms,omitempty"`
	ErrorCode    *string         `json:"error_code,omitempty"`
	ReadOnly     bool            `json:"read_only"`
	Target       json.RawMessage `json:"target,omitempty"`
}

// StatementSnapshot is one row of the snapshot and of a statement_status event.
type StatementSnapshot struct {
	ID           string  `json:"id"`
	ExecutionID  string  `json:"execution_id"`
	Position     int32   `json:"position"`
	Sql          string  `json:"sql"`
	Status       string  `json:"status"`
	ErrorCode    *string `json:"error_code,omitempty"`
	ErrorMessage *string `json:"error_message,omitempty"`
	RowsReturned *int64  `json:"rows_returned,omitempty"`
	ResultCount  int     `json:"result_count"`
	Truncated    bool    `json:"truncated"`
	DurationMs   *int64  `json:"duration_ms,omitempty"`
	StartedAt    *string `json:"started_at,omitempty"`
	FinishedAt   *string `json:"finished_at,omitempty"`
}

// WSEvent is one ExecutionEvent: snapshot, execution_status,
// statement_status or statement_progress (#111).
type WSEvent struct {
	Type         string              `json:"type"`
	ExecutionID  string              `json:"execution_id"`
	Seq          int64               `json:"seq"`
	TS           string              `json:"ts"`
	Execution    *ExecutionSnapshot  `json:"execution,omitempty"`
	Statements   []StatementSnapshot `json:"statements,omitempty"`
	Status       string              `json:"status,omitempty"`
	ErrorCode    *string             `json:"error_code,omitempty"`
	Position     *int                `json:"position,omitempty"`
	ErrorMessage *string             `json:"error_message,omitempty"`
	RowsReturned *int64              `json:"rows_returned,omitempty"`
	ResultCount  *int                `json:"result_count,omitempty"`
	Truncated    *bool               `json:"truncated,omitempty"`
	DurationMs   *int64              `json:"duration_ms,omitempty"`
	RowsSoFar    *int64              `json:"rows_so_far,omitempty"`
}

func (h *Handler) StreamExecution(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("ticket")
	if token == "" {
		http.Error(w, "missing ticket", http.StatusUnauthorized)
		return
	}
	if h.tickets == nil {
		http.Error(w, "tickets are not configured", http.StatusUnauthorized)
		return
	}
	ticket, ok := h.tickets.Consume(token)
	if !ok {
		http.Error(w, "invalid or expired ticket", http.StatusUnauthorized)
		return
	}

	workspaceID := r.PathValue("id")
	executionID := r.PathValue("execution_id")
	if ticket.ExecutionID != "" && ticket.ExecutionID != executionID {
		http.Error(w, "ticket is for another execution", http.StatusUnauthorized)
		return
	}

	// Make the authenticated actor visible to the request-logging middleware,
	// which logs after this handler returns.
	auth.SetLogActor(r.Context(), ticket.UserID)

	var wsID pgtype.UUID
	if err := wsID.Scan(workspaceID); err != nil {
		http.Error(w, "invalid workspace id", http.StatusBadRequest)
		return
	}

	var userID pgtype.UUID
	if err := userID.Scan(ticket.UserID); err != nil {
		http.Error(w, "invalid user", http.StatusUnauthorized)
		return
	}

	hasAccess, err := h.queries.CheckWorkspaceAccess(r.Context(), QuerierCheckWorkspaceAccessParams{
		ID:     wsID,
		UserID: userID,
	})
	if err != nil || !hasAccess {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "websocket upgrade", "err", err)
		return
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(pongTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongTimeout))
		return nil
	})

	ctx, cancel := context.WithTimeout(r.Context(), maxDuration)
	defer cancel()

	var lastStatus string
	var seenStatements map[int32]string
	var seq int64

	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()

	pingTicker := time.NewTicker(pingInterval)
	defer pingTicker.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			sendExecutionStatus(h.logger, conn, executionID, &seq, "failed", strPtr("timeout"))
			return
		case <-pingTicker.C:
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-pollTicker.C:
			if !h.poll(ctx, conn, executionID, wsID, &lastStatus, &seenStatements, &seq) {
				return
			}
		}
	}
}

func (h *Handler) poll(ctx context.Context, conn *websocket.Conn, executionID string, workspaceID pgtype.UUID, lastStatus *string, seenStatements *map[int32]string, seq *int64) bool {
	var execID pgtype.UUID
	execID.Scan(executionID)

	execution, err := h.queries.GetQueryExecutionByIDAndWorkspace(ctx, execID, workspaceID)
	if err != nil {
		sendExecutionStatus(h.logger, conn, executionID, seq, "failed", strPtr("not_found"))
		return false
	}

	statements, err := h.queries.ListQueryStatementsByWorkspace(ctx, execID, workspaceID)
	if err != nil {
		statements = nil
	}

	// The first message of every connection replaces the client state.
	if *lastStatus == "" {
		snapshot := executionSnapshot(execution)
		sendEvent(h.logger, conn, WSEvent{
			Type:        "snapshot",
			ExecutionID: executionID,
			Seq:         nextSeq(seq),
			TS:          now(),
			Execution:   &snapshot,
			Statements:  statementSnapshots(statements),
		})
		*seenStatements = make(map[int32]string, len(statements))
		for _, stmt := range statements {
			(*seenStatements)[stmt.Position] = stmt.Status
		}
		*lastStatus = execution.Status
		return !terminal(execution.Status)
	}

	for _, stmt := range statements {
		if (*seenStatements)[stmt.Position] == stmt.Status {
			continue
		}
		(*seenStatements)[stmt.Position] = stmt.Status
		snapshot := statementSnapshot(stmt)
		count := snapshot.ResultCount
		truncated := snapshot.Truncated
		sendEvent(h.logger, conn, WSEvent{
			Type:         "statement_status",
			ExecutionID:  executionID,
			Seq:          nextSeq(seq),
			TS:           now(),
			Position:     intPtr(int(stmt.Position)),
			Status:       stmt.Status,
			ErrorCode:    stmt.ErrorCode,
			ErrorMessage: stmt.ErrorMessage,
			RowsReturned: stmt.RowsReturned,
			ResultCount:  &count,
			Truncated:    &truncated,
			DurationMs:   stmt.DurationMs,
		})
	}

	if execution.Status != *lastStatus {
		*lastStatus = execution.Status
		sendExecutionStatus(h.logger, conn, executionID, seq, execution.Status, execution.ErrorCode)
		if terminal(execution.Status) {
			return false
		}
	}
	return true
}

func executionSnapshot(e QuerierQueryExecution) ExecutionSnapshot {
	out := ExecutionSnapshot{
		ID:           uuidString(e.ID),
		WorkspaceID:  uuidString(e.WorkspaceID),
		Status:       e.Status,
		Sql:          e.Sql,
		CreatedAt:    timeString(e.StartedAt),
		RowsReturned: e.RowsReturned,
		DurationMs:   e.DurationMs,
		ErrorCode:    e.ErrorCode,
	}
	if len(e.TargetSnapshot) > 0 {
		out.Target = json.RawMessage(e.TargetSnapshot)
		var target struct {
			ReadOnly bool `json:"read_only"`
		}
		if err := json.Unmarshal(e.TargetSnapshot, &target); err == nil {
			out.ReadOnly = target.ReadOnly
		}
	}
	return out
}

func statementSnapshots(statements []QuerierQueryStatement) []StatementSnapshot {
	out := make([]StatementSnapshot, 0, len(statements))
	for _, stmt := range statements {
		out = append(out, statementSnapshot(stmt))
	}
	return out
}

func statementSnapshot(s QuerierQueryStatement) StatementSnapshot {
	out := StatementSnapshot{
		ID:           uuidString(s.ID),
		ExecutionID:  uuidString(s.ExecutionID),
		Position:     s.Position,
		Sql:          s.Sql,
		Status:       s.Status,
		ErrorCode:    s.ErrorCode,
		ErrorMessage: s.ErrorMessage,
		RowsReturned: s.RowsReturned,
		Truncated:    s.Truncated,
		DurationMs:   s.DurationMs,
	}
	if len(s.ResultSets) > 0 {
		var sets []json.RawMessage
		if err := json.Unmarshal(s.ResultSets, &sets); err == nil {
			out.ResultCount = len(sets)
		}
	}
	if s.StartedAt.Valid {
		started := s.StartedAt.Time.UTC().Format(time.RFC3339)
		out.StartedAt = &started
	}
	if s.FinishedAt.Valid {
		finished := s.FinishedAt.Time.UTC().Format(time.RFC3339)
		out.FinishedAt = &finished
	}
	return out
}

func sendExecutionStatus(logger *slog.Logger, conn *websocket.Conn, executionID string, seq *int64, status string, code *string) {
	sendEvent(logger, conn, WSEvent{
		Type:        "execution_status",
		ExecutionID: executionID,
		Seq:         nextSeq(seq),
		TS:          now(),
		Status:      status,
		ErrorCode:   code,
	})
}

func sendEvent(logger *slog.Logger, conn *websocket.Conn, event WSEvent) {
	var lastErr error
	for i := 0; i < maxWriteRetry; i++ {
		conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := conn.WriteJSON(event); err != nil {
			lastErr = err
			time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
			continue
		}
		return
	}
	logger.Error("websocket write failed after retries", "err", lastErr)
}

func terminal(status string) bool {
	return status == "finished" || status == "failed" || status == "cancelled"
}

func nextSeq(seq *int64) int64 {
	*seq = *seq + 1
	return *seq
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func timeString(ts pgtype.Timestamptz) string {
	if ts.Valid {
		return ts.Time.UTC().Format(time.RFC3339)
	}
	return now()
}

func uuidString(id pgtype.UUID) string {
	return uuid.UUID(id.Bytes).String()
}

func strPtr(s string) *string { return &s }

func intPtr(i int) *int { return &i }
