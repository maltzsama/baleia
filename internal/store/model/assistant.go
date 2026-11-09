package model

import "time"

// AssistantConversation is the assistant_conversations table.
type AssistantConversation struct {
	ID              UUID       `gorm:"column:id;primaryKey;not null;check:assistant_conversations_state_check,((state IN ('idle', 'running', 'interrupted')))"`
	UserID          UUID       `gorm:"column:user_id;not null;index:assistant_conversations_user,priority:1"`
	WorkspaceID     UUID       `gorm:"column:workspace_id;not null"`
	AgentID         UUID       `gorm:"column:agent_id;not null"`
	Title           Text       `gorm:"column:title;not null;size:255;default:''"`
	State           Text       `gorm:"column:state;not null;size:255;default:'idle'"`
	TurnOwner       *string    `gorm:"column:turn_owner"`
	TurnHeartbeatAt *time.Time `gorm:"column:turn_heartbeat_at"`
	LastSeq         int32      `gorm:"column:last_seq;not null;default:0"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3);index:assistant_conversations_user,priority:2"`
	User            *User      `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
	Workspace       *Workspace `gorm:"foreignKey:WorkspaceID;references:ID;constraint:OnDelete:CASCADE"`
	Agent           *Agent     `gorm:"foreignKey:AgentID;references:ID;constraint:OnDelete:CASCADE"`
}

func (AssistantConversation) TableName() string { return "assistant_conversations" }

// AssistantMessage is the assistant_messages table.
type AssistantMessage struct {
	ConversationID UUID                   `gorm:"column:conversation_id;primaryKey;not null;check:assistant_messages_role_check,((role IN ('user', 'assistant', 'tool_call', 'tool_result', 'notice')))"`
	Seq            int32                  `gorm:"column:seq;primaryKey;not null"`
	Role           string                 `gorm:"column:role;not null"`
	Content        JSON                   `gorm:"column:content;not null"`
	InputTokens    *int32                 `gorm:"column:input_tokens"`
	OutputTokens   *int32                 `gorm:"column:output_tokens"`
	CreatedAt      time.Time              `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP(3);index:assistant_messages_usage,priority:1,where:output_tokens IS NOT NULL"`
	Conversation   *AssistantConversation `gorm:"foreignKey:ConversationID;references:ID;constraint:OnDelete:CASCADE"`
}

func (AssistantMessage) TableName() string { return "assistant_messages" }

// AssistantSetting is the assistant_settings table.
type AssistantSetting struct {
	ID                      bool      `gorm:"column:id;primaryKey;not null;default:true;check:assistant_settings_id_check,((id = TRUE))"`
	Enabled                 bool      `gorm:"column:enabled;not null;default:false;check:assistant_settings_provider_check,((provider IN ('anthropic', 'openai_compatible')))"`
	Provider                *string   `gorm:"column:provider;check:assistant_settings_data_sharing_check,((data_sharing IN ('metadata_only', 'metadata_and_samples')))"`
	BaseURL                 *string   `gorm:"column:base_url"`
	Model                   *string   `gorm:"column:model"`
	APIKeyEnc               *string   `gorm:"column:api_key_enc"`
	DataSharing             Text      `gorm:"column:data_sharing;not null;size:255;default:'metadata_only'"`
	MaxTurnSeconds          int32     `gorm:"column:max_turn_seconds;not null;default:180"`
	MaxToolCallsPerTurn     int32     `gorm:"column:max_tool_calls_per_turn;not null;default:10"`
	MaxOutputTokensPerTurn  int32     `gorm:"column:max_output_tokens_per_turn;not null;default:4096"`
	DailyTokenBudgetPerUser int64     `gorm:"column:daily_token_budget_per_user;not null;default:500000"`
	UpdatedBy               *UUID     `gorm:"column:updated_by"`
	UpdatedAt               time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP(3)"`
	UpdatedByRef            *User     `gorm:"foreignKey:UpdatedBy;references:ID;constraint:OnDelete:SET NULL"`
}

func (AssistantSetting) TableName() string { return "assistant_settings" }
