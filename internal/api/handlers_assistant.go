package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/assistant"
	"github.com/galileostudio/baleia/internal/assistant/llm"
	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	adb "github.com/galileostudio/baleia/internal/db/assistant"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Assistant (#62): the settings, the private conversations and their messages.
// The turn engine and SSE stream land in the follow-up; this file is the
// persistence and configuration surface.

func (h *Handlers) assistantService() (*assistant.Service, error) {
	if h.assistant == nil {
		return nil, errors.New("assistant service is not configured")
	}
	return h.assistant, nil
}

func assistantSettingsToPublic(s adb.AssistantSetting) AssistantSettings {
	out := AssistantSettings{
		Enabled:                 s.Enabled,
		DataSharing:             AssistantSettingsDataSharing(s.DataSharing),
		ApiKeySet:               s.ApiKeyEnc != nil && *s.ApiKeyEnc != "",
		MaxTurnSeconds:          int(s.MaxTurnSeconds),
		MaxToolCallsPerTurn:     int(s.MaxToolCallsPerTurn),
		MaxOutputTokensPerTurn:  int(s.MaxOutputTokensPerTurn),
		DailyTokenBudgetPerUser: int(s.DailyTokenBudgetPerUser),
		BaseUrl:                 s.BaseUrl,
		Model:                   s.Model,
	}
	if s.Provider != nil {
		p := AssistantSettingsProvider(*s.Provider)
		out.Provider = &p
	}
	return out
}

func assistantConversationToPublic(c adb.AssistantConversation) AssistantConversation {
	return AssistantConversation{
		Id:          openapi_types.UUID(c.ID.Bytes),
		WorkspaceId: openapi_types.UUID(c.WorkspaceID.Bytes),
		Title:       c.Title,
		State:       AssistantConversationState(c.State),
		LastSeq:     int(c.LastSeq),
		CreatedAt:   pgTimestampToTime(c.CreatedAt),
		UpdatedAt:   pgTimestampToTime(c.UpdatedAt),
	}
}

func assistantMessageToPublic(m adb.AssistantMessage) AssistantMessage {
	var content any
	_ = json.Unmarshal(m.Content, &content)
	out := AssistantMessage{
		Seq:       int(m.Seq),
		Role:      AssistantMessageRole(m.Role),
		Content:   content,
		CreatedAt: pgTimestampToTime(m.CreatedAt),
	}
	if m.InputTokens != nil {
		v := int(*m.InputTokens)
		out.InputTokens = &v
	}
	if m.OutputTokens != nil {
		v := int(*m.OutputTokens)
		out.OutputTokens = &v
	}
	return out
}

// GetAssistantStatus — GET /assistant/status. Any authenticated user; a missing
// settings row or an unconfigured service reports the conservative default.
func (h *Handlers) GetAssistantStatus(ctx context.Context, _ GetAssistantStatusRequestObject) (GetAssistantStatusResponseObject, error) {
	if _, err := authz.PrincipalFromContext(ctx); err != nil {
		return GetAssistantStatus401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	status := AssistantStatus{Enabled: false, DataSharing: AssistantStatusDataSharing("metadata_only")}
	if h.assistant != nil {
		if row, err := h.assistant.Settings(ctx); err == nil {
			status.Enabled = row.Enabled
			status.DataSharing = AssistantStatusDataSharing(row.DataSharing)
		}
	}
	return GetAssistantStatus200JSONResponse(status), nil
}

// GetAssistantSettings — GET /admin/assistant/settings (G-admin).
func (h *Handlers) GetAssistantSettings(ctx context.Context, _ GetAssistantSettingsRequestObject) (GetAssistantSettingsResponseObject, error) {
	if auth.RoleFromContext(ctx) != "admin" {
		return GetAssistantSettings403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}
	svc, err := h.assistantService()
	if err != nil {
		return nil, err
	}
	row, err := svc.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return GetAssistantSettings200JSONResponse(assistantSettingsToPublic(row)), nil
}

// PutAssistantSettings — PUT /admin/assistant/settings (G-admin). The API key is
// write-only: an omitted or empty value keeps the stored key.
func (h *Handlers) PutAssistantSettings(ctx context.Context, request PutAssistantSettingsRequestObject) (PutAssistantSettingsResponseObject, error) {
	if auth.RoleFromContext(ctx) != "admin" {
		return PutAssistantSettings403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}
	if request.Body == nil {
		return PutAssistantSettings422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "missing body")}, nil
	}
	svc, err := h.assistantService()
	if err != nil {
		return nil, err
	}
	current, err := svc.Settings(ctx)
	if err != nil {
		return nil, err
	}

	in := assistant.SettingsInput{
		Enabled:                 current.Enabled,
		Provider:                current.Provider,
		BaseURL:                 current.BaseUrl,
		Model:                   current.Model,
		APIKey:                  request.Body.ApiKey,
		DataSharing:             current.DataSharing,
		MaxTurnSeconds:          current.MaxTurnSeconds,
		MaxToolCallsPerTurn:     current.MaxToolCallsPerTurn,
		MaxOutputTokensPerTurn:  current.MaxOutputTokensPerTurn,
		DailyTokenBudgetPerUser: current.DailyTokenBudgetPerUser,
		UpdatedBy:               pgtype.UUID{Bytes: [16]byte{}, Valid: false},
	}
	if request.Body.Enabled != nil {
		in.Enabled = *request.Body.Enabled
	}
	if request.Body.Provider != nil {
		p := string(*request.Body.Provider)
		in.Provider = &p
	}
	if request.Body.BaseUrl != nil {
		in.BaseURL = request.Body.BaseUrl
	}
	if request.Body.Model != nil {
		in.Model = request.Body.Model
	}
	if request.Body.DataSharing != nil {
		in.DataSharing = string(*request.Body.DataSharing)
	}
	if request.Body.MaxTurnSeconds != nil {
		in.MaxTurnSeconds = int32(*request.Body.MaxTurnSeconds)
	}
	if request.Body.MaxToolCallsPerTurn != nil {
		in.MaxToolCallsPerTurn = int32(*request.Body.MaxToolCallsPerTurn)
	}
	if request.Body.MaxOutputTokensPerTurn != nil {
		in.MaxOutputTokensPerTurn = int32(*request.Body.MaxOutputTokensPerTurn)
	}
	if request.Body.DailyTokenBudgetPerUser != nil {
		in.DailyTokenBudgetPerUser = int64(*request.Body.DailyTokenBudgetPerUser)
	}
	if p, err := authz.PrincipalFromContext(ctx); err == nil {
		in.UpdatedBy = p.UserID
	}

	if in.MaxTurnSeconds < 1 || in.MaxToolCallsPerTurn < 1 || in.MaxOutputTokensPerTurn < 1 || in.DailyTokenBudgetPerUser < 0 {
		return PutAssistantSettings422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "limits must be positive")}, nil
	}
	if in.Enabled {
		if in.Provider == nil || *in.Provider == "" || in.Model == nil || *in.Model == "" {
			return PutAssistantSettings422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "provider and model are required when the assistant is enabled")}, nil
		}
		if *in.Provider == "openai_compatible" && (in.BaseURL == nil || *in.BaseURL == "") {
			return PutAssistantSettings422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "base_url is required for an OpenAI-compatible provider")}, nil
		}
	}

	row, err := svc.UpdateSettings(ctx, in)
	if err != nil {
		if errors.Is(err, assistant.ErrKeyringUnavailable) {
			return PutAssistantSettings422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("encryption_unavailable", "BALEIA_ENCRYPTION_KEY is not configured")}, nil
		}
		return nil, err
	}
	h.audited(ctx, audit.Event{
		Action: audit.AssistantSettingsUpdate, ResourceType: "assistant_settings", Outcome: audit.Allowed,
		Changes: map[string]audit.Change{
			"enabled":      {To: row.Enabled},
			"provider":     {To: deref(row.Provider)},
			"model":        {To: deref(row.Model)},
			"data_sharing": {To: row.DataSharing},
			// api_key is deliberately absent.
		},
	})
	if h.assistantRunner != nil && row.Enabled && row.Provider != nil && row.Model != nil {
		key, _ := svc.APIKey(ctx)
		h.assistantRunner.SetProvider(llm.NewHTTPProvider(*row.Provider, deref(row.BaseUrl), key, *row.Model))
	}
	return PutAssistantSettings200JSONResponse(assistantSettingsToPublic(row)), nil
}

// TestAssistant — POST /admin/assistant/test. Sends a one-token prompt to the
// configured provider and reports latency or a sanitized error.
func (h *Handlers) TestAssistant(ctx context.Context, _ TestAssistantRequestObject) (TestAssistantResponseObject, error) {
	if auth.RoleFromContext(ctx) != "admin" {
		return TestAssistant403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}
	svc, err := h.assistantService()
	if err != nil {
		return nil, err
	}
	row, err := svc.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if !row.Enabled || row.Provider == nil || row.Model == nil {
		msg := "provider and model are not configured"
		return TestAssistant200JSONResponse(AssistantTestResult{Ok: false, Error: &msg}), nil
	}
	key, err := svc.APIKey(ctx)
	if err != nil || key == "" {
		msg := "api key is not configured"
		return TestAssistant200JSONResponse(AssistantTestResult{Ok: false, Error: &msg}), nil
	}
	provider := llm.NewHTTPProvider(*row.Provider, deref(row.BaseUrl), key, *row.Model)
	start := time.Now()
	_, err = provider.Stream(ctx, llm.Request{
		Messages:  []llm.Message{{Role: "user", Content: "reply with the single word ok"}},
		MaxTokens: 1,
	}, nil)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		msg := "provider rejected the test request"
		return TestAssistant200JSONResponse(AssistantTestResult{Ok: false, LatencyMs: &latency, Error: &msg}), nil
	}
	return TestAssistant200JSONResponse(AssistantTestResult{Ok: true, LatencyMs: &latency}), nil
}

// ListAssistantConversations — GET /assistant/conversations?workspace_id=…
func (h *Handlers) ListAssistantConversations(ctx context.Context, request ListAssistantConversationsRequestObject) (ListAssistantConversationsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListAssistantConversations401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	wsID := pgtype.UUID{Bytes: [16]byte(request.Params.WorkspaceId), Valid: true}
	if err := requireWorkspaceAccess(ctx, h.queries, request.Params.WorkspaceId); err != nil {
		return ListAssistantConversations404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	}
	svc, err := h.assistantService()
	if err != nil {
		return nil, err
	}
	rows, err := svc.ListConversations(ctx, p.UserID, wsID, 50)
	if err != nil {
		return nil, err
	}
	out := make([]AssistantConversation, 0, len(rows))
	for _, row := range rows {
		out = append(out, assistantConversationToPublic(row))
	}
	return ListAssistantConversations200JSONResponse(out), nil
}

// CreateAssistantConversation — POST /assistant/conversations.
func (h *Handlers) CreateAssistantConversation(ctx context.Context, request CreateAssistantConversationRequestObject) (CreateAssistantConversationResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateAssistantConversation401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	if request.Body == nil {
		return CreateAssistantConversation422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "missing body")}, nil
	}
	wsID := pgtype.UUID{Bytes: [16]byte(request.Body.WorkspaceId), Valid: true}
	if err := requireWorkspaceAccess(ctx, h.queries, request.Body.WorkspaceId); err != nil {
		return CreateAssistantConversation404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "workspace not found"}}, nil
	}
	svc, err := h.assistantService()
	if err != nil {
		return nil, err
	}
	row, err := svc.CreateConversation(ctx, p.UserID, wsID)
	if err != nil {
		return nil, err
	}
	return CreateAssistantConversation201JSONResponse(assistantConversationToPublic(row)), nil
}

// GetAssistantConversation — GET /assistant/conversations/{id}. Conversations
// are private: another user, admins included, gets a 404.
func (h *Handlers) GetAssistantConversation(ctx context.Context, request GetAssistantConversationRequestObject) (GetAssistantConversationResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetAssistantConversation401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	svc, err := h.assistantService()
	if err != nil {
		return nil, err
	}
	id := pgtype.UUID{Bytes: [16]byte(request.Id), Valid: true}
	conv, err := svc.GetConversation(ctx, p.UserID, id)
	if err != nil {
		if errors.Is(err, assistant.ErrNotFound) {
			return GetAssistantConversation404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "conversation not found"}}, nil
		}
		return nil, err
	}
	msgs, err := svc.ListMessages(ctx, id, 0, 500)
	if err != nil {
		return nil, err
	}
	out := AssistantConversationDetail{
		Id:          openapi_types.UUID(conv.ID.Bytes),
		WorkspaceId: openapi_types.UUID(conv.WorkspaceID.Bytes),
		Title:       conv.Title,
		State:       AssistantConversationDetailState(conv.State),
		LastSeq:     int(conv.LastSeq),
		CreatedAt:   pgTimestampToTime(conv.CreatedAt),
		UpdatedAt:   pgTimestampToTime(conv.UpdatedAt),
		Messages:    make([]AssistantMessage, 0, len(msgs)),
	}
	for _, m := range msgs {
		out.Messages = append(out.Messages, assistantMessageToPublic(m))
	}
	return GetAssistantConversation200JSONResponse(out), nil
}

// SendAssistantMessage — POST /assistant/conversations/{id}/messages. The turn
// runs in the background; the response carries the accepted user seq.
func (h *Handlers) SendAssistantMessage(ctx context.Context, request SendAssistantMessageRequestObject) (SendAssistantMessageResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SendAssistantMessage401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	if request.Body == nil {
		return SendAssistantMessage422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "missing body")}, nil
	}
	text := strings.TrimSpace(request.Body.Text)
	if text == "" {
		return SendAssistantMessage422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "text is required")}, nil
	}
	if _, err := h.assistantService(); err != nil {
		return nil, err
	}
	if h.assistantRunner == nil {
		return nil, assistant.ErrRunnerUnavailable
	}
	id := pgtype.UUID{Bytes: [16]byte(request.Id), Valid: true}
	seq, err := h.assistantRunner.Start(ctx, p.UserID, id, text)
	switch {
	case errors.Is(err, assistant.ErrNotFound):
		return SendAssistantMessage404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "conversation not found"}}, nil
	case errors.Is(err, assistant.ErrTurnRunning):
		return SendAssistantMessage409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: "turn_running", Message: "a turn is already running"}}, nil
	case errors.Is(err, assistant.ErrSettingsInvalid):
		return SendAssistantMessage422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("assistant_disabled", "the assistant is disabled or not configured")}, nil
	case err != nil:
		return nil, err
	}
	return SendAssistantMessage202JSONResponse(SendAssistantMessageResponse{Seq: int(seq)}), nil
}

// CancelAssistantTurn — POST /assistant/conversations/{id}/cancel.
func (h *Handlers) CancelAssistantTurn(ctx context.Context, request CancelAssistantTurnRequestObject) (CancelAssistantTurnResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CancelAssistantTurn401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	svc, err := h.assistantService()
	if err != nil {
		return nil, err
	}
	id := pgtype.UUID{Bytes: [16]byte(request.Id), Valid: true}
	if _, err := svc.GetConversation(ctx, p.UserID, id); err != nil {
		if errors.Is(err, assistant.ErrNotFound) {
			return CancelAssistantTurn404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "conversation not found"}}, nil
		}
		return nil, err
	}
	if h.assistantRunner != nil {
		h.assistantRunner.Cancel(id)
	}
	return CancelAssistantTurn202Response{}, nil
}

// StreamAssistantConversation — GET /assistant/conversations/{id}/events. The
// stream replays persisted messages after after_seq and then forwards live
// messages/deltas. The generated Visit uses io.Copy, so the body implements
// io.WriterTo to flush each frame through http.Flusher.
func (h *Handlers) StreamAssistantConversation(ctx context.Context, request StreamAssistantConversationRequestObject) (StreamAssistantConversationResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return StreamAssistantConversation401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	svc, err := h.assistantService()
	if err != nil {
		return nil, err
	}
	id := pgtype.UUID{Bytes: [16]byte(request.Id), Valid: true}
	if _, err := svc.GetConversation(ctx, p.UserID, id); err != nil {
		if errors.Is(err, assistant.ErrNotFound) {
			return StreamAssistantConversation404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "conversation not found"}}, nil
		}
		return nil, err
	}
	after := int32(0)
	if request.Params.AfterSeq != nil {
		after = int32(*request.Params.AfterSeq)
	}
	rows, err := svc.ListMessages(ctx, id, after, 500)
	if err != nil {
		return nil, err
	}
	replay := make([]AssistantMessage, 0, len(rows))
	for _, row := range rows {
		replay = append(replay, assistantMessageToPublic(row))
	}
	stream := &assistantSSE{ctx: ctx, replay: replay, lastSeq: after, ping: time.NewTicker(15 * time.Second)}
	if h.assistantRunner != nil {
		stream.events, stream.unsubscribe = h.assistantRunner.Hub().Subscribe(id.Bytes)
	}
	return StreamAssistantConversation200TexteventStreamResponse{Body: stream}, nil
}

// assistantSSE is the SSE body. WriteTo is what io.Copy uses, which lets each
// frame flush immediately instead of waiting for the HTTP buffer.
type assistantSSE struct {
	ctx         context.Context
	replay      []AssistantMessage
	events      <-chan assistant.Event
	unsubscribe func()
	ping        *time.Ticker
	lastSeq     int32
}

func (s *assistantSSE) Read([]byte) (int, error) { return 0, io.EOF }

func (s *assistantSSE) Close() error {
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
	if s.ping != nil {
		s.ping.Stop()
	}
	return nil
}

func (s *assistantSSE) WriteTo(w io.Writer) (int64, error) {
	var written int64
	flusher, _ := w.(http.Flusher)
	write := func(frame string) error {
		n, err := io.WriteString(w, frame)
		written += int64(n)
		if flusher != nil {
			flusher.Flush()
		}
		return err
	}
	for _, msg := range s.replay {
		raw, err := json.Marshal(msg)
		if err != nil {
			return written, err
		}
		if err := write("event: message\ndata: " + string(raw) + "\n\n"); err != nil {
			return written, err
		}
		if msg.Seq > int(s.lastSeq) {
			s.lastSeq = int32(msg.Seq)
		}
	}
	var ping <-chan time.Time
	if s.ping != nil {
		ping = s.ping.C
	}
	for {
		select {
		case <-s.ctx.Done():
			return written, nil
		case ev := <-s.events:
			switch ev.Type {
			case "message":
				if ev.Seq <= s.lastSeq {
					continue
				}
				s.lastSeq = ev.Seq
				msg := AssistantMessage{
					Seq:       int(ev.Seq),
					Role:      AssistantMessageRole(ev.Role),
					Content:   json.RawMessage(ev.Content),
					CreatedAt: ev.CreatedAt,
				}
				raw, err := json.Marshal(msg)
				if err != nil {
					return written, err
				}
				if err := write("event: message\ndata: " + string(raw) + "\n\n"); err != nil {
					return written, err
				}
			case "delta":
				raw, _ := json.Marshal(map[string]string{"text": ev.Delta})
				if err := write("event: delta\ndata: " + string(raw) + "\n\n"); err != nil {
					return written, err
				}
			}
		case <-ping:
			if err := write(": ping\n\n"); err != nil {
				return written, err
			}
		}
	}
}

func (h *Handlers) DeleteAssistantConversation(ctx context.Context, request DeleteAssistantConversationRequestObject) (DeleteAssistantConversationResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteAssistantConversation401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}
	svc, err := h.assistantService()
	if err != nil {
		return nil, err
	}
	id := pgtype.UUID{Bytes: [16]byte(request.Id), Valid: true}
	if err := svc.DeleteConversation(ctx, p.UserID, id); err != nil {
		if errors.Is(err, assistant.ErrNotFound) {
			return DeleteAssistantConversation404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "conversation not found"}}, nil
		}
		return nil, err
	}
	return DeleteAssistantConversation204Response{}, nil
}
