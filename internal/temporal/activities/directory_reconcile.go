package activities

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"strconv"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/jackc/pgx/v5/pgtype"
)

// Directory reconciliation (#257). A pass pages every enabled LDAP provider,
// compares the directory with the linked users, and applies suspensions,
// reinstatements and role/team mappings — unless the safety brake fires.

const (
	reconcileOutcomeSuccess = "success"
	reconcileOutcomeError   = "error"
	reconcileOutcomeAborted = "aborted_safety"

	directorySuspensionReason = "directory"

	// safetyBrakeFloor is the smallest number of suspensions that can trigger
	// the brake; the ratio catches a mass change anyway.
	safetyBrakeFloor = 5
	safetyBrakeRatio = 0.10
)

// DirectoryReconcileParams is the empty input of the scheduled pass.
type DirectoryReconcileParams struct{}

// DirectoryReconcileResult counts what the pass did across providers.
type DirectoryReconcileResult struct {
	Providers  int
	Suspended  int
	Reinstated int
	Aborted    int
	Failed     int
}

// ReconcileDirectories is the activity behind the directory-reconcile schedule.
func ReconcileDirectories(ctx context.Context, _ DirectoryReconcileParams) (*DirectoryReconcileResult, error) {
	return defaultActivities.reconcileDirectories(ctx)
}

func (a *Activities) reconcileDirectories(ctx context.Context) (*DirectoryReconcileResult, error) {
	providers, err := a.queries.ListEnabledLDAPProviders(ctx)
	if err != nil {
		a.passDone(schedules.DirectoryReconcile, err)
		return nil, err
	}

	res := &DirectoryReconcileResult{}
	var firstErr error
	for _, ap := range providers {
		res.Providers++
		suspended, reinstated, aborted, err := a.reconcileOneDirectory(ctx, ap)
		if err != nil {
			res.Failed++
			if firstErr == nil {
				firstErr = err
			}
			slog.Warn("directory reconcile", "provider", ap.Name, "err", err)
			continue
		}
		res.Suspended += suspended
		res.Reinstated += reinstated
		if aborted {
			res.Aborted++
		}
	}
	a.passDone(schedules.DirectoryReconcile, firstErr)
	return res, firstErr
}

// reconcilableUser is the subset of a linked user the planner needs.
type reconcilableUser struct {
	ID              pgtype.UUID
	ExternalID      string
	Role            string
	Status          string
	SuspendedReason *string
}

type roleChange struct {
	UserID pgtype.UUID
	Role   string
}

type teamChange struct {
	UserID   pgtype.UUID
	TeamID   string
	TeamRole string
}

// reconcilePlan is the pure decision of one pass. Tests exercise it without a
// directory or a database.
type reconcilePlan struct {
	Suspend   []reconcilableUser
	Reinstate []reconcilableUser
	Synced    []reconcilableUser
	Roles     []roleChange
	Teams     []teamChange
	Aborted   bool
}

// planDirectoryReconcile decides the changes. A missing or disabled directory
// entry suspends a linked active user; a re-enabled entry reinstate only a
// directory suspension. The safety brake aborts everything when more than
// max(5, 10%) of the linked users would be suspended in one pass.
func planDirectoryReconcile(dir []identityldap.DirectoryUser, users []reconcilableUser, roleMapping []identity.RoleMapping, teamMapping []identity.TeamMapping, defaultRole string) reconcilePlan {
	byID := make(map[string]identityldap.DirectoryUser, len(dir))
	for _, u := range dir {
		byID[u.ExternalID] = u
	}

	plan := reconcilePlan{}
	for _, user := range users {
		entry, present := byID[user.ExternalID]
		if !present || entry.Disabled {
			if user.Status == auth.StatusActive {
				plan.Suspend = append(plan.Suspend, user)
			}
			continue
		}

		plan.Synced = append(plan.Synced, user)
		if user.Status == auth.StatusSuspended && user.SuspendedReason != nil && *user.SuspendedReason == directorySuspensionReason {
			plan.Reinstate = append(plan.Reinstate, user)
		}

		mapped, err := identity.MapIdentity(identity.MappingInput{
			Groups:      entry.Groups,
			RoleMapping: roleMapping,
			TeamMapping: teamMapping,
			DefaultRole: defaultRole,
		})
		if err != nil {
			// A malformed mapping is a configuration error; skip the mapping
			// rather than guess. The run still reports the suspend decisions.
			continue
		}
		if mapped.Role != user.Role {
			plan.Roles = append(plan.Roles, roleChange{UserID: user.ID, Role: mapped.Role})
		}
		for _, team := range mapped.Teams {
			plan.Teams = append(plan.Teams, teamChange{UserID: user.ID, TeamID: team.TeamID, TeamRole: team.TeamRole})
		}
	}

	if limit := safetyBrakeLimit(len(users)); len(plan.Suspend) > limit {
		plan.Aborted = true
		plan.Suspend = nil
		plan.Reinstate = nil
		plan.Synced = nil
		plan.Roles = nil
		plan.Teams = nil
	}
	return plan
}

// safetyBrakeLimit is max(5, 10% of the linked users), rounded up.
func safetyBrakeLimit(users int) int {
	return int(math.Max(safetyBrakeFloor, math.Ceil(safetyBrakeRatio*float64(users))))
}

func (a *Activities) reconcileOneDirectory(ctx context.Context, ap db.AuthProvider) (suspended, reinstated int, aborted bool, err error) {
	if a.keyring == nil {
		return 0, 0, false, errors.New("directory reconcile: keyring is required")
	}
	cfg, err := identityldap.OpenConfig(a.keyring, ap.ID.String(), ap.ConfigEnc)
	if err != nil {
		return 0, 0, false, err
	}

	run, err := a.queries.CreateDirectoryReconcileRun(ctx, ap.ID)
	if err != nil {
		return 0, 0, false, err
	}

	fail := func(runErr error) (int, int, bool, error) {
		a.finishReconcileRun(ctx, run, reconcileOutcomeError, 0, 0, 0, runErr, nil)
		a.metrics.directory.Run(reconcileOutcomeError)
		return 0, 0, false, runErr
	}

	dir, err := identityldap.ListUsers(ctx, cfg)
	if err != nil {
		return fail(err)
	}

	rows, err := a.queries.ListUsersByAuthProvider(ctx, ap.ID)
	if err != nil {
		return fail(err)
	}

	users := make([]reconcilableUser, 0, len(rows))
	for _, u := range rows {
		externalID := ""
		if u.ExternalID != nil {
			externalID = *u.ExternalID
		}
		users = append(users, reconcilableUser{
			ID: u.ID, ExternalID: externalID, Role: u.Role, Status: u.Status, SuspendedReason: u.SuspendedReason,
		})
	}

	plan := planDirectoryReconcile(dir, users, cfg.RoleMapping, cfg.TeamMapping, cfg.DefaultRole)
	if plan.Aborted {
		a.finishReconcileRun(ctx, run, reconcileOutcomeAborted, len(dir), 0, 0, nil, nil)
		a.metrics.directory.Run(reconcileOutcomeAborted)
		a.auditChange(ctx, audit.Event{
			Action: audit.UserSuspend, ResourceType: "identity_provider", ResourceID: ap.ID.String(),
			Outcome: audit.Denied, Reason: "directory_safety_brake",
			Changes: map[string]audit.Change{"would_suspend": {To: strconv.Itoa(len(users))}},
		})
		return 0, 0, true, nil
	}

	actions := make([]reconcileAction, 0, len(plan.Suspend)+len(plan.Reinstate)+len(plan.Roles)+len(plan.Teams))
	if err := a.applyReconcilePlan(ctx, plan, &actions); err != nil {
		return fail(err)
	}
	a.finishReconcileRun(ctx, run, reconcileOutcomeSuccess, len(dir), len(plan.Suspend), len(plan.Reinstate), nil, actions)
	a.metrics.directory.Run(reconcileOutcomeSuccess)
	return len(plan.Suspend), len(plan.Reinstate), false, nil
}

type reconcileAction struct {
	UserID string `json:"user_id"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

func (a *Activities) applyReconcilePlan(ctx context.Context, plan reconcilePlan, actions *[]reconcileAction) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	for _, u := range plan.Suspend {
		reason := directorySuspensionReason
		if _, err := q.SuspendUser(ctx, db.SuspendUserParams{ID: u.ID, SuspendedReason: &reason}); err != nil {
			return err
		}
		if err := q.RevokeUserRefreshTokens(ctx, u.ID); err != nil {
			return err
		}
		if err := q.DeleteAPIKeysByUser(ctx, u.ID); err != nil {
			return err
		}
		*actions = append(*actions, reconcileAction{UserID: u.ID.String(), Action: "suspend", Detail: "missing_or_disabled"})
		a.metrics.directory.Suspended(directorySuspensionReason)
		a.auditChange(ctx, audit.Event{Action: audit.UserSuspend, ResourceType: "user", ResourceID: u.ID.String(), Outcome: audit.Allowed, Reason: "directory"})
	}
	for _, u := range plan.Reinstate {
		if err := q.ReinstateDirectoryUser(ctx, u.ID); err != nil {
			return err
		}
		*actions = append(*actions, reconcileAction{UserID: u.ID.String(), Action: "reinstate", Detail: "reenabled_in_directory"})
		a.auditChange(ctx, audit.Event{Action: audit.UserReinstate, ResourceType: "user", ResourceID: u.ID.String(), Outcome: audit.Allowed, Reason: "directory"})
	}
	for _, rc := range plan.Roles {
		if err := q.UpdateUserRole(ctx, db.UpdateUserRoleParams{ID: rc.UserID, Role: rc.Role}); err != nil {
			return err
		}
		*actions = append(*actions, reconcileAction{UserID: rc.UserID.String(), Action: "role", Detail: rc.Role})
		a.auditChange(ctx, audit.Event{Action: audit.UserRoleChange, ResourceType: "user", ResourceID: rc.UserID.String(), Outcome: audit.Allowed, Reason: "directory"})
	}
	for _, tc := range plan.Teams {
		teamID, err := uuidFromString(tc.TeamID)
		if err != nil {
			continue
		}
		if err := q.UpsertTeamMember(ctx, db.UpsertTeamMemberParams{TeamID: teamID, UserID: tc.UserID, Role: tc.TeamRole}); err != nil {
			return err
		}
		*actions = append(*actions, reconcileAction{UserID: tc.UserID.String(), Action: "team", Detail: tc.TeamID})
	}
	for _, u := range plan.Synced {
		// Not best effort: a failed statement aborts the transaction, so the
		// commit would fail anyway; report the cause instead.
		if err := q.MarkDirectorySynced(ctx, u.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (a *Activities) finishReconcileRun(ctx context.Context, run db.DirectoryReconcileRun, outcome string, seen, suspended, reinstated int, runErr error, actions []reconcileAction) {
	var errText *string
	if runErr != nil {
		msg := runErr.Error()
		errText = &msg
	}
	if actions == nil {
		actions = []reconcileAction{}
	}
	raw, err := json.Marshal(actions)
	if err != nil {
		raw = []byte("[]")
	}
	o := outcome
	if err := a.queries.FinishDirectoryReconcileRun(context.WithoutCancel(ctx), db.FinishDirectoryReconcileRunParams{
		ID: run.ID, Outcome: &o, Seen: int32(seen), Suspended: int32(suspended), Reinstated: int32(reinstated),
		Error: errText, Actions: raw,
	}); err != nil {
		slog.Warn("finish directory reconcile run", "run", run.ID.String(), "err", err)
	}
}

func (a *Activities) auditChange(ctx context.Context, ev audit.Event) {
	if a.audit == nil {
		return
	}
	// Audit is best effort; Record logs and counts its own failures.
	_ = a.audit.Record(audit.WithService(ctx, "directory-reconciler"), ev)
}

// uuidFromString parses a UUID string into the pgtype shape.
func uuidFromString(s string) (pgtype.UUID, error) {
	var id pgtype.UUID
	err := id.Scan(s)
	return id, err
}
