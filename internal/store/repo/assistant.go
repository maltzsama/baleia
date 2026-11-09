package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/assistant"
	"github.com/galileostudio/baleia/internal/db"
	adb "github.com/galileostudio/baleia/internal/db/assistant"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ assistant.Store = (*Repo)(nil)

// staleTurnAge is how long a running turn may stay without a heartbeat before
// the sweeper marks it interrupted (#62).
const staleTurnAge = 60 * time.Second

// ── settings ──────────────────────────────────────────────────────────────────

// GetAssistantSettings returns the single settings row or db.ErrNoRows.
func (r *Repo) GetAssistantSettings(ctx context.Context) (adb.AssistantSetting, error) {
	var row model.AssistantSetting
	err := r.Users.db.WithContext(ctx).Where("id = ?", true).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return adb.AssistantSetting{}, db.ErrNoRows
	}
	if err != nil {
		return adb.AssistantSetting{}, err
	}
	return toAssistantSetting(row), nil
}

// UpdateAssistantSettings merges the configuration over the single row.
func (r *Repo) UpdateAssistantSettings(ctx context.Context, arg adb.UpdateAssistantSettingsParams) (adb.AssistantSetting, error) {
	var out adb.AssistantSetting
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.AssistantSetting{}).Where("id = ?", true).
			Updates(map[string]any{
				"enabled":                     arg.Enabled,
				"provider":                    arg.Provider,
				"base_url":                    arg.BaseUrl,
				"model":                       arg.Model,
				"api_key_enc":                 arg.ApiKeyEnc,
				"data_sharing":                arg.DataSharing,
				"max_turn_seconds":            arg.MaxTurnSeconds,
				"max_tool_calls_per_turn":     arg.MaxToolCallsPerTurn,
				"max_output_tokens_per_turn":  arg.MaxOutputTokensPerTurn,
				"daily_token_budget_per_user": arg.DailyTokenBudgetPerUser,
				"updated_by":                  modelUUIDPtr(arg.UpdatedBy),
				"updated_at":                  time.Now().UTC(),
			}).Error; err != nil {
			return err
		}
		var row model.AssistantSetting
		if err := tx.Where("id = ?", true).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return db.ErrNoRows
			}
			return err
		}
		out = toAssistantSetting(row)
		return nil
	})
	if err != nil {
		return adb.AssistantSetting{}, err
	}
	return out, nil
}

// ── assistant agent ───────────────────────────────────────────────────────────

// FindAssistantAgent returns the owner × workspace assistant agent or
// db.ErrNoRows.
func (r *Repo) FindAssistantAgent(ctx context.Context, arg adb.FindAssistantAgentParams) (adb.Agent, error) {
	return findAssistantAgent(r.Users.db.WithContext(ctx), arg.OwnerID, arg.WorkspaceID)
}

// EnsureAssistantAgent returns the owner × workspace assistant agent,
// provisioning it on first use. The owner's row is locked for the duration, so
// concurrent first conversations for the same owner create one agent: the loser
// waits, then finds the committed agent. A binding failure rolls the created
// agent back (ADR 0020: row locks, never advisory locks).
func (r *Repo) EnsureAssistantAgent(ctx context.Context, ownerID, workspaceID db.UUID, name, description string) (adb.Agent, error) {
	var out adb.Agent
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owner model.User
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").
			Where("id = ?", UUIDString(ownerID)).
			First(&owner).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return db.ErrNoRows
		}
		if err != nil {
			return err
		}
		found, err := findAssistantAgent(tx, ownerID, workspaceID)
		if err == nil {
			out = found
			return nil
		}
		if !errors.Is(err, db.ErrNoRows) {
			return err
		}
		agent, err := createAssistantAgent(tx, adb.CreateAssistantAgentParams{
			OwnerID: ownerID, Name: name, Description: description,
		})
		if err != nil {
			return err
		}
		if err := addAssistantAgentWorkspace(tx, adb.AddAssistantAgentWorkspaceParams{
			AgentID: agent.ID, WorkspaceID: workspaceID,
		}); err != nil {
			return err
		}
		out = agent
		return nil
	})
	if err != nil {
		return adb.Agent{}, err
	}
	return out, nil
}

// CreateAssistantAgent inserts an assistant agent with an application-generated
// id.
func (r *Repo) CreateAssistantAgent(ctx context.Context, arg adb.CreateAssistantAgentParams) (adb.Agent, error) {
	return createAssistantAgent(r.Users.db.WithContext(ctx), arg)
}

// AddAssistantAgentWorkspace binds the agent to the workspace; a repeated bind
// is a no-op.
func (r *Repo) AddAssistantAgentWorkspace(ctx context.Context, arg adb.AddAssistantAgentWorkspaceParams) error {
	return addAssistantAgentWorkspace(r.Users.db.WithContext(ctx), arg)
}

func findAssistantAgent(db *gorm.DB, ownerID, workspaceID db.UUID) (adb.Agent, error) {
	var row model.Agent
	err := db.
		Joins("JOIN agent_workspaces aw ON aw.agent_id = agents.id").
		Where("agents.owner_id = ? AND agents.kind = ? AND aw.workspace_id = ?",
			UUIDString(ownerID), "assistant", UUIDString(workspaceID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return adb.Agent{}, gorm.ErrRecordNotFound
	}
	if err != nil {
		return adb.Agent{}, err
	}
	return toAssistantAgent(row), nil
}

func createAssistantAgent(db *gorm.DB, arg adb.CreateAssistantAgentParams) (adb.Agent, error) {
	now := time.Now().UTC()
	row := model.Agent{
		ID:              model.UUID(NewUUID()),
		OwnerID:         model.UUID(UUIDString(arg.OwnerID)),
		Kind:            "assistant",
		Name:            arg.Name,
		Description:     model.Text(arg.Description),
		Status:          "active",
		CreatedAt:       now,
		UpdatedAt:       now,
		Policy:          model.JSON("{}"),
		PolicyUpdatedAt: now,
	}
	if err := db.Create(&row).Error; err != nil {
		return adb.Agent{}, err
	}
	return toAssistantAgent(row), nil
}

func addAssistantAgentWorkspace(db *gorm.DB, arg adb.AddAssistantAgentWorkspaceParams) error {
	row := model.AgentWorkspace{
		AgentID:     model.UUID(UUIDString(arg.AgentID)),
		WorkspaceID: model.UUID(UUIDString(arg.WorkspaceID)),
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// ── conversations ─────────────────────────────────────────────────────────────

// CreateConversation starts an idle, empty conversation.
func (r *Repo) CreateConversation(ctx context.Context, arg adb.CreateConversationParams) (adb.AssistantConversation, error) {
	now := time.Now().UTC()
	row := model.AssistantConversation{
		ID:          model.UUID(NewUUID()),
		UserID:      model.UUID(UUIDString(arg.UserID)),
		WorkspaceID: model.UUID(UUIDString(arg.WorkspaceID)),
		AgentID:     model.UUID(UUIDString(arg.AgentID)),
		Title:       model.Text(arg.Title),
		State:       "idle",
		LastSeq:     0,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return adb.AssistantConversation{}, err
	}
	return toAssistantConversation(row), nil
}

// GetConversation returns one of the caller's conversations or db.ErrNoRows.
func (r *Repo) GetConversation(ctx context.Context, arg adb.GetConversationParams) (adb.AssistantConversation, error) {
	if !arg.ID.Valid || !arg.UserID.Valid {
		return adb.AssistantConversation{}, db.ErrNoRows
	}
	var row model.AssistantConversation
	err := r.Users.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", UUIDString(arg.ID), UUIDString(arg.UserID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return adb.AssistantConversation{}, db.ErrNoRows
	}
	if err != nil {
		return adb.AssistantConversation{}, err
	}
	return toAssistantConversation(row), nil
}

// ListConversations returns the caller's conversations in a workspace, newest
// first.
func (r *Repo) ListConversations(ctx context.Context, arg adb.ListConversationsParams) ([]adb.AssistantConversation, error) {
	var rows []model.AssistantConversation
	if err := r.Users.db.WithContext(ctx).
		Where("user_id = ? AND workspace_id = ?", UUIDString(arg.UserID), UUIDString(arg.WorkspaceID)).
		Order("updated_at DESC, id DESC").
		Limit(int(arg.Limit)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]adb.AssistantConversation, len(rows))
	for i, row := range rows {
		out[i] = toAssistantConversation(row)
	}
	return out, nil
}

// DeleteConversation hard-deletes the caller's conversation; its messages go
// with it through the foreign key.
func (r *Repo) DeleteConversation(ctx context.Context, arg adb.DeleteConversationParams) error {
	return r.Users.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", UUIDString(arg.ID), UUIDString(arg.UserID)).
		Delete(&model.AssistantConversation{}).Error
}

// ── turn lifecycle ────────────────────────────────────────────────────────────

// ClaimConversation starts a turn or returns db.ErrNoRows when one is already
// running. The row lock makes the check and the transition one step.
func (r *Repo) ClaimConversation(ctx context.Context, arg adb.ClaimConversationParams) (adb.AssistantConversation, error) {
	var out adb.AssistantConversation
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.AssistantConversation
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", UUIDString(arg.ID), UUIDString(arg.UserID)).
			First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return db.ErrNoRows
		}
		if err != nil {
			return err
		}
		if row.State == "running" {
			return db.ErrNoRows
		}
		now := time.Now().UTC()
		if err := tx.Model(&model.AssistantConversation{}).
			Where("id = ?", string(row.ID)).
			Updates(map[string]any{
				"state":             "running",
				"turn_owner":        arg.TurnOwner,
				"turn_heartbeat_at": now,
				"updated_at":        now,
			}).Error; err != nil {
			return err
		}
		row.State = "running"
		row.TurnOwner = arg.TurnOwner
		row.TurnHeartbeatAt = &now
		row.UpdatedAt = now
		out = toAssistantConversation(row)
		return nil
	})
	if err != nil {
		return adb.AssistantConversation{}, err
	}
	return out, nil
}

// ReleaseConversation returns a conversation to idle after a turn. The turn
// token must still be the owner, so a runner that lost the conversation to a
// reclaim cannot release the new turn.
func (r *Repo) ReleaseConversation(ctx context.Context, id db.UUID, turnOwner string) error {
	if !id.Valid {
		return nil
	}
	return r.Users.db.WithContext(ctx).Model(&model.AssistantConversation{}).
		Where("id = ? AND turn_owner = ?", UUIDString(id), turnOwner).
		Updates(map[string]any{
			"state":             "idle",
			"turn_owner":        nil,
			"turn_heartbeat_at": nil,
			"updated_at":        time.Now().UTC(),
		}).Error
}

// SetConversationInterrupted marks a conversation interrupted (cancel, crash
// recovery).
func (r *Repo) SetConversationInterrupted(ctx context.Context, id db.UUID) error {
	if !id.Valid {
		return nil
	}
	return r.Users.db.WithContext(ctx).Model(&model.AssistantConversation{}).
		Where("id = ?", UUIDString(id)).
		Updates(map[string]any{
			"state":             "interrupted",
			"turn_owner":        nil,
			"turn_heartbeat_at": nil,
			"updated_at":        time.Now().UTC(),
		}).Error
}

// HeartbeatConversation refreshes the turn lease while the turn is running. It
// deliberately does not touch updated_at: listing order follows the last real
// activity, not the beat.
func (r *Repo) HeartbeatConversation(ctx context.Context, id db.UUID) error {
	if !id.Valid {
		return nil
	}
	return r.Users.db.WithContext(ctx).Model(&model.AssistantConversation{}).
		Where("id = ? AND state = ?", UUIDString(id), "running").
		UpdateColumn("turn_heartbeat_at", time.Now().UTC()).Error
}

// InterruptStaleConversations marks turns whose replica stopped heartbeating as
// interrupted and returns their ids.
func (r *Repo) InterruptStaleConversations(ctx context.Context) ([]db.UUID, error) {
	var ids []db.UUID
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stale []model.AssistantConversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "state", "turn_heartbeat_at").
			Where("state = ? AND turn_heartbeat_at < ?", "running", time.Now().UTC().Add(-staleTurnAge)).
			Find(&stale).Error; err != nil {
			return err
		}
		if len(stale) == 0 {
			return nil
		}
		keys := make([]string, len(stale))
		for i, row := range stale {
			keys[i] = string(row.ID)
		}
		if err := tx.Model(&model.AssistantConversation{}).
			Where("id IN ?", keys).
			Updates(map[string]any{
				"state":             "interrupted",
				"turn_owner":        nil,
				"turn_heartbeat_at": nil,
				"updated_at":        time.Now().UTC(),
			}).Error; err != nil {
			return err
		}
		ids = make([]db.UUID, len(stale))
		for i, row := range stale {
			ids[i] = PGUUID(string(row.ID))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// SetConversationTitle names a conversation once, from its first user message.
func (r *Repo) SetConversationTitle(ctx context.Context, arg adb.SetConversationTitleParams) error {
	if !arg.ID.Valid {
		return nil
	}
	return r.Users.db.WithContext(ctx).Model(&model.AssistantConversation{}).
		Where("id = ? AND title = ?", UUIDString(arg.ID), "").
		Updates(map[string]any{
			"title":      arg.Title,
			"updated_at": time.Now().UTC(),
		}).Error
}

// ── messages ──────────────────────────────────────────────────────────────────

// AppendMessage bumps last_seq and inserts the message in one transaction, so
// SSE sequence numbers have no gaps.
func (r *Repo) AppendMessage(ctx context.Context, arg adb.AppendMessageParams) (adb.AssistantMessage, error) {
	var out adb.AssistantMessage
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv model.AssistantConversation
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "last_seq").
			Where("id = ?", UUIDString(arg.ConversationID)).
			First(&conv).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return db.ErrNoRows
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		seq := conv.LastSeq + 1
		if err := tx.Model(&model.AssistantConversation{}).
			Where("id = ?", string(conv.ID)).
			Updates(map[string]any{
				"last_seq":   seq,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
		msg := model.AssistantMessage{
			ConversationID: conv.ID,
			Seq:            seq,
			Role:           arg.Role,
			Content:        model.JSON(arg.Content),
			InputTokens:    arg.InputTokens,
			OutputTokens:   arg.OutputTokens,
			CreatedAt:      now,
		}
		if err := tx.Create(&msg).Error; err != nil {
			return err
		}
		out = toAssistantMessage(msg)
		return nil
	})
	if err != nil {
		return adb.AssistantMessage{}, err
	}
	return out, nil
}

// ListMessages returns messages after a sequence number, oldest first.
func (r *Repo) ListMessages(ctx context.Context, arg adb.ListMessagesParams) ([]adb.AssistantMessage, error) {
	var rows []model.AssistantMessage
	if err := r.Users.db.WithContext(ctx).
		Where("conversation_id = ? AND seq > ?", UUIDString(arg.ConversationID), arg.Seq).
		Order("seq").
		Limit(int(arg.Limit)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]adb.AssistantMessage, len(rows))
	for i, row := range rows {
		out[i] = toAssistantMessage(row)
	}
	return out, nil
}

// LatestConversationMessage returns the conversation's newest message or
// db.ErrNoRows.
func (r *Repo) LatestConversationMessage(ctx context.Context, conversationID db.UUID) (adb.AssistantMessage, error) {
	if !conversationID.Valid {
		return adb.AssistantMessage{}, db.ErrNoRows
	}
	var row model.AssistantMessage
	err := r.Users.db.WithContext(ctx).
		Where("conversation_id = ?", UUIDString(conversationID)).
		Order("seq DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return adb.AssistantMessage{}, db.ErrNoRows
	}
	if err != nil {
		return adb.AssistantMessage{}, err
	}
	return toAssistantMessage(row), nil
}

// CountUserTokensSince returns the user's token usage over their conversations
// since the given time. A subquery scopes the messages to the user, and the
// token columns are summed in Go so the store keeps to GORM's model API
// (ADR 0020); a message with only one side counted is ignored, as a NULL
// summand was in the SQL aggregate.
func (r *Repo) CountUserTokensSince(ctx context.Context, arg adb.CountUserTokensSinceParams) (int64, error) {
	if !arg.UserID.Valid || !arg.CreatedAt.Valid {
		return 0, nil
	}
	conversations := r.Users.db.Model(&model.AssistantConversation{}).
		Select("id").
		Where("user_id = ?", UUIDString(arg.UserID))
	var messages []model.AssistantMessage
	if err := r.Users.db.WithContext(ctx).
		Select("input_tokens", "output_tokens").
		Where("conversation_id IN (?) AND created_at >= ?", conversations, arg.CreatedAt.Time).
		Find(&messages).Error; err != nil {
		return 0, err
	}
	var used int64
	for _, msg := range messages {
		if msg.InputTokens == nil || msg.OutputTokens == nil {
			continue
		}
		used += int64(*msg.InputTokens) + int64(*msg.OutputTokens)
	}
	return used, nil
}

// ── conversions ───────────────────────────────────────────────────────────────

func toAssistantSetting(s model.AssistantSetting) adb.AssistantSetting {
	return adb.AssistantSetting{
		ID:                      s.ID,
		Enabled:                 s.Enabled,
		Provider:                s.Provider,
		BaseUrl:                 s.BaseURL,
		Model:                   s.Model,
		ApiKeyEnc:               s.APIKeyEnc,
		DataSharing:             string(s.DataSharing),
		MaxTurnSeconds:          s.MaxTurnSeconds,
		MaxToolCallsPerTurn:     s.MaxToolCallsPerTurn,
		MaxOutputTokensPerTurn:  s.MaxOutputTokensPerTurn,
		DailyTokenBudgetPerUser: s.DailyTokenBudgetPerUser,
		UpdatedBy:               uuidPtr(s.UpdatedBy),
		UpdatedAt:               PGTime(s.UpdatedAt),
	}
}

func toAssistantAgent(a model.Agent) adb.Agent {
	return adb.Agent{
		ID:              PGUUID(string(a.ID)),
		OwnerID:         PGUUID(string(a.OwnerID)),
		Kind:            string(a.Kind),
		Name:            a.Name,
		Description:     string(a.Description),
		Status:          string(a.Status),
		CreatedAt:       PGTime(a.CreatedAt),
		UpdatedAt:       PGTime(a.UpdatedAt),
		Policy:          []byte(a.Policy),
		PolicyUpdatedAt: PGTime(a.PolicyUpdatedAt),
		PolicyUpdatedBy: uuidPtr(a.PolicyUpdatedBy),
	}
}

func toAssistantConversation(c model.AssistantConversation) adb.AssistantConversation {
	return adb.AssistantConversation{
		ID:              PGUUID(string(c.ID)),
		UserID:          PGUUID(string(c.UserID)),
		WorkspaceID:     PGUUID(string(c.WorkspaceID)),
		AgentID:         PGUUID(string(c.AgentID)),
		Title:           string(c.Title),
		State:           string(c.State),
		TurnOwner:       c.TurnOwner,
		TurnHeartbeatAt: PGTimePtr(c.TurnHeartbeatAt),
		LastSeq:         c.LastSeq,
		CreatedAt:       PGTime(c.CreatedAt),
		UpdatedAt:       PGTime(c.UpdatedAt),
	}
}

func toAssistantMessage(m model.AssistantMessage) adb.AssistantMessage {
	return adb.AssistantMessage{
		ConversationID: PGUUID(string(m.ConversationID)),
		Seq:            m.Seq,
		Role:           m.Role,
		Content:        []byte(m.Content),
		InputTokens:    m.InputTokens,
		OutputTokens:   m.OutputTokens,
		CreatedAt:      PGTime(m.CreatedAt),
	}
}
