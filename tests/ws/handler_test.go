package ws_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/ws"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"
)

type mockQuerier struct {
	accessCheck func(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error)
	execution   func(ctx context.Context, id pgtype.UUID) (ws.QuerierQueryExecution, error)
	statements  func(ctx context.Context, executionID pgtype.UUID) ([]ws.QuerierQueryStatement, error)
}

func (m *mockQuerier) CheckWorkspaceAccess(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error) {
	if m.accessCheck != nil {
		return m.accessCheck(ctx, arg)
	}
	return true, nil
}

func (m *mockQuerier) GetQueryExecutionByID(ctx context.Context, id pgtype.UUID) (ws.QuerierQueryExecution, error) {
	if m.execution != nil {
		return m.execution(ctx, id)
	}
	return ws.QuerierQueryExecution{Status: "running"}, nil
}

func (m *mockQuerier) ListQueryStatements(ctx context.Context, executionID pgtype.UUID) ([]ws.QuerierQueryStatement, error) {
	if m.statements != nil {
		return m.statements(ctx, executionID)
	}
	return nil, nil
}

func finishedExecution() ws.QuerierQueryExecution {
	rows := int64(100)
	dur := int64(500)
	resultKey := "query-results/test/result.arrow"
	return ws.QuerierQueryExecution{
		Status:       "finished",
		RowsReturned: &rows,
		DurationMs:   &dur,
		ResultKey:    &resultKey,
	}
}

func finishedStatements() []ws.QuerierQueryStatement {
	rows := int64(50)
	dur := int64(250)
	return []ws.QuerierQueryStatement{
		{Position: 1, Status: "finished", RowsReturned: &rows, DurationMs: &dur},
	}
}

func testJWT(userID, role string) string {
	token, _ := auth.NewAccessToken(userID, role, "test-secret")
	return token
}

func TestWebSocketMissingToken(t *testing.T) {
	mock := &mockQuerier{}
	handler := ws.New(mock, "test-secret", "", "", "", "", false)

	req := httptest.NewRequest("GET", "/ws/workspaces/ws1/executions/exec1", nil)
	req.SetPathValue("id", "00000000-0000-0000-0000-000000000001")
	req.SetPathValue("execution_id", "00000000-0000-0000-0000-000000000002")
	rec := httptest.NewRecorder()

	handler.StreamExecution(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestWebSocketInvalidToken(t *testing.T) {
	mock := &mockQuerier{}
	handler := ws.New(mock, "test-secret", "", "", "", "", false)

	req := httptest.NewRequest("GET", "/ws/workspaces/ws1/executions/exec1?token=invalid", nil)
	req.SetPathValue("id", "00000000-0000-0000-0000-000000000001")
	req.SetPathValue("execution_id", "00000000-0000-0000-0000-000000000002")
	rec := httptest.NewRecorder()

	handler.StreamExecution(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestWebSocketNoAccess(t *testing.T) {
	mock := &mockQuerier{
		accessCheck: func(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error) {
			return false, nil
		},
	}
	handler := ws.New(mock, "test-secret", "", "", "", "", false)

	token := testJWT("00000000-0000-0000-0000-000000000001", "admin")
	req := httptest.NewRequest("GET", "/ws/workspaces/00000000-0000-0000-0000-000000000002/executions/00000000-0000-0000-0000-000000000003?token="+token, nil)
	req.SetPathValue("id", "00000000-0000-0000-0000-000000000002")
	req.SetPathValue("execution_id", "00000000-0000-0000-0000-000000000003")
	rec := httptest.NewRecorder()

	handler.StreamExecution(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestWebSocketStreamFinished(t *testing.T) {
	execID := "00000000-0000-0000-0000-000000000004"
	wsID := "00000000-0000-0000-0000-000000000005"
	userID := "00000000-0000-0000-0000-000000000006"

	mock := &mockQuerier{
		accessCheck: func(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error) {
			return true, nil
		},
		execution: func(ctx context.Context, id pgtype.UUID) (ws.QuerierQueryExecution, error) {
			return finishedExecution(), nil
		},
		statements: func(ctx context.Context, executionID pgtype.UUID) ([]ws.QuerierQueryStatement, error) {
			return finishedStatements(), nil
		},
	}

	handler := ws.New(mock, "test-secret", "ceph:8080", "key", "secret", "bucket", false)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", wsID)
		r.SetPathValue("execution_id", execID)
		handler.StreamExecution(w, r)
	}))
	defer srv.Close()

	token := testJWT(userID, "admin")
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "?token=" + token

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
		if evt.Type == "finished" || evt.Type == "failed" || evt.Type == "cancelled" {
			break
		}
	}

	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}

	lastEvent := events[len(events)-1]
	if lastEvent.Type != "finished" {
		t.Errorf("expected 'finished' event, got %q", lastEvent.Type)
	}
	if lastEvent.ResultURL == "" {
		t.Error("expected non-empty result_url in finished event")
	}
}

func TestWebSocketWithStatements(t *testing.T) {
	execID := "00000000-0000-0000-0000-000000000007"
	wsID := "00000000-0000-0000-0000-000000000008"
	userID := "00000000-0000-0000-0000-000000000009"

	callCount := 0
	mock := &mockQuerier{
		accessCheck: func(ctx context.Context, arg ws.QuerierCheckWorkspaceAccessParams) (bool, error) {
			return true, nil
		},
		execution: func(ctx context.Context, id pgtype.UUID) (ws.QuerierQueryExecution, error) {
			callCount++
			if callCount < 3 {
				return ws.QuerierQueryExecution{Status: "running"}, nil
			}
			return finishedExecution(), nil
		},
		statements: func(ctx context.Context, executionID pgtype.UUID) ([]ws.QuerierQueryStatement, error) {
			if callCount < 2 {
				return nil, nil
			}
			return finishedStatements(), nil
		},
	}

	handler := ws.New(mock, "test-secret", "ceph:8080", "key", "secret", "bucket", false)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", wsID)
		r.SetPathValue("execution_id", execID)
		handler.StreamExecution(w, r)
	}))
	defer srv.Close()

	token := testJWT(userID, "admin")
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "?token=" + token

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
		if evt.Type == "finished" || evt.Type == "failed" || evt.Type == "cancelled" {
			break
		}
	}

	foundRunning := false
	foundProgress := false
	foundFinished := false
	for _, evt := range events {
		switch evt.Type {
		case "running":
			foundRunning = true
		case "progress":
			foundProgress = true
		case "finished":
			foundFinished = true
		}
	}

	if !foundRunning {
		t.Error("expected 'running' event")
	}
	if !foundProgress {
		t.Error("expected 'progress' event")
	}
	if !foundFinished {
		t.Error("expected 'finished' event")
	}
}

func TestWebSocketEventsJSON(t *testing.T) {
	evt := ws.WSEvent{
		Type:        "finished",
		ExecutionID: "test-id",
		Timestamp:   "2024-01-01T00:00:00Z",
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ws.WSEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != evt.Type {
		t.Errorf("expected %q, got %q", evt.Type, decoded.Type)
	}
}
