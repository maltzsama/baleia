package api

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/bikeys"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ListBiKeys — GET /workspaces/{id}/bi-keys (#708). A workspace admin sees
// every key of the workspace; a member sees their own.
func (h *WorkspaceHandlers) ListBiKeys(ctx context.Context, request ListBiKeysRequestObject) (ListBiKeysResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); errors.Is(err, ErrWorkspaceNotFound) {
		return ListBiKeys404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListBiKeys401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	svc := h.biKeyService()
	if svc == nil {
		return nil, errors.New("bi keys are unavailable without the store")
	}
	wsID := workspaceIDFromRequest(request.Id)
	role, err := h.authz().WorkspaceRole(ctx, p, wsID)
	if errors.Is(err, authz.ErrNotFound) {
		return ListBiKeys404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	keys, err := svc.List(ctx, wsID, p.UserID, role >= authz.WorkspaceAdmin)
	if err != nil {
		return nil, err
	}
	out := make([]BiKey, 0, len(keys))
	emails := map[[16]byte]string{}
	for _, key := range keys {
		email, ok := emails[key.UserID.Bytes]
		if !ok {
			owner, err := h.queries.GetUserByID(ctx, key.UserID)
			if err != nil {
				return nil, err
			}
			email = owner.Email
			emails[key.UserID.Bytes] = email
		}
		out = append(out, biKeyToPublic(key, email))
	}
	return ListBiKeys200JSONResponse(out), nil
}

// CreateBiKey — POST /workspaces/{id}/bi-keys (#708). A member creates a key
// for themselves; a workspace admin may create one for a service account of
// the workspace. The secret is returned once and never stored.
func (h *WorkspaceHandlers) CreateBiKey(ctx context.Context, request CreateBiKeyRequestObject) (CreateBiKeyResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); errors.Is(err, ErrWorkspaceNotFound) {
		return CreateBiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateBiKey401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if p.IsAgent() {
		return CreateBiKey403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "an agent cannot create BI keys"}}, nil
	}
	body := request.Body
	if body == nil || !requiredName(body.Name) {
		return CreateBiKey422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
			"name is required", FieldError{Field: "name", Code: FieldCodeRequired},
		)}, nil
	}
	if body.ExpiresAt != nil && !body.ExpiresAt.After(time.Now()) {
		return CreateBiKey422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
			"expires_at must be in the future", FieldError{Field: "expires_at", Code: FieldCodeOutOfRange},
		)}, nil
	}
	svc := h.biKeyService()
	if svc == nil {
		return nil, errors.New("bi keys are unavailable without the store")
	}
	wsID := workspaceIDFromRequest(request.Id)
	role, err := h.authz().WorkspaceRole(ctx, p, wsID)
	if errors.Is(err, authz.ErrNotFound) {
		return CreateBiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	owner := p.UserID
	if body.OwnerId != nil {
		owner = db.UUID{Bytes: *body.OwnerId, Valid: true}
	}
	key, secret, err := svc.Create(ctx, bikeys.CreateParams{
		WorkspaceID:           wsID,
		EngineID:              db.UUID{Bytes: body.EngineId, Valid: true},
		ActorID:               p.UserID,
		OwnerID:               owner,
		ActorIsWorkspaceAdmin: role >= authz.WorkspaceAdmin,
		Name:                  body.Name,
		ExpiresAt:             body.ExpiresAt,
	})
	ev := audit.Event{Action: audit.APIKeyCreate, ResourceType: "bi_key", WorkspaceID: idString(wsID)}
	switch {
	case errors.Is(err, bikeys.ErrForbidden):
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return CreateBiKey403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "a workspace admin may create a key for a service account of the workspace"}}, nil
	case errors.Is(err, bikeys.ErrTargetUnavailable):
		h.audited(ctx, ev.Denied(audit.ReasonValidation))
		return CreateBiKey422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
			"engine_id must be an enabled Trino target of the workspace", FieldError{Field: "engine_id", Code: FieldCodeInvalidFormat},
		)}, nil
	case errors.Is(err, bikeys.ErrInvalid):
		h.audited(ctx, ev.Denied(audit.ReasonValidation))
		return CreateBiKey422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
			"name and expires_at must be valid", FieldError{Field: "name", Code: FieldCodeInvalidFormat},
		)}, nil
	case err != nil:
		return nil, err
	}
	ev.ResourceID = idString(key.ID)
	ev.Changes = map[string]audit.Change{
		"name":      {To: key.Name},
		"engine_id": {To: idString(key.EngineID)},
		"owner_id":  {To: idString(key.UserID)},
	}
	h.audited(ctx, ev.Allowed())
	ownerUser, err := h.queries.GetUserByID(ctx, key.UserID)
	if err != nil {
		return nil, err
	}
	pub := biKeyToPublic(key, ownerUser.Email)
	created := BiKeyCreated{
		Id: pub.Id, Name: pub.Name, KeyPrefix: pub.KeyPrefix,
		WorkspaceId: pub.WorkspaceId, EngineId: pub.EngineId,
		OwnerId: pub.OwnerId, OwnerEmail: pub.OwnerEmail,
		ExpiresAt: pub.ExpiresAt, CreatedAt: pub.CreatedAt, LastUsedAt: pub.LastUsedAt,
		Secret: secret,
	}
	return CreateBiKey201JSONResponse(created), nil
}

// DeleteBiKey — DELETE /workspaces/{id}/bi-keys/{key_id} (#708). The owner
// revokes their own key; a workspace admin any key of the workspace.
func (h *WorkspaceHandlers) DeleteBiKey(ctx context.Context, request DeleteBiKeyRequestObject) (DeleteBiKeyResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); errors.Is(err, ErrWorkspaceNotFound) {
		return DeleteBiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteBiKey401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	svc := h.biKeyService()
	if svc == nil {
		return nil, errors.New("bi keys are unavailable without the store")
	}
	wsID := workspaceIDFromRequest(request.Id)
	role, err := h.authz().WorkspaceRole(ctx, p, wsID)
	if errors.Is(err, authz.ErrNotFound) {
		return DeleteBiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	keyID := db.UUID{Bytes: request.KeyId, Valid: true}
	key, err := svc.Revoke(ctx, wsID, keyID, p.UserID, role >= authz.WorkspaceAdmin)
	ev := audit.Event{Action: audit.APIKeyRevoke, ResourceType: "bi_key", ResourceID: idString(keyID), WorkspaceID: idString(wsID)}
	switch {
	case errors.Is(err, bikeys.ErrNotFound):
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return DeleteBiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "bi key not found"}}, nil
	case errors.Is(err, bikeys.ErrForbidden):
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return DeleteBiKey403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the key owner or a workspace admin may revoke it"}}, nil
	case errors.Is(err, bikeys.ErrInvalid):
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return DeleteBiKey404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "bi key not found"}}, nil
	case err != nil:
		return nil, err
	}
	ev.Changes = map[string]audit.Change{"name": {To: key.Name}}
	h.audited(ctx, ev.Allowed())
	return DeleteBiKey204Response{}, nil
}

// biKeyToPublic renders a key without its secret.
func biKeyToPublic(k db.ApiKey, ownerEmail string) BiKey {
	prefix := k.Prefix
	if prefix == "" {
		prefix = k.KeyHash[:8] // pre-#276 rows
	}
	out := BiKey{
		Id:          openapi_types.UUID(k.ID.Bytes),
		Name:        k.Name,
		KeyPrefix:   prefix + "...",
		WorkspaceId: openapi_types.UUID(k.WorkspaceID.Bytes),
		EngineId:    openapi_types.UUID(k.EngineID.Bytes),
		OwnerId:     openapi_types.UUID(k.UserID.Bytes),
		OwnerEmail:  ownerEmail,
		CreatedAt:   pgTimestampToTime(k.CreatedAt),
	}
	if k.ExpiresAt.Valid {
		t := k.ExpiresAt.Time
		out.ExpiresAt = &t
	}
	if k.LastUsedAt.Valid {
		t := k.LastUsedAt.Time
		out.LastUsedAt = &t
	}
	return out
}
