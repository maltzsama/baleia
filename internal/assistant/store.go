package assistant

import (
	"context"

	adb "github.com/galileostudio/baleia/internal/db/assistant"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store is the assistant's persistence boundary (#774). The GORM-backed
// implementation lives in internal/store/repo/assistant.go (ADR 0020); the
// signatures match the retired sqlc queries so the existing error checks keep
// their meaning: a missing row is pgx.ErrNoRows.
type Store interface {
	// Settings
	GetAssistantSettings(ctx context.Context) (adb.AssistantSetting, error)
	UpdateAssistantSettings(ctx context.Context, arg adb.UpdateAssistantSettingsParams) (adb.AssistantSetting, error)

	// Assistant agent
	EnsureAssistantAgent(ctx context.Context, ownerID, workspaceID pgtype.UUID, name, description string) (adb.Agent, error)

	// Conversations
	CreateConversation(ctx context.Context, arg adb.CreateConversationParams) (adb.AssistantConversation, error)
	GetConversation(ctx context.Context, arg adb.GetConversationParams) (adb.AssistantConversation, error)
	ListConversations(ctx context.Context, arg adb.ListConversationsParams) ([]adb.AssistantConversation, error)
	DeleteConversation(ctx context.Context, arg adb.DeleteConversationParams) error

	// Turn lifecycle
	ClaimConversation(ctx context.Context, arg adb.ClaimConversationParams) (adb.AssistantConversation, error)
	ReleaseConversation(ctx context.Context, id pgtype.UUID, turnOwner string) error
	SetConversationInterrupted(ctx context.Context, id pgtype.UUID) error
	HeartbeatConversation(ctx context.Context, id pgtype.UUID) error
	InterruptStaleConversations(ctx context.Context) ([]pgtype.UUID, error)
	SetConversationTitle(ctx context.Context, arg adb.SetConversationTitleParams) error

	// Messages
	AppendMessage(ctx context.Context, arg adb.AppendMessageParams) (adb.AssistantMessage, error)
	ListMessages(ctx context.Context, arg adb.ListMessagesParams) ([]adb.AssistantMessage, error)
	LatestConversationMessage(ctx context.Context, conversationID pgtype.UUID) (adb.AssistantMessage, error)
	CountUserTokensSince(ctx context.Context, arg adb.CountUserTokensSinceParams) (int64, error)
}
