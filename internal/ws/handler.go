package ws

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
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
	Status       string
	RowsReturned *int64
	DurationMs   *int64
	ResultKey    *string
}

type QuerierQueryStatement struct {
	Position     int32
	Status       string
	RowsReturned *int64
	DurationMs   *int64
	ErrorMessage *string
}

type Handler struct {
	queries     Querier
	jwtSecret   string
	s3Endpoint  string
	s3AccessKey string
	s3SecretKey string
	s3Bucket    string
	s3UseSSL    bool
}

func New(queries Querier, jwtSecret, s3Endpoint, s3AccessKey, s3SecretKey, s3Bucket string, s3UseSSL bool) *Handler {
	return &Handler{
		queries:     queries,
		jwtSecret:   jwtSecret,
		s3Endpoint:  s3Endpoint,
		s3AccessKey: s3AccessKey,
		s3SecretKey: s3SecretKey,
		s3Bucket:    s3Bucket,
		s3UseSSL:    s3UseSSL,
	}
}

type WSEvent struct {
	Type          string `json:"type"`
	ExecutionID   string `json:"execution_id,omitempty"`
	Position      *int   `json:"statement_position,omitempty"`
	RowsReturned  *int64 `json:"rows_returned,omitempty"`
	RowsSoFar     *int64 `json:"rows_so_far,omitempty"`
	DurationMs    *int64 `json:"duration_ms,omitempty"`
	Message       string `json:"message,omitempty"`
	ResultURL     string `json:"result_url,omitempty"`
	Timestamp     string `json:"ts"`
}

func (h *Handler) StreamExecution(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	claims, err := auth.ParseToken(token, h.jwtSecret)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// Make the authenticated actor visible to the request-logging middleware,
	// which logs after this handler returns.
	auth.SetLogActor(r.Context(), claims.UserID)

	workspaceID := r.PathValue("id")
	executionID := r.PathValue("execution_id")

	var wsID pgtype.UUID
	if err := wsID.Scan(workspaceID); err != nil {
		http.Error(w, "invalid workspace id", http.StatusBadRequest)
		return
	}

	var userID pgtype.UUID
	if err := userID.Scan(claims.UserID); err != nil {
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
		slog.Error("websocket upgrade", "err", err)
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
			sendEvent(conn, WSEvent{
				Type:      "failed",
				Message:   "execution timed out",
				Timestamp: now(),
			})
			return
		case <-pingTicker.C:
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-pollTicker.C:
			if !h.poll(ctx, conn, executionID, wsID, &lastStatus, &seenStatements) {
				return
			}
		}
	}
}

func (h *Handler) poll(ctx context.Context, conn *websocket.Conn, executionID string, workspaceID pgtype.UUID, lastStatus *string, seenStatements *map[int32]string) bool {
	var execID pgtype.UUID
	execID.Scan(executionID)

	execution, err := h.queries.GetQueryExecutionByIDAndWorkspace(ctx, execID, workspaceID)
	if err != nil {
		sendEvent(conn, WSEvent{
			Type:      "failed",
			Message:   "execution not found",
			Timestamp: now(),
		})
		return false
	}

	nowStr := now()

	if *lastStatus == "" && execution.Status == "running" {
		sendEvent(conn, WSEvent{
			Type:        "running",
			ExecutionID: executionID,
			Timestamp:   nowStr,
		})
	}

	statements, err := h.queries.ListQueryStatementsByWorkspace(ctx, execID, workspaceID)
	if err == nil {
		if *seenStatements == nil {
			*seenStatements = make(map[int32]string)
		}

		var totalRows int64
		for _, stmt := range statements {
			prevStatus := (*seenStatements)[stmt.Position]

			if prevStatus != stmt.Status {
				(*seenStatements)[stmt.Position] = stmt.Status

				switch stmt.Status {
				case "running":
					sendEvent(conn, WSEvent{
						Type:        "statement_running",
						ExecutionID: executionID,
						Position:    intPtr(int(stmt.Position)),
						Timestamp:   nowStr,
					})
				case "finished":
					var rows int64
					var dur int64
					if stmt.RowsReturned != nil {
						rows = *stmt.RowsReturned
					}
					if stmt.DurationMs != nil {
						dur = *stmt.DurationMs
					}
					totalRows += rows
					sendEvent(conn, WSEvent{
						Type:         "progress",
						ExecutionID:  executionID,
						Position:     intPtr(int(stmt.Position)),
						RowsReturned: &rows,
						DurationMs:   &dur,
						RowsSoFar:    &totalRows,
						Timestamp:    nowStr,
					})
				case "failed":
					errMsg := ""
					if stmt.ErrorMessage != nil {
						errMsg = *stmt.ErrorMessage
					}
					sendEvent(conn, WSEvent{
						Type:        "statement_error",
						ExecutionID: executionID,
						Position:    intPtr(int(stmt.Position)),
						Message:     errMsg,
						Timestamp:   nowStr,
					})
				}
			}
		}
	}

	if execution.Status != *lastStatus {
		*lastStatus = execution.Status

		switch execution.Status {
		case "finished":
			var rows int64
			var dur int64
			if execution.RowsReturned != nil {
				rows = *execution.RowsReturned
			}
			if execution.DurationMs != nil {
				dur = *execution.DurationMs
			}
			resultURL := ""
			if execution.ResultKey != nil && *execution.ResultKey != "" {
				resultURL = buildPresignedURL(h.s3Endpoint, h.s3AccessKey, h.s3SecretKey, h.s3Bucket, *execution.ResultKey, h.s3UseSSL)
			}
			sendEvent(conn, WSEvent{
				Type:         "finished",
				ExecutionID:  executionID,
				RowsReturned: &rows,
				DurationMs:   &dur,
				ResultURL:    resultURL,
				Timestamp:    nowStr,
			})
			return false

		case "failed":
			errMsg := "query execution failed"
			if statements, err := h.queries.ListQueryStatementsByWorkspace(ctx, execID, workspaceID); err == nil {
				for _, s := range statements {
					if s.Status == "failed" && s.ErrorMessage != nil && *s.ErrorMessage != "" {
						errMsg = *s.ErrorMessage
						break
					}
				}
			}
			sendEvent(conn, WSEvent{
				Type:        "failed",
				ExecutionID: executionID,
				Message:     errMsg,
				Timestamp:   nowStr,
			})
			return false

		case "cancelled":
			sendEvent(conn, WSEvent{
				Type:        "cancelled",
				ExecutionID: executionID,
				Timestamp:   nowStr,
			})
			return false
		}
	}

	return true
}

func sendEvent(conn *websocket.Conn, event WSEvent) {
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
	slog.Error("websocket write failed after retries", "err", lastErr)
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func intPtr(i int) *int {
	return &i
}

func buildPresignedURL(endpoint, accessKey, secretKey, bucket, key string, useSSL bool) string {
	scheme := "http"
	if useSSL {
		scheme = "https"
	}
	return scheme + "://" + endpoint + "/" + bucket + "/" + key
}
