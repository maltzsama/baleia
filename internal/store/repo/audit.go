package repo

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
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
func (r *Repo) auditScope(ctx context.Context, actor *string, owner db.UUID) (string, []any, error) {
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
func (r *Repo) auditOwnedWorkspaceIDs(ctx context.Context, owner db.UUID) ([]string, error) {
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
// workspace (#432). The effective role is resolved from set-based reads of
// the workspaces each user belongs to, so no per-workspace query is issued.
func (r *Repo) SharesWorkspace(ctx context.Context, arg db.SharesWorkspaceParams) (bool, error) {
	if !arg.CallerID.Valid || !arg.UserID.Valid {
		return false, nil
	}
	caller, user := UUIDString(arg.CallerID), UUIDString(arg.UserID)
	sets := map[string]map[string]bool{}
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		sets, err = memberWorkspaceSets(tx, []string{caller, user})
		return err
	})
	if err != nil {
		return false, err
	}
	for workspaceID := range sets[caller] {
		if sets[user][workspaceID] {
			return true, nil
		}
	}
	return false, nil
}

// memberWorkspaceSets resolves, in a fixed number of set-based queries, the
// workspaces where each user has an effective role. It mirrors
// workspaceRoleFor: a personal owner, an explicit grant, a shared workspace's
// owning team (except for service accounts, which only hold explicit grants).
func memberWorkspaceSets(tx *gorm.DB, userIDs []string) (map[string]map[string]bool, error) {
	sets := make(map[string]map[string]bool, len(userIDs))
	for _, id := range userIDs {
		sets[id] = map[string]bool{}
	}
	var users []model.User
	if err := tx.Select("id", "kind").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	kinds := make(map[string]string, len(users))
	for _, user := range users {
		kinds[string(user.ID)] = string(user.Kind)
	}

	var owned []model.Workspace
	if err := tx.Select("id", "owner_user_id").
		Where("type = ? AND owner_user_id IN ?", "personal", userIDs).
		Find(&owned).Error; err != nil {
		return nil, err
	}
	for _, ws := range owned {
		if ws.OwnerUserID != nil {
			sets[string(*ws.OwnerUserID)][string(ws.ID)] = true
		}
	}

	var explicit []model.WorkspaceMember
	if err := tx.Where("user_id IN ?", userIDs).Find(&explicit).Error; err != nil {
		return nil, err
	}
	explicitIDs := map[string]bool{}
	for _, member := range explicit {
		explicitIDs[string(member.WorkspaceID)] = true
	}
	explicitWS := make(map[string]model.Workspace, len(explicitIDs))
	if len(explicitIDs) > 0 {
		ids := make([]string, 0, len(explicitIDs))
		for id := range explicitIDs {
			ids = append(ids, id)
		}
		var workspaces []model.Workspace
		if err := tx.Select("id", "type", "owner_team_id").Where("id IN ?", ids).Find(&workspaces).Error; err != nil {
			return nil, err
		}
		for _, ws := range workspaces {
			explicitWS[string(ws.ID)] = ws
		}
	}

	var teams []model.TeamMember
	if err := tx.Where("user_id IN ?", userIDs).Find(&teams).Error; err != nil {
		return nil, err
	}
	teamIDs := map[string]bool{}
	teamRoles := map[string]map[string]string{}
	for _, member := range teams {
		teamID, userID := string(member.TeamID), string(member.UserID)
		teamIDs[teamID] = true
		if teamRoles[userID] == nil {
			teamRoles[userID] = map[string]string{}
		}
		teamRoles[userID][teamID] = string(member.Role)
	}
	sharedByTeam := map[string][]string{}
	if len(teamIDs) > 0 {
		ids := make([]string, 0, len(teamIDs))
		for id := range teamIDs {
			ids = append(ids, id)
		}
		var shared []model.Workspace
		if err := tx.Select("id", "owner_team_id").
			Where("type = ? AND owner_team_id IN ?", "shared", ids).
			Find(&shared).Error; err != nil {
			return nil, err
		}
		for _, ws := range shared {
			if ws.OwnerTeamID != nil {
				sharedByTeam[string(*ws.OwnerTeamID)] = append(sharedByTeam[string(*ws.OwnerTeamID)], string(ws.ID))
			}
		}
	}

	// A team's shared workspaces belong to its humans, not to a service
	// account that happened to be added to the team.
	for userID, roles := range teamRoles {
		if kinds[userID] == "service_account" {
			continue
		}
		for teamID := range roles {
			for _, workspaceID := range sharedByTeam[teamID] {
				sets[userID][workspaceID] = true
			}
		}
	}

	for _, member := range explicit {
		userID := string(member.UserID)
		ws, ok := explicitWS[string(member.WorkspaceID)]
		if !ok {
			continue
		}
		switch ws.Type {
		case "personal":
			sets[userID][string(ws.ID)] = true
		case "shared":
			if kinds[userID] == "service_account" {
				sets[userID][string(ws.ID)] = true
				continue
			}
			if ws.OwnerTeamID != nil && teamRoles[userID][string(*ws.OwnerTeamID)] != "" {
				sets[userID][string(ws.ID)] = true
			}
		}
	}
	return sets, nil
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

// GetAuditExportCursor returns the stream and sink's position or db.ErrNoRows.
func (r *Repo) GetAuditExportCursor(ctx context.Context, stream, sink string) (db.GetAuditExportCursorRow, error) {
	var row model.AuditExportCursor
	err := r.Users.db.WithContext(ctx).
		Where("stream = ? AND sink = ?", stream, sink).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetAuditExportCursorRow{}, db.ErrNoRows
	}
	if err != nil {
		return db.GetAuditExportCursorRow{}, err
	}
	return db.GetAuditExportCursorRow{Stream: string(row.Stream), OccurredAt: PGTime(row.OccurredAt), ID: row.ID, CursorKey: string(row.CursorKey)}, nil
}

// SetAuditExportCursor advances the stream and sink's cursor (upsert).
func (r *Repo) SetAuditExportCursor(ctx context.Context, arg db.SetAuditExportCursorParams) error {
	row := model.AuditExportCursor{
		Stream:     model.Text(arg.Stream),
		Sink:       model.Text(arg.Sink),
		OccurredAt: arg.OccurredAt.Time,
		ID:         arg.ID,
		CursorKey:  model.Text(arg.CursorKey),
		UpdatedAt:  time.Now().UTC(),
	}
	return r.Users.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "stream"}, {Name: "sink"}},
		DoUpdates: clause.AssignmentColumns([]string{"occurred_at", "id", "cursor_key", "updated_at"}),
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
	raw := string(*v)
	if addr, err := netip.ParseAddr(raw); err == nil {
		return &addr
	}
	// PostgreSQL's inet may carry a prefix (for example "10.0.0.0/8").
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		addr := prefix.Addr()
		return &addr
	}
	return nil
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
