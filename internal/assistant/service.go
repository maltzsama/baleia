// Package assistant owns the embedded assistant's settings, its per-user ×
// workspace agent and the persisted conversations (#62). It never reaches the
// database, engines or object storage for *data*: the turn engine in this
// package calls the same MCP tools external agents use.
package assistant

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/galileostudio/baleia/internal/crypto"
	adb "github.com/galileostudio/baleia/internal/db/assistant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// apiKeyAAD is the keyring AAD for the provider API key (#19). It is a
// constant so a key sealed by one replication can be opened by another.
const apiKeyAAD = "assistant:api_key"

var (
	// ErrNotFound is returned for a missing conversation or settings row.
	ErrNotFound = errors.New("assistant: not found")
	// ErrTurnRunning means the conversation already has an in-flight turn.
	ErrTurnRunning = errors.New("assistant: a turn is already running")
	// ErrKeyringUnavailable means BALEIA_ENCRYPTION_KEY is not configured.
	ErrKeyringUnavailable = errors.New("assistant: encryption key is not configured")
	// ErrSettingsInvalid means the provider settings are incomplete.
	ErrSettingsInvalid = errors.New("assistant: invalid settings")
)

// Service is the assistant's persistence boundary.
type Service struct {
	q       *adb.Queries
	keyring *crypto.Keyring
}

// New builds the service. keyring may be nil in tests that never write a key.
func New(q *adb.Queries, keyring *crypto.Keyring) *Service {
	return &Service{q: q, keyring: keyring}
}

// ── settings ────────────────────────────────────────────────────────────────

// Settings returns the single settings row.
func (s *Service) Settings(ctx context.Context) (adb.AssistantSetting, error) {
	row, err := s.q.GetAssistantSettings(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return adb.AssistantSetting{}, ErrNotFound
	}
	return row, err
}

// SettingsInput is the editable part of the settings row. APIKey is plaintext
// and write-only: nil or an empty string keeps the stored key.
type SettingsInput struct {
	Enabled                 bool
	Provider                *string
	BaseURL                 *string
	Model                   *string
	APIKey                  *string
	DataSharing             string
	MaxTurnSeconds          int32
	MaxToolCallsPerTurn     int32
	MaxOutputTokensPerTurn  int32
	DailyTokenBudgetPerUser int64
	UpdatedBy               pgtype.UUID
}

// UpdateSettings merges the input over the current row and seals a new API key
// when one is given. The key is never returned.
func (s *Service) UpdateSettings(ctx context.Context, in SettingsInput) (adb.AssistantSetting, error) {
	current, err := s.Settings(ctx)
	if err != nil {
		return adb.AssistantSetting{}, err
	}
	apiKeyEnc := current.ApiKeyEnc
	if in.APIKey != nil && *in.APIKey != "" {
		if s.keyring == nil {
			return adb.AssistantSetting{}, ErrKeyringUnavailable
		}
		sealed, err := s.keyring.Seal([]byte(*in.APIKey), apiKeyAAD)
		if err != nil {
			return adb.AssistantSetting{}, err
		}
		apiKeyEnc = &sealed
	}
	return s.q.UpdateAssistantSettings(ctx, adb.UpdateAssistantSettingsParams{
		Enabled:                 in.Enabled,
		Provider:                in.Provider,
		BaseUrl:                 in.BaseURL,
		Model:                   in.Model,
		ApiKeyEnc:               apiKeyEnc,
		DataSharing:             in.DataSharing,
		MaxTurnSeconds:          in.MaxTurnSeconds,
		MaxToolCallsPerTurn:     in.MaxToolCallsPerTurn,
		MaxOutputTokensPerTurn:  in.MaxOutputTokensPerTurn,
		DailyTokenBudgetPerUser: in.DailyTokenBudgetPerUser,
		UpdatedBy:               in.UpdatedBy,
	})
}

// APIKey opens the sealed provider key. It is only used by the provider
// adapters and never by a response handler.
func (s *Service) APIKey(ctx context.Context) (string, error) {
	row, err := s.Settings(ctx)
	if err != nil {
		return "", err
	}
	if row.ApiKeyEnc == nil || *row.ApiKeyEnc == "" {
		return "", nil
	}
	if s.keyring == nil {
		return "", ErrKeyringUnavailable
	}
	raw, err := s.keyring.Open(*row.ApiKeyEnc, apiKeyAAD)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ── agent ───────────────────────────────────────────────────────────────────

// EnsureAgent returns the assistant agent for a user × workspace, creating it
// on first use. The agent is never listed or minted by the agents API.
func (s *Service) EnsureAgent(ctx context.Context, ownerID, workspaceID pgtype.UUID) (adb.Agent, error) {
	agent, err := s.q.FindAssistantAgent(ctx, adb.FindAssistantAgentParams{
		OwnerID: ownerID, WorkspaceID: workspaceID,
	})
	if err == nil {
		return agent, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return adb.Agent{}, err
	}
	agent, err = s.q.CreateAssistantAgent(ctx, adb.CreateAssistantAgentParams{
		OwnerID:     ownerID,
		Name:        "Assistant",
		Description: "Embedded assistant acting on behalf of its owner",
	})
	if err != nil {
		return adb.Agent{}, err
	}
	if err := s.q.AddAssistantAgentWorkspace(ctx, adb.AddAssistantAgentWorkspaceParams{
		AgentID: agent.ID, WorkspaceID: workspaceID,
	}); err != nil {
		return adb.Agent{}, err
	}
	return agent, nil
}

// ── conversations ───────────────────────────────────────────────────────────

// CreateConversation provisions the agent and starts an empty conversation.
func (s *Service) CreateConversation(ctx context.Context, userID, workspaceID pgtype.UUID) (adb.AssistantConversation, error) {
	agent, err := s.EnsureAgent(ctx, userID, workspaceID)
	if err != nil {
		return adb.AssistantConversation{}, err
	}
	return s.q.CreateConversation(ctx, adb.CreateConversationParams{
		UserID: userID, WorkspaceID: workspaceID, AgentID: agent.ID, Title: "",
	})
}

// GetConversation is scoped to the caller: another user's conversation is a
// 404, admins included.
func (s *Service) GetConversation(ctx context.Context, userID, id pgtype.UUID) (adb.AssistantConversation, error) {
	row, err := s.q.GetConversation(ctx, adb.GetConversationParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return adb.AssistantConversation{}, ErrNotFound
	}
	return row, err
}

// ListConversations returns the caller's conversations in a workspace, newest
// first.
func (s *Service) ListConversations(ctx context.Context, userID, workspaceID pgtype.UUID, limit int32) ([]adb.AssistantConversation, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.q.ListConversations(ctx, adb.ListConversationsParams{
		UserID: userID, WorkspaceID: workspaceID, Limit: limit,
	})
}

// DeleteConversation hard-deletes the caller's conversation and its messages.
func (s *Service) DeleteConversation(ctx context.Context, userID, id pgtype.UUID) error {
	if _, err := s.GetConversation(ctx, userID, id); err != nil {
		return err
	}
	return s.q.DeleteConversation(ctx, adb.DeleteConversationParams{ID: id, UserID: userID})
}

// ── messages ────────────────────────────────────────────────────────────────

// AppendMessage persists one typed message and returns the stored row with its
// sequence number. content is marshalled as JSON.
func (s *Service) AppendMessage(ctx context.Context, conversationID pgtype.UUID, role string, content any, inputTokens, outputTokens *int32) (adb.AssistantMessage, error) {
	raw, err := json.Marshal(content)
	if err != nil {
		return adb.AssistantMessage{}, err
	}
	return s.q.AppendMessage(ctx, adb.AppendMessageParams{
		ConversationID: conversationID,
		Role:           role,
		Content:        raw,
		InputTokens:    inputTokens,
		OutputTokens:   outputTokens,
	})
}

// ListMessages returns messages after a sequence number, oldest first.
func (s *Service) ListMessages(ctx context.Context, conversationID pgtype.UUID, afterSeq, limit int32) ([]adb.AssistantMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	return s.q.ListMessages(ctx, adb.ListMessagesParams{
		ConversationID: conversationID, Seq: afterSeq, Limit: limit,
	})
}
