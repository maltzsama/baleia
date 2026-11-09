// Querier is the assistant store contract the service consumes. The GORM store
// implements it.
package assistant

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
)

type Querier interface {
	AddAssistantAgentWorkspace(ctx context.Context, arg AddAssistantAgentWorkspaceParams) error
	// Bumps last_seq and inserts atomically, so SSE sequence numbers have no gaps.
	AppendMessage(ctx context.Context, arg AppendMessageParams) (AssistantMessage, error)
	// Exactly one turn may run per conversation. Zero rows means a turn is running.
	ClaimConversation(ctx context.Context, arg ClaimConversationParams) (AssistantConversation, error)
	CountUserTokensSince(ctx context.Context, arg CountUserTokensSinceParams) (int64, error)
	CreateAssistantAgent(ctx context.Context, arg CreateAssistantAgentParams) (Agent, error)
	CreateConversation(ctx context.Context, arg CreateConversationParams) (AssistantConversation, error)
	DeleteConversation(ctx context.Context, arg DeleteConversationParams) error
	// One assistant agent per owner × workspace, provisioned on first use.
	FindAssistantAgent(ctx context.Context, arg FindAssistantAgentParams) (Agent, error)
	// Assistant settings, conversations, messages and the assistant agent (#62).
	// This file is the only source of the assistant DTO package; the service may
	// not use the main db package for data access.
	GetAssistantSettings(ctx context.Context) (AssistantSetting, error)
	GetConversation(ctx context.Context, arg GetConversationParams) (AssistantConversation, error)
	HeartbeatConversation(ctx context.Context, id db.UUID) error
	// A crashed replica leaves a running turn behind; the sweeper marks it
	// interrupted so the user can retry.
	InterruptStaleConversations(ctx context.Context) ([]db.UUID, error)
	LatestConversationMessage(ctx context.Context, conversationID db.UUID) (AssistantMessage, error)
	ListConversations(ctx context.Context, arg ListConversationsParams) ([]AssistantConversation, error)
	ListMessages(ctx context.Context, arg ListMessagesParams) ([]AssistantMessage, error)
	ReleaseConversation(ctx context.Context, id db.UUID) error
	SetConversationInterrupted(ctx context.Context, id db.UUID) error
	SetConversationTitle(ctx context.Context, arg SetConversationTitleParams) error
	UpdateAssistantSettings(ctx context.Context, arg UpdateAssistantSettingsParams) (AssistantSetting, error)
}
