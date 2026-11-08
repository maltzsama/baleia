package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
)

// Teams persists teams and their members.
type Teams struct{ db *gorm.DB }

// NewTeams builds the team store.
func NewTeams(db *gorm.DB) *Teams { return &Teams{db: db} }

// ListTeams returns every team, by name.
func (s *Teams) ListTeams(ctx context.Context) ([]db.Team, error) {
	var rows []model.Team
	if err := s.db.WithContext(ctx).Order("name").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.Team, len(rows))
	for i, row := range rows {
		out[i] = toDBTeam(row)
	}
	return out, nil
}

// ListTeamsByUser returns the teams the user belongs to, by name.
func (s *Teams) ListTeamsByUser(ctx context.Context, userID pgtype.UUID) ([]db.Team, error) {
	var rows []model.Team
	if err := s.db.WithContext(ctx).
		Joins("JOIN team_members tm ON tm.team_id = teams.id").
		Where("tm.user_id = ?", UUIDString(userID)).
		Order("teams.name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.Team, len(rows))
	for i, row := range rows {
		out[i] = toDBTeam(row)
	}
	return out, nil
}

// GetTeamByID returns one team or pgx.ErrNoRows.
func (s *Teams) GetTeamByID(ctx context.Context, id pgtype.UUID) (db.Team, error) {
	if !id.Valid {
		return db.Team{}, pgx.ErrNoRows
	}
	var row model.Team
	err := s.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Team{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.Team{}, err
	}
	return toDBTeam(row), nil
}

// ListTeamMembers returns the members with their user details, by name.
func (s *Teams) ListTeamMembers(ctx context.Context, teamID pgtype.UUID) ([]db.ListTeamMembersRow, error) {
	var rows []struct {
		ID               model.UUID
		TeamID           model.UUID
		UserID           model.UUID
		Role             model.Text
		JoinedAt         time.Time
		UserName         string
		UserEmail        string
		UserRole         string
		UserAuthProvider string
		UserStatus       string
		UserCreatedAt    time.Time
	}
	if err := s.db.WithContext(ctx).
		Model(&model.TeamMember{}).
		Select(`team_members.id, team_members.team_id, team_members.user_id, team_members.role, team_members.joined_at,
			u.name AS user_name, u.email AS user_email, u.role AS user_role,
			u.auth_provider AS user_auth_provider, u.status AS user_status, u.created_at AS user_created_at`).
		Joins("JOIN users u ON u.id = team_members.user_id").
		Where("team_members.team_id = ?", UUIDString(teamID)).
		Order("u.name").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListTeamMembersRow, len(rows))
	for i, row := range rows {
		out[i] = db.ListTeamMembersRow{
			ID:               PGUUID(string(row.ID)),
			TeamID:           PGUUID(string(row.TeamID)),
			UserID:           PGUUID(string(row.UserID)),
			Role:             string(row.Role),
			JoinedAt:         PGTime(row.JoinedAt),
			UserName:         row.UserName,
			UserEmail:        row.UserEmail,
			UserRole:         row.UserRole,
			UserAuthProvider: row.UserAuthProvider,
			UserStatus:       row.UserStatus,
			UserCreatedAt:    PGTime(row.UserCreatedAt),
		}
	}
	return out, nil
}

// CreateTeam inserts a team.
func (s *Teams) CreateTeam(ctx context.Context, arg db.CreateTeamParams) (db.Team, error) {
	now := time.Now().UTC()
	row := model.Team{
		ID:          model.UUID(NewUUID()),
		Name:        arg.Name,
		Description: arg.Description,
		CreatedBy:   model.UUID(UUIDString(arg.CreatedBy)),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.Team{}, err
	}
	return toDBTeam(row), nil
}

// DeleteTeam removes the team.
func (s *Teams) DeleteTeam(ctx context.Context, id pgtype.UUID) error {
	return s.db.WithContext(ctx).Where("id = ?", UUIDString(id)).Delete(&model.Team{}).Error
}

// AddTeamMember adds a member and returns the row.
func (s *Teams) AddTeamMember(ctx context.Context, arg db.AddTeamMemberParams) (db.TeamMember, error) {
	row := model.TeamMember{
		ID:       model.UUID(NewUUID()),
		TeamID:   model.UUID(UUIDString(arg.TeamID)),
		UserID:   model.UUID(UUIDString(arg.UserID)),
		Role:     model.Text(arg.Role),
		JoinedAt: time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.TeamMember{}, err
	}
	return toDBTeamMember(row), nil
}

// UpdateTeamMemberRole changes the member's role and returns the row.
func (s *Teams) UpdateTeamMemberRole(ctx context.Context, arg db.UpdateTeamMemberRoleParams) (db.TeamMember, error) {
	var row model.TeamMember
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&model.TeamMember{}).
			Where("team_id = ? AND user_id = ?", UUIDString(arg.TeamID), UUIDString(arg.UserID)).
			Update("role", arg.Role).Error; err != nil {
			return err
		}
		res := tx.WithContext(ctx).
			Where("team_id = ? AND user_id = ?", UUIDString(arg.TeamID), UUIDString(arg.UserID)).
			First(&row)
		if errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return pgx.ErrNoRows
		}
		return res.Error
	})
	if err != nil {
		return db.TeamMember{}, err
	}
	return toDBTeamMember(row), nil
}

// RemoveTeamMember removes the member and returns the affected rows.
func (s *Teams) RemoveTeamMember(ctx context.Context, arg db.RemoveTeamMemberParams) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("team_id = ? AND user_id = ?", UUIDString(arg.TeamID), UUIDString(arg.UserID)).
		Delete(&model.TeamMember{})
	return res.RowsAffected, res.Error
}

func toDBTeam(t model.Team) db.Team {
	return db.Team{
		ID:          PGUUID(string(t.ID)),
		Name:        t.Name,
		Description: t.Description,
		CreatedBy:   PGUUID(string(t.CreatedBy)),
		CreatedAt:   PGTime(t.CreatedAt),
		UpdatedAt:   PGTime(t.UpdatedAt),
	}
}

func toDBTeamMember(m model.TeamMember) db.TeamMember {
	return db.TeamMember{
		ID:       PGUUID(string(m.ID)),
		TeamID:   PGUUID(string(m.TeamID)),
		UserID:   PGUUID(string(m.UserID)),
		Role:     string(m.Role),
		JoinedAt: PGTime(m.JoinedAt),
	}
}
