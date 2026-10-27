package ws_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/ws"
	"github.com/galileostudio/baleia/internal/wsticket"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"
)

type mockQuerier struct {
	accessCheck func(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error)
	execution   func(ctx context.Context, id pgtype.UUID, workspaceID pgtype.UUID) (ws.QuerierQueryExecution, error)
	statements  func(ctx context.Context, executionID pgtype.UUID, workspaceID pgtype.UUID) ([]ws.QuerierQueryStatement, error)
}

func (m *mockQuerier) CheckWorkspaceAccess(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error) {
	if m.accessCheck != nil {
		return m.accessCheck(ctx, arg)
	}
	return true, nil
}

func (m *mockQuerier) GetQueryExecutionByIDAndWorkspace(ctx context.Context, id pgtype.UUID, workspaceID pgtype.UUID) (ws.QuerierQueryExecution, error) {
	if m.execution != nil {
		return m.execution(ctx, id, workspaceID)
	}
	return ws.QuerierQueryExecution{Status: "running"}, nil
}

func (m *mockQuerier) ListQueryStatementsByWorkspace(ctx context.Context, executionID pgtype.UUID, workspaceID pgtype.UUID) ([]ws.QuerierQueryStatement, error) {
	if m.statements != nil {
		return m.statements(ctx, executionID, workspaceID)
	}
	return nil, nil
}

func finishedExecution() ws.QuerierQueryExecution {
	rows := int64(100)
	dur := int64(500)
	return ws.QuerierQueryExecution{
		Status:       "finished",
		RowsReturned: &rows,
		DurationMs:   &dur,
	}
}

func finishedStatements() []ws.QuerierQueryStatement {
	rows := int64(50)
	dur := int64(250)
	return []ws.QuerierQueryStatement{
		{Position: 1, Sql: "SELECT 1", Status: "finished", RowsReturned: &rows, DurationMs: &dur},
	}
}

// newHandler returns a handler wired to a fresh ticket store.
func newHandler(mock *mockQuerier) (*ws.Handler, *wsticket.Store) {
	handler := ws.New(mock, "test-secret", "", "", "", "", false)
	tickets := wsticket.New()
	handler.SetTickets(tickets)
	return handler, tickets
}

func TestWebSocketMissingTicket(t *testing.T) {
	t.Parallel()
	handler, _ := newHandler(&mockQuerier{})

	req := httptest.NewRequest("GET", "/ws/workspaces/ws1/executions/exec1", nil)
	req.SetPathValue("id", "00000000-0000-0000-0000-000000000001")
	req.SetPathValue("execution_id", "00000000-0000-0000-0000-000000000002")
	rec := httptest.NewRecorder()

	handler.StreamExecution(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestWebSocketInvalidTicket(t *testing.T) {
	t.Parallel()
	handler, _ := newHandler(&mockQuerier{})

	req := httptest.NewRequest("GET", "/ws/workspaces/ws1/executions/exec1?ticket=invalid", nil)
	req.SetPathValue("id", "00000000-0000-0000-0000-000000000001")
	req.SetPathValue("execution_id", "00000000-0000-0000-0000-000000000002")
	rec := httptest.NewRecorder()

	handler.StreamExecution(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestWebSocketTicketScopedToAnotherExecution(t *testing.T) {
	t.Parallel()
	handler, tickets := newHandler(&mockQuerier{})
	ticket, _ := tickets.Issue("00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000099")

	req := httptest.NewRequest("GET", "/ws/workspaces/ws1/executions/exec1?ticket="+ticket, nil)
	req.SetPathValue("id", "00000000-0000-0000-0000-000000000001")
	req.SetPathValue("execution_id", "00000000-0000-0000-0000-000000000002")
	rec := httptest.NewRecorder()

	handler.StreamExecution(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestWebSocketNoAccess(t *testing.T) {
	t.Parallel()
	mock := &mockQuerier{
		accessCheck: func(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error) {
			return false, nil
		},
	}
	handler, tickets := newHandler(mock)

	const userID = "00000000-0000-0000-0000-000000000001"
	const executionID = "00000000-0000-0000-0000-000000000003"
	ticket, _ := tickets.Issue(userID, executionID)
	req := httptest.NewRequest("GET", "/ws/workspaces/00000000-0000-0000-0000-000000000002/executions/"+executionID+"?ticket="+ticket, nil)
	req.SetPathValue("id", "00000000-0000-0000-0000-000000000002")
	req.SetPathValue("execution_id", executionID)
	rec := httptest.NewRecorder()

	handler.StreamExecution(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestWebSocketStreamFinished(t *testing.T) {
	t.Parallel()
	execID := "00000000-0000-0000-0000-000000000004"
	wsID := "00000000-0000-0000-0000-000000000005"
	userID := "00000000-0000-0000-0000-000000000006"

	mock := &mockQuerier{
		accessCheck: func(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error) {
			return true, nil
		},
		execution: func(ctx context.Context, id pgtype.UUID, workspaceID pgtype.UUID) (ws.QuerierQueryExecution, error) {
			return finishedExecution(), nil
		},
		statements: func(ctx context.Context, executionID pgtype.UUID, workspaceID pgtype.UUID) ([]ws.QuerierQueryStatement, error) {
			return finishedStatements(), nil
		},
	}

	handler, tickets := newHandler(mock)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", wsID)
		r.SetPathValue("execution_id", execID)
		handler.StreamExecution(w, r)
	}))
	defer srv.Close()

	ticket, _ := tickets.Issue(userID, execID)
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "?ticket=" + ticket

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	var events []ws.WSEvent
	for {
		var evt ws.WSEvent
		err := conn.ReadJSON(&evt)
		if err != nil {
			break
		}
		events = append(events, evt)
		if evt.Type == "snapshot" {
			break
		}
	}

	if len(events) != 1 {
		t.Fatalf("expected only the snapshot, got %d events", len(events))
	}
	lastEvent := events[0]
	if lastEvent.Type != "snapshot" {
		t.Errorf("expected 'snapshot' event, got %q", lastEvent.Type)
	}
	if lastEvent.Execution == nil || lastEvent.Execution.Status != "finished" {
		t.Errorf("snapshot execution = %+v", lastEvent.Execution)
	}
	if lastEvent.Seq != 1 || lastEvent.TS == "" {
		t.Errorf("snapshot seq/ts = %d/%q", lastEvent.Seq, lastEvent.TS)
	}
}

func TestWebSocketWithStatements(t *testing.T) {
	t.Parallel()
	execID := "00000000-0000-0000-0000-000000000007"
	wsID := "00000000-0000-0000-0000-000000000008"
	userID := "00000000-0000-0000-0000-000000000009"

	callCount := 0
	mock := &mockQuerier{
		accessCheck: func(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error) {
			return true, nil
		},
		execution: func(ctx context.Context, id pgtype.UUID, workspaceID pgtype.UUID) (ws.QuerierQueryExecution, error) {
			callCount++
			if callCount < 3 {
				return ws.QuerierQueryExecution{Status: "running"}, nil
			}
			return finishedExecution(), nil
		},
		statements: func(ctx context.Context, executionID pgtype.UUID, workspaceID pgtype.UUID) ([]ws.QuerierQueryStatement, error) {
			if callCount < 2 {
				return nil, nil
			}
			return finishedStatements(), nil
		},
	}

	handler, tickets := newHandler(mock)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", wsID)
		r.SetPathValue("execution_id", execID)
		handler.StreamExecution(w, r)
	}))
	defer srv.Close()

	ticket, _ := tickets.Issue(userID, execID)
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "?ticket=" + ticket

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	var events []ws.WSEvent
	timeout := time.After(10 * time.Second)
loop:
	for {
		select {
		case <-timeout:
			break loop
		default:
		}
		var evt ws.WSEvent
		err := conn.ReadJSON(&evt)
		if err != nil {
			break
		}
		events = append(events, evt)
		if evt.Type == "execution_status" && evt.Status == "finished" {
			break
		}
	}

	foundSnapshot := false
	foundStatement := false
	foundFinished := false
	for _, evt := range events {
		switch evt.Type {
		case "snapshot":
			foundSnapshot = true
		case "statement_status":
			foundStatement = true
		case "execution_status":
			if evt.Status == "finished" {
				foundFinished = true
			}
		}
	}

	if !foundSnapshot {
		t.Error("expected 'snapshot' event")
	}
	if !foundStatement {
		t.Error("expected 'statement_status' event")
	}
	if !foundFinished {
		t.Error("expected a terminal 'execution_status' event")
	}
}

func TestWebSocketEventsJSON(t *testing.T) {
	t.Parallel()
	evt := ws.WSEvent{
		Type:        "execution_status",
		ExecutionID: "test-id",
		Seq:         1,
		TS:          "2024-01-01T00:00:00Z",
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ws.WSEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != evt.Type || decoded.Seq != evt.Seq || decoded.TS != evt.TS {
		t.Errorf("expected %+v, got %+v", evt, decoded)
	}
}
