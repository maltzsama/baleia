package repo

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InsertAuditEvent appends one audit event (ADR 0020, #774). The table is
// append-only; the recorder never updates or deletes a row.
func (r *Repo) InsertAuditEvent(ctx context.Context, arg db.InsertAuditEventParams) error {
	row := model.AuditEvent{
		ActorKind:    arg.ActorKind,
		ActorID:      modelTextPtr(arg.ActorID),
		AuthMethod:   arg.AuthMethod,
		Action:       arg.Action,
		ResourceType: model.Text(arg.ResourceType),
		ResourceID:   modelTextPtr(arg.ResourceID),
		Outcome:      arg.Outcome,
		Reason:       arg.Reason,
		RequestID:    modelTextPtr(arg.RequestID),
		TraceID:      modelTextPtr(arg.TraceID),
		UserAgent:    arg.UserAgent,
	}
	if arg.WorkspaceID.Valid {
		id := model.UUID(UUIDString(arg.WorkspaceID))
		row.WorkspaceID = &id
	}
	if arg.OnBehalfOf.Valid {
		id := model.UUID(UUIDString(arg.OnBehalfOf))
		row.OnBehalfOf = &id
	}
	if arg.ClientIp != nil {
		ip := model.Inet(arg.ClientIp.String())
		row.ClientIP = &ip
	}
	if len(arg.Changes) > 0 {
		changes := model.JSON(arg.Changes)
		row.Changes = &changes
	}
	return r.Users.db.WithContext(ctx).Create(&row).Error
}

// ListAuditEvents returns one page of the trail, newest first, keyset
// paginated on (occurred_at, id) (#43, #49). A non-admin is scoped to their
// own events, the events of the workspaces they own and the events they
// caused on behalf of an agent.
func (r *Repo) ListAuditEvents(ctx context.Context, arg db.ListAuditEventsParams) ([]db.ListAuditEventsRow, error) {
	q := r.Users.db.WithContext(ctx).Model(&model.AuditEvent{})
	if arg.ResourceType != nil {
		q = q.Where("resource_type = ?", *arg.ResourceType)
	}
	if arg.ResourceID != nil {
		q = q.Where("resource_id = ?", *arg.ResourceID)
	}
	if arg.ActorID != nil {
		q = q.Where("actor_id = ?", *arg.ActorID)
	}
	if arg.Action != nil {
		q = q.Where("action = ?", *arg.Action)
	}
	if arg.Outcome != nil {
		q = q.Where("outcome = ?", *arg.Outcome)
	}
	if arg.RequestID != nil {
		q = q.Where("request_id = ?", *arg.RequestID)
	}
	if arg.TraceID != nil {
		q = q.Where("trace_id = ?", *arg.TraceID)
	}
	if arg.ScopeActor != nil || arg.ScopeOwner.Valid {
		scope, args, err := r.auditScope(ctx, arg.ScopeActor, arg.ScopeOwner)
		if err != nil {
			return nil, err
		}
		if scope != "" {
			q = q.Where(scope, args...)
		}
	}
	if arg.FromTime.Valid {
		q = q.Where("occurred_at >= ?", arg.FromTime.Time)
	}
	if arg.ToTime.Valid {
		q = q.Where("occurred_at < ?", arg.ToTime.Time)
	}
	if arg.CursorAt.Valid {
		if arg.CursorID != nil {
			q = q.Where("(occurred_at < ? OR (occurred_at = ? AND id < ?))",
				arg.CursorAt.Time, arg.CursorAt.Time, *arg.CursorID)
		} else {
			q = q.Where("occurred_at < ?", arg.CursorAt.Time)
		}
	}
	var rows []model.AuditEvent
	if err := q.Order("occurred_at DESC, id DESC").Limit(int(arg.Lim)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ListAuditEventsRow, len(rows))
	for i, row := range rows {
		out[i] = toDBAuditEvent(row)
	}
	return out, nil
}

// auditScope builds the non-admin predicate: the caller's own events, the
// events of the workspaces they own and the events of agents they own.
func (r *Repo) auditScope(ctx context.Context, actor *string, owner pgtype.UUID) (string, []any, error) {
	var clauses []string
	var args []any
	if actor != nil {
		clauses = append(clauses, "actor_id = ?")
		args = append(args, *actor)
	}
	if owner.Valid {
		ids, err := r.auditOwnedWorkspaceIDs(ctx, owner)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, "on_behalf_of = ?")
		args = append(args, UUIDString(owner))
		if len(ids) > 0 {
			clauses = append(clauses, "workspace_id IN ?")
			args = append(args, ids)
		}
	}
	if len(clauses) == 0 {
		return "", nil, nil
	}
	return "(" + strings.Join(clauses, " OR ") + ")", args, nil
}

// auditOwnedWorkspaceIDs lists the workspaces the user owns: their personal
// ones and the ones owned by a team where they are an owner (#49).
func (r *Repo) auditOwnedWorkspaceIDs(ctx context.Context, owner pgtype.UUID) ([]string, error) {
	userID := UUIDString(owner)
	var teamIDs []string
	if err := r.Users.db.WithContext(ctx).Model(&model.TeamMember{}).
		Where("user_id = ? AND role = ?", userID, "owner").
		Pluck("team_id", &teamIDs).Error; err != nil {
		return nil, err
	}
	q := r.Users.db.WithContext(ctx).Model(&model.Workspace{}).Select("id")
	if len(teamIDs) > 0 {
		q = q.Where("owner_user_id = ? OR owner_team_id IN ?", userID, teamIDs)
	} else {
		q = q.Where("owner_user_id = ?", userID)
	}
	var ids []string
	if err := q.Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// SharesWorkspace reports whether two users are both members of one
// workspace (#432). The effective role is resolved in Go, like WorkspaceRole,
// so the result does not depend on a PL/pgSQL helper.
func (r *Repo) SharesWorkspace(ctx context.Context, arg db.SharesWorkspaceParams) (bool, error) {
	if !arg.CallerID.Valid || !arg.UserID.Valid {
		return false, nil
	}
	caller, user := UUIDString(arg.CallerID), UUIDString(arg.UserID)
	shares := false
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workspaces []model.Workspace
		if err := tx.Order("id").Find(&workspaces).Error; err != nil {
			return err
		}
		for _, ws := range workspaces {
			callerRole, err := workspaceRoleFor(tx, ws, caller)
			if err != nil {
				return err
			}
			if callerRole == "" {
				continue
			}
			userRole, err := workspaceRoleFor(tx, ws, user)
			if err != nil {
				return err
			}
			if userRole != "" {
				shares = true
				return nil
			}
		}
		return nil
	})
	return shares, err
}

// ListAuditEventsForExport returns the batch after the cursor, before the
// safety-lag cutoff, oldest first (#50).
func (r *Repo) ListAuditEventsForExport(ctx context.Context, arg db.ListAuditEventsForExportParams) ([]db.ListAuditEventsForExportRow, error) {
	var rows []model.AuditEvent
	err := r.Users.db.WithContext(ctx).
		Where("(occurred_at > ? OR (occurred_at = ? AND id > ?)) AND occurred_at < ?",
			arg.AfterAt.Time, arg.AfterAt.Time, arg.AfterID, arg.Before.Time).
		Order("occurred_at ASC, id ASC").
		Limit(int(arg.Lim)).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]db.ListAuditEventsForExportRow, len(rows))
	for i, row := range rows {
		workspaceID := ""
		if row.WorkspaceID != nil {
			workspaceID = string(*row.WorkspaceID)
		}
		out[i] = db.ListAuditEventsForExportRow{
			OccurredAt:   PGTime(row.OccurredAt),
			ID:           row.ID,
			ActorKind:    row.ActorKind,
			ActorID:      derefText(row.ActorID),
			AuthMethod:   derefString(row.AuthMethod),
			Action:       row.Action,
			ResourceType: string(row.ResourceType),
			ResourceID:   derefText(row.ResourceID),
			WorkspaceID:  workspaceID,
			Outcome:      row.Outcome,
			Reason:       derefString(row.Reason),
			RequestID:    derefText(row.RequestID),
			TraceID:      derefText(row.TraceID),
			ClientIp:     derefInet(row.ClientIP),
			UserAgent:    derefString(row.UserAgent),
			Changes:      derefJSON(row.Changes),
		}
	}
	return out, nil
}

// GetAuditExportCursor returns the sink's position or pgx.ErrNoRows.
func (r *Repo) GetAuditExportCursor(ctx context.Context, sink string) (db.GetAuditExportCursorRow, error) {
	var row model.AuditExportCursor
	err := r.Users.db.WithContext(ctx).Where("sink = ?", sink).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetAuditExportCursorRow{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.GetAuditExportCursorRow{}, err
	}
	return db.GetAuditExportCursorRow{OccurredAt: PGTime(row.OccurredAt), ID: row.ID}, nil
}

// SetAuditExportCursor advances the sink's cursor (upsert).
func (r *Repo) SetAuditExportCursor(ctx context.Context, arg db.SetAuditExportCursorParams) error {
	row := model.AuditExportCursor{
		Sink:       model.Text(arg.Sink),
		OccurredAt: arg.OccurredAt.Time,
		ID:         arg.ID,
		UpdatedAt:  time.Now().UTC(),
	}
	return r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "sink"}},
		DoUpdates: clause.AssignmentColumns([]string{"occurred_at", "id", "updated_at"}),
	}).Create(&row).Error
}

func toDBAuditEvent(row model.AuditEvent) db.ListAuditEventsRow {
	return db.ListAuditEventsRow{
		ID:           row.ID,
		OccurredAt:   PGTime(row.OccurredAt),
		ActorKind:    row.ActorKind,
		ActorID:      textPtr(row.ActorID),
		AuthMethod:   row.AuthMethod,
		Action:       row.Action,
		ResourceType: string(row.ResourceType),
		ResourceID:   textPtr(row.ResourceID),
		WorkspaceID:  uuidPtr(row.WorkspaceID),
		Outcome:      row.Outcome,
		Reason:       row.Reason,
		RequestID:    textPtr(row.RequestID),
		TraceID:      textPtr(row.TraceID),
		ClientIp:     inetPtr(row.ClientIP),
		UserAgent:    row.UserAgent,
		Changes:      derefJSON(row.Changes),
	}
}

func inetPtr(v *model.Inet) *netip.Addr {
	if v == nil {
		return nil
	}
	addr, err := netip.ParseAddr(string(*v))
	if err != nil {
		return nil
	}
	return &addr
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func derefText(v *model.Text) string {
	if v == nil {
		return ""
	}
	return string(*v)
}

func derefInet(v *model.Inet) string {
	if v == nil {
		return ""
	}
	return string(*v)
}

func derefJSON(v *model.JSON) []byte {
	if v == nil {
		return nil
	}
	return []byte(*v)
}
