// Package assistant keeps the hand-maintained data-transfer types the store and
// its callers share. They were extracted from the retired generated query
// code and are maintained here from now on.
//
//nolint:revive // DTO fields keep the SQL column spelling and the published JSON contract.
package assistant

import (
	"github.com/jackc/pgx/v5/pgtype"
)

type AddAssistantAgentWorkspaceParams struct {
	AgentID     pgtype.UUID `json:"agent_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type AppendMessageParams struct {
	ConversationID pgtype.UUID `json:"conversation_id"`
	Role           string      `json:"role"`
	Content        []byte      `json:"content"`
	InputTokens    *int32      `json:"input_tokens"`
	OutputTokens   *int32      `json:"output_tokens"`
}

type ClaimConversationParams struct {
	ID        pgtype.UUID `json:"id"`
	UserID    pgtype.UUID `json:"user_id"`
	TurnOwner *string     `json:"turn_owner"`
}

type CountUserTokensSinceParams struct {
	UserID    pgtype.UUID        `json:"user_id"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
}

type CreateAssistantAgentParams struct {
	OwnerID     pgtype.UUID `json:"owner_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
}

type CreateConversationParams struct {
	UserID      pgtype.UUID `json:"user_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	AgentID     pgtype.UUID `json:"agent_id"`
	Title       string      `json:"title"`
}

type DeleteConversationParams struct {
	ID     pgtype.UUID `json:"id"`
	UserID pgtype.UUID `json:"user_id"`
}

type FindAssistantAgentParams struct {
	OwnerID     pgtype.UUID `json:"owner_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

type GetConversationParams struct {
	ID     pgtype.UUID `json:"id"`
	UserID pgtype.UUID `json:"user_id"`
}

type ListConversationsParams struct {
	UserID      pgtype.UUID `json:"user_id"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	Limit       int32       `json:"limit"`
}

type ListMessagesParams struct {
	ConversationID pgtype.UUID `json:"conversation_id"`
	Seq            int32       `json:"seq"`
	Limit          int32       `json:"limit"`
}

type SetConversationTitleParams struct {
	ID    pgtype.UUID `json:"id"`
	Title string      `json:"title"`
}

type UpdateAssistantSettingsParams struct {
	Enabled                 bool        `json:"enabled"`
	Provider                *string     `json:"provider"`
	BaseUrl                 *string     `json:"base_url"`
	Model                   *string     `json:"model"`
	ApiKeyEnc               *string     `json:"api_key_enc"`
	DataSharing             string      `json:"data_sharing"`
	MaxTurnSeconds          int32       `json:"max_turn_seconds"`
	MaxToolCallsPerTurn     int32       `json:"max_tool_calls_per_turn"`
	MaxOutputTokensPerTurn  int32       `json:"max_output_tokens_per_turn"`
	DailyTokenBudgetPerUser int64       `json:"daily_token_budget_per_user"`
	UpdatedBy               pgtype.UUID `json:"updated_by"`
}
