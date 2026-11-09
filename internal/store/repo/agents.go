package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// Agents reads agents and their tokens for authentication and management.
type Agents struct{ db *gorm.DB }

// NewAgents builds the agent store.
func NewAgents(db *gorm.DB) *Agents { return &Agents{db: db} }

// GetAgentTokenByHash returns the token row or db.ErrNoRows.
func (s *Agents) GetAgentTokenByHash(ctx context.Context, tokenHash string) (db.AgentToken, error) {
	var row model.AgentToken
	err := s.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.AgentToken{}, db.ErrNoRows
	}
	if err != nil {
		return db.AgentToken{}, err
	}
	return toDBAgentToken(row), nil
}

// GetAgentByID returns one agent or db.ErrNoRows.
func (s *Agents) GetAgentByID(ctx context.Context, id db.UUID) (db.Agent, error) {
	if !id.Valid {
		return db.Agent{}, db.ErrNoRows
	}
	var row model.Agent
	err := s.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Agent{}, db.ErrNoRows
	}
	if err != nil {
		return db.Agent{}, err
	}
	return toDBAgent(row), nil
}

// TouchAgentToken records that the token was just used.
func (s *Agents) TouchAgentToken(ctx context.Context, id db.UUID) error {
	return s.db.WithContext(ctx).Model(&model.AgentToken{}).
		Where("id = ?", UUIDString(id)).
		Update("last_used_at", time.Now().UTC()).Error
}

func toDBAgentToken(t model.AgentToken) db.AgentToken {
	return db.AgentToken{
		ID:         PGUUID(string(t.ID)),
		AgentID:    PGUUID(string(t.AgentID)),
		Name:       t.Name,
		TokenHash:  string(t.TokenHash),
		Prefix:     t.Prefix,
		ExpiresAt:  PGTime(t.ExpiresAt),
		LastUsedAt: PGTimePtr(t.LastUsedAt),
		CreatedBy:  PGUUID(string(t.CreatedBy)),
		CreatedAt:  PGTime(t.CreatedAt),
		RevokedAt:  PGTimePtr(t.RevokedAt),
	}
}

func toDBAgent(a model.Agent) db.Agent {
	return db.Agent{
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
