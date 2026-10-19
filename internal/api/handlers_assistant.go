package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/galileostudio/baleia/internal/assistant"
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
	return PutAssistantSettings200JSONResponse(assistantSettingsToPublic(row)), nil
}

// TestAssistant — POST /admin/assistant/test. The provider adapters land with
// the turn engine; until then this reports the configuration gap explicitly.
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
	msg := "provider is not configured"
	if !row.Enabled {
		msg = "the assistant is disabled"
	} else if row.Provider == nil || row.Model == nil {
		msg = "provider and model are not configured"
	}
	return TestAssistant200JSONResponse(AssistantTestResult{Ok: false, Error: &msg}), nil
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

// DeleteAssistantConversation — DELETE /assistant/conversations/{id}.
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
