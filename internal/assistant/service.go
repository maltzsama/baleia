// Package assistant owns the embedded assistant's settings, its per-user ×
// workspace agent and the persisted conversations (#62). It never reaches the
// database, engines or object storage for *data*: the turn engine in this
// package calls the same MCP tools external agents use.
package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	adb "github.com/galileostudio/baleia/internal/db/assistant"
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
	store   Store
	keyring *crypto.Keyring
}

// New builds the service. keyring may be nil in tests that never write a key.
func New(store Store, keyring *crypto.Keyring) *Service {
	return &Service{store: store, keyring: keyring}
}

// ── settings ────────────────────────────────────────────────────────────────

// Settings returns the single settings row.
func (s *Service) Settings(ctx context.Context) (adb.AssistantSetting, error) {
	row, err := s.store.GetAssistantSettings(ctx)
	if errors.Is(err, db.ErrNoRows) {
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
	UpdatedBy               db.UUID
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
	return s.store.UpdateAssistantSettings(ctx, adb.UpdateAssistantSettingsParams{
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
func (s *Service) EnsureAgent(ctx context.Context, ownerID, workspaceID db.UUID) (adb.Agent, error) {
	return s.store.EnsureAssistantAgent(ctx, ownerID, workspaceID,
		"Assistant", "Embedded assistant acting on behalf of its owner")
}

// ── conversations ───────────────────────────────────────────────────────────

// CreateConversation provisions the agent and starts an empty conversation.
func (s *Service) CreateConversation(ctx context.Context, userID, workspaceID db.UUID) (adb.AssistantConversation, error) {
	agent, err := s.EnsureAgent(ctx, userID, workspaceID)
	if err != nil {
		return adb.AssistantConversation{}, err
	}
	return s.store.CreateConversation(ctx, adb.CreateConversationParams{
		UserID: userID, WorkspaceID: workspaceID, AgentID: agent.ID, Title: "",
	})
}

// GetConversation is scoped to the caller: another user's conversation is a
// 404, admins included.
func (s *Service) GetConversation(ctx context.Context, userID, id db.UUID) (adb.AssistantConversation, error) {
	row, err := s.store.GetConversation(ctx, adb.GetConversationParams{ID: id, UserID: userID})
	if errors.Is(err, db.ErrNoRows) {
		return adb.AssistantConversation{}, ErrNotFound
	}
	return row, err
}

// ListConversations returns the caller's conversations in a workspace, newest
// first.
func (s *Service) ListConversations(ctx context.Context, userID, workspaceID db.UUID, limit int32) ([]adb.AssistantConversation, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.store.ListConversations(ctx, adb.ListConversationsParams{
		UserID: userID, WorkspaceID: workspaceID, Limit: limit,
	})
}

// DeleteConversation hard-deletes the caller's conversation and its messages.
func (s *Service) DeleteConversation(ctx context.Context, userID, id db.UUID) error {
	if _, err := s.GetConversation(ctx, userID, id); err != nil {
		return err
	}
	return s.store.DeleteConversation(ctx, adb.DeleteConversationParams{ID: id, UserID: userID})
}

// ── messages ────────────────────────────────────────────────────────────────

// AppendMessage persists one typed message and returns the stored row with its
// sequence number. content is marshalled as JSON.
func (s *Service) AppendMessage(ctx context.Context, conversationID db.UUID, role string, content any, inputTokens, outputTokens *int32) (adb.AssistantMessage, error) {
	raw, err := json.Marshal(content)
	if err != nil {
		return adb.AssistantMessage{}, err
	}
	return s.store.AppendMessage(ctx, adb.AppendMessageParams{
		ConversationID: conversationID,
		Role:           role,
		Content:        raw,
		InputTokens:    inputTokens,
		OutputTokens:   outputTokens,
	})
}

// ListMessages returns messages after a sequence number, oldest first.
func (s *Service) ListMessages(ctx context.Context, conversationID db.UUID, afterSeq, limit int32) ([]adb.AssistantMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	return s.store.ListMessages(ctx, adb.ListMessagesParams{
		ConversationID: conversationID, Seq: afterSeq, Limit: limit,
	})
}

// ── turn lifecycle ──────────────────────────────────────────────────────────

// Claim starts a turn or returns ErrTurnRunning when one is already in flight.
func (s *Service) Claim(ctx context.Context, userID, id db.UUID, owner string) (adb.AssistantConversation, error) {
	row, err := s.store.ClaimConversation(ctx, adb.ClaimConversationParams{
		ID: id, UserID: userID, TurnOwner: &owner,
	})
	if errors.Is(err, db.ErrNoRows) {
		return adb.AssistantConversation{}, ErrTurnRunning
	}
	return row, err
}

// Release returns a conversation to idle after a turn. turnOwner is the token
// the claim returned; a stale runner that lost the conversation cannot release
// the reclaimed turn.
func (s *Service) Release(ctx context.Context, id db.UUID, turnOwner string) error {
	return s.store.ReleaseConversation(ctx, id, turnOwner)
}

// Interrupt marks a conversation interrupted (cancel, crash recovery).
func (s *Service) Interrupt(ctx context.Context, id db.UUID) error {
	return s.store.SetConversationInterrupted(ctx, id)
}

// Heartbeat refreshes the turn lease.
func (s *Service) Heartbeat(ctx context.Context, id db.UUID) error {
	return s.store.HeartbeatConversation(ctx, id)
}

// InterruptStale recovers turns whose replica stopped heartbeating.
func (s *Service) InterruptStale(ctx context.Context) ([]db.UUID, error) {
	return s.store.InterruptStaleConversations(ctx)
}

// SetTitle names a conversation once, from its first user message.
func (s *Service) SetTitle(ctx context.Context, id db.UUID, title string) error {
	return s.store.SetConversationTitle(ctx, adb.SetConversationTitleParams{ID: id, Title: title})
}

// OverBudget reports whether the user's last 24 h of tokens reached the
// instance budget. A zero budget disables the check.
func (s *Service) OverBudget(ctx context.Context, userID db.UUID, budget int64) (bool, error) {
	if budget <= 0 {
		return false, nil
	}
	used, err := s.store.CountUserTokensSince(ctx, adb.CountUserTokensSinceParams{
		UserID:    userID,
		CreatedAt: db.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
	})
	if err != nil {
		return false, err
	}
	return used >= budget, nil
}
