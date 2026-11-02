package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/workload"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Ordered, admin-managed workload rules (#539, ADR 0014): the CRUD surface and
// the :test endpoint that explains how a hypothetical run would be classified.

func workloadForbidden() ForbiddenJSONResponse {
	return ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only an administrator may manage workload rules"}
}

func workloadRuleNotFound() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workload rule not found"}
}

// ListWorkloadRules — GET /workload-rules (#539).
func (h *CatalogHandlers) ListWorkloadRules(ctx context.Context, _ ListWorkloadRulesRequestObject) (ListWorkloadRulesResponseObject, error) {
	if auth.RoleFromContext(ctx) != authz.RoleAdmin {
		return ListWorkloadRules403JSONResponse{ForbiddenJSONResponse: workloadForbidden()}, nil
	}
	rows, err := h.queries.ListWorkloadRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WorkloadRule, 0, len(rows))
	for _, row := range rows {
		rule, err := workloadRuleToPublic(row)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return ListWorkloadRules200JSONResponse(out), nil
}

// CreateWorkloadRule — POST /workload-rules (#539).
func (h *CatalogHandlers) CreateWorkloadRule(ctx context.Context, request CreateWorkloadRuleRequestObject) (CreateWorkloadRuleResponseObject, error) {
	if auth.RoleFromContext(ctx) != authz.RoleAdmin {
		return CreateWorkloadRule403JSONResponse{ForbiddenJSONResponse: workloadForbidden()}, nil
	}
	if request.Body == nil {
		return CreateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "a body is required")}, nil
	}

	class := queryengine.WorkloadClass(request.Body.Class)
	if !class.Valid() {
		return CreateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "unknown workload class")}, nil
	}
	match, err := workloadMatchFromRequest(request.Body.Match)
	if err != nil {
		return CreateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	}
	maxClass, err := workloadMaxClass(request.Body.MaxClass)
	if err != nil {
		return CreateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	}
	weight := 1
	if request.Body.Weight != nil {
		weight = *request.Body.Weight
	}
	if weight < 1 {
		return CreateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "weight must be at least 1")}, nil
	}
	position := 0
	if request.Body.Position != nil {
		if *request.Body.Position < 0 {
			return CreateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "position must not be negative")}, nil
		}
		position = *request.Body.Position
	} else {
		last, err := h.queries.MaxWorkloadRulePosition(ctx)
		if err != nil {
			return nil, err
		}
		position = int(last) + 1
	}
	enabled := true
	if request.Body.Enabled != nil {
		enabled = *request.Body.Enabled
	}
	raw, err := workload.EncodeRuleMatch(match)
	if err != nil {
		return CreateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	}

	row, err := h.queries.CreateWorkloadRule(ctx, db.CreateWorkloadRuleParams{
		Position:  int32(position), //nolint:gosec // validated non-negative
		Enabled:   enabled,
		Match:     raw,
		Class:     string(class),
		MaxClass:  maxClass,
		Weight:    int32(weight), //nolint:gosec // weight is a small admin setting
		CreatedBy: callerID(ctx),
	})
	if err != nil {
		return nil, err
	}
	out, err := workloadRuleToPublic(row)
	if err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{
		Action:       audit.WorkloadRuleCreate,
		ResourceType: "workload_rule",
		ResourceID:   idString(row.ID),
		Outcome:      audit.Allowed,
		Changes:      workloadRuleChanges(out),
	})
	return CreateWorkloadRule201JSONResponse(out), nil
}

// UpdateWorkloadRule — PATCH /workload-rules/{rule_id} (#539).
func (h *CatalogHandlers) UpdateWorkloadRule(ctx context.Context, request UpdateWorkloadRuleRequestObject) (UpdateWorkloadRuleResponseObject, error) {
	if auth.RoleFromContext(ctx) != authz.RoleAdmin {
		return UpdateWorkloadRule403JSONResponse{ForbiddenJSONResponse: workloadForbidden()}, nil
	}
	if request.Body == nil {
		return UpdateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "a body is required")}, nil
	}
	id := pgtype.UUID{Bytes: request.RuleId, Valid: true}
	row, err := h.queries.GetWorkloadRule(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return UpdateWorkloadRule404JSONResponse{NotFoundJSONResponse: workloadRuleNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}

	match, err := workload.ParseRuleMatch(row.Match)
	if err != nil {
		return nil, err
	}
	enabled := row.Enabled
	position := row.Position
	class := queryengine.WorkloadClass(row.Class)
	weight := int(row.Weight)
	var maxClass *string
	if row.MaxClass != nil {
		s := *row.MaxClass
		maxClass = &s
	}

	if request.Body.Match != nil {
		if match, err = workloadMatchFromRequest(request.Body.Match); err != nil {
			return UpdateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
		}
	}
	if request.Body.Enabled != nil {
		enabled = *request.Body.Enabled
	}
	if request.Body.Position != nil {
		if *request.Body.Position < 0 {
			return UpdateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "position must not be negative")}, nil
		}
		position = int32(*request.Body.Position) //nolint:gosec // validated non-negative
	}
	if request.Body.Class != nil {
		class = queryengine.WorkloadClass(*request.Body.Class)
		if !class.Valid() {
			return UpdateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "unknown workload class")}, nil
		}
	}
	if request.Body.MaxClass != nil {
		if maxClass, err = workloadMaxClass(request.Body.MaxClass); err != nil {
			return UpdateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
		}
	}
	if request.Body.Weight != nil {
		weight = *request.Body.Weight
		if weight < 1 {
			return UpdateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "weight must be at least 1")}, nil
		}
	}
	if !class.Valid() {
		return UpdateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "unknown workload class")}, nil
	}
	raw, err := workload.EncodeRuleMatch(match)
	if err != nil {
		return UpdateWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	}

	updated, err := h.queries.UpdateWorkloadRule(ctx, db.UpdateWorkloadRuleParams{
		ID:       id,
		Position: position,
		Enabled:  enabled,
		Match:    raw,
		Class:    string(class),
		MaxClass: maxClass,
		Weight:   int32(weight), //nolint:gosec // weight is a small admin setting
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UpdateWorkloadRule404JSONResponse{NotFoundJSONResponse: workloadRuleNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := workloadRuleToPublic(updated)
	if err != nil {
		return nil, err
	}
	before, _ := workloadRuleToPublic(row)
	h.audited(ctx, audit.Event{
		Action:       audit.WorkloadRuleUpdate,
		ResourceType: "workload_rule",
		ResourceID:   idString(updated.ID),
		Outcome:      audit.Allowed,
		Changes:      workloadRuleDiff(before, out),
	})
	return UpdateWorkloadRule200JSONResponse(out), nil
}

// DeleteWorkloadRule — DELETE /workload-rules/{rule_id} (#539).
func (h *CatalogHandlers) DeleteWorkloadRule(ctx context.Context, request DeleteWorkloadRuleRequestObject) (DeleteWorkloadRuleResponseObject, error) {
	if auth.RoleFromContext(ctx) != authz.RoleAdmin {
		return DeleteWorkloadRule403JSONResponse{ForbiddenJSONResponse: workloadForbidden()}, nil
	}
	id := pgtype.UUID{Bytes: request.RuleId, Valid: true}
	deleted, err := h.queries.DeleteWorkloadRule(ctx, id)
	if err != nil {
		return nil, err
	}
	if deleted == 0 {
		return DeleteWorkloadRule404JSONResponse{NotFoundJSONResponse: workloadRuleNotFound()}, nil
	}
	h.audited(ctx, audit.Event{
		Action:       audit.WorkloadRuleDelete,
		ResourceType: "workload_rule",
		ResourceID:   idString(id),
		Outcome:      audit.Allowed,
	})
	return DeleteWorkloadRule204Response{}, nil
}

// TestWorkloadRule — POST /workload-rules:test (#539). Returns the class the
// hypothetical run would get and the rule (or origin/default step) that chose
// it.
func (h *CatalogHandlers) TestWorkloadRule(ctx context.Context, request TestWorkloadRuleRequestObject) (TestWorkloadRuleResponseObject, error) {
	if auth.RoleFromContext(ctx) != authz.RoleAdmin {
		return TestWorkloadRule403JSONResponse{ForbiddenJSONResponse: workloadForbidden()}, nil
	}
	if request.Body == nil {
		return TestWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "a body is required")}, nil
	}
	origin := workload.Origin(request.Body.Origin)
	if !origin.Valid() {
		return TestWorkloadRule422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "unknown origin")}, nil
	}
	rules, err := h.workloadRules(ctx)
	if err != nil {
		return nil, err
	}
	decision := workload.Decide(origin, workload.RuleInput{
		WorkspaceID:  workloadUUIDString(request.Body.WorkspaceId),
		TeamID:       workloadUUIDString(request.Body.TeamId),
		Role:         workloadString(request.Body.Role),
		UserID:       workloadUUIDString(request.Body.UserId),
		AgentID:      workloadUUIDString(request.Body.AgentId),
		EngineID:     workloadUUIDString(request.Body.EngineId),
		Origin:       origin,
		SavedQueryID: workloadUUIDString(request.Body.SavedQueryId),
		Statements:   workloadInt(request.Body.Statements),
	}, rules)

	out := WorkloadRuleTestResult{
		Class:  WorkloadRuleTestResultClass(decision.Class),
		Reason: decision.Reason,
		Weight: decision.Weight,
	}
	if id, ok := strings.CutPrefix(decision.Reason, "rule:"); ok {
		if parsed, err := uuid.Parse(id); err == nil {
			rid := openapi_types.UUID(parsed)
			out.RuleId = &rid
		}
	}
	return TestWorkloadRule200JSONResponse(out), nil
}

// workloadRules loads the ordered rules in their in-memory form (#539, #546).
func (h *core) workloadRules(ctx context.Context) ([]workload.Rule, error) {
	rows, err := h.queries.ListWorkloadRules(ctx)
	if err != nil {
		return nil, err
	}
	rules := make([]workload.Rule, 0, len(rows))
	for _, row := range rows {
		match, err := workload.ParseRuleMatch(row.Match)
		if err != nil {
			// A stored rule outside the closed schema is a bug; skip it rather
			// than fail every classification.
			h.logger.WarnContext(ctx, "skipping an invalid workload rule", "rule_id", idString(row.ID), "err", err)
			continue
		}
		rule := workload.Rule{
			ID:       idString(row.ID),
			Position: int(row.Position),
			Enabled:  row.Enabled,
			Match:    match,
			Class:    queryengine.WorkloadClass(row.Class),
			Weight:   int(row.Weight),
		}
		if row.MaxClass != nil {
			rule.MaxClass = queryengine.WorkloadClass(*row.MaxClass)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// workloadRuleToPublic maps a stored rule to the API shape.
func workloadRuleToPublic(row db.WorkloadRule) (WorkloadRule, error) {
	match, err := workload.ParseRuleMatch(row.Match)
	if err != nil {
		return WorkloadRule{}, err
	}
	out := WorkloadRule{
		Id:        openapi_types.UUID(row.ID.Bytes),
		Position:  int(row.Position),
		Enabled:   row.Enabled,
		Match:     workloadMatchToPublic(match),
		Class:     WorkloadRuleClass(row.Class),
		Weight:    int(row.Weight),
		CreatedAt: pgTimestampToTime(row.CreatedAt),
		UpdatedAt: pgTimestampToTime(row.UpdatedAt),
	}
	if row.MaxClass != nil {
		mc := WorkloadRuleMaxClass(*row.MaxClass)
		out.MaxClass = &mc
	}
	return out, nil
}

// workloadMatchToPublic renders a stored match. An invalid UUID in storage is
// dropped rather than panicking the request.
func workloadMatchToPublic(m workload.RuleMatch) WorkloadRuleMatch {
	out := WorkloadRuleMatch{
		WorkspaceId:   workloadUUIDPtr(m.WorkspaceID),
		TeamId:        workloadUUIDPtr(m.TeamID),
		UserId:        workloadUUIDPtr(m.UserID),
		AgentId:       workloadUUIDPtr(m.AgentID),
		EngineId:      workloadUUIDPtr(m.EngineID),
		SavedQueryId:  workloadUUIDPtr(m.SavedQueryID),
		MinStatements: workloadIntPtr(m.MinStatements),
		MaxStatements: workloadIntPtr(m.MaxStatements),
	}
	if m.Role != "" {
		role := m.Role
		out.Role = &role
	}
	if m.Origin != "" {
		origin := WorkloadRuleMatchOrigin(m.Origin)
		out.Origin = &origin
	}
	return out
}

// workloadMatchFromRequest validates and converts the closed match schema.
func workloadMatchFromRequest(m *WorkloadRuleMatch) (workload.RuleMatch, error) {
	var out workload.RuleMatch
	if m == nil {
		return out, nil
	}
	out.WorkspaceID = workloadUUIDString(m.WorkspaceId)
	out.TeamID = workloadUUIDString(m.TeamId)
	out.UserID = workloadUUIDString(m.UserId)
	out.AgentID = workloadUUIDString(m.AgentId)
	out.EngineID = workloadUUIDString(m.EngineId)
	out.SavedQueryID = workloadUUIDString(m.SavedQueryId)
	if m.Role != nil {
		out.Role = *m.Role
	}
	if m.Origin != nil {
		origin := workload.Origin(*m.Origin)
		if !origin.Valid() {
			return out, fmt.Errorf("unknown origin %q", *m.Origin)
		}
		out.Origin = origin
	}
	if m.MinStatements != nil {
		if *m.MinStatements < 1 {
			return out, errors.New("min_statements must be at least 1")
		}
		out.MinStatements = *m.MinStatements
	}
	if m.MaxStatements != nil {
		if *m.MaxStatements < 1 {
			return out, errors.New("max_statements must be at least 1")
		}
		out.MaxStatements = *m.MaxStatements
	}
	if out.MinStatements > 0 && out.MaxStatements > 0 && out.MinStatements > out.MaxStatements {
		return out, errors.New("min_statements must not exceed max_statements")
	}
	return out, nil
}

// workloadMaxClass validates the optional max class. It returns nil for both
// "omitted" and "explicitly cleared".
func workloadMaxClass[T ~string](v *T) (*string, error) {
	if v == nil {
		return nil, nil
	}
	class := queryengine.WorkloadClass(*v)
	if !class.Valid() {
		return nil, errors.New("unknown max workload class")
	}
	s := string(class)
	return &s, nil
}

// workloadRuleChanges is the audit payload of a create.
func workloadRuleChanges(rule WorkloadRule) map[string]audit.Change {
	return map[string]audit.Change{
		"position":  {To: rule.Position},
		"enabled":   {To: rule.Enabled},
		"match":     {To: rule.Match},
		"class":     {To: rule.Class},
		"max_class": {To: rule.MaxClass},
		"weight":    {To: rule.Weight},
	}
}

// workloadRuleDiff is the audit payload of an update: only the fields that
// changed.
func workloadRuleDiff(before, after WorkloadRule) map[string]audit.Change {
	changes := map[string]audit.Change{}
	addChange := func(field string, from, to any) {
		if fmt.Sprint(from) != fmt.Sprint(to) {
			changes[field] = audit.Change{From: from, To: to}
		}
	}
	addChange("position", before.Position, after.Position)
	addChange("enabled", before.Enabled, after.Enabled)
	addChange("match", before.Match, after.Match)
	addChange("class", before.Class, after.Class)
	addChange("max_class", before.MaxClass, after.MaxClass)
	addChange("weight", before.Weight, after.Weight)
	return changes
}

// callerID is the creating user, or the zero UUID for an agent principal.
func callerID(ctx context.Context) pgtype.UUID {
	var id pgtype.UUID
	if err := id.Scan(auth.UserIDFromContext(ctx)); err != nil {
		return pgtype.UUID{}
	}
	return id
}

func workloadUUIDString(v *openapi_types.UUID) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func workloadUUIDPtr(s string) *openapi_types.UUID {
	if s == "" {
		return nil
	}
	parsed, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	out := openapi_types.UUID(parsed)
	return &out
}

func workloadString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func workloadInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func workloadIntPtr(v int) *int {
	if v <= 0 {
		return nil
	}
	return &v
}
