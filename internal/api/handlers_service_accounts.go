package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/slug"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// serviceAccountEmailDomain is reserved (RFC 2606): a service account's
// address never receives mail.
const serviceAccountEmailDomain = "service-accounts.baleia.invalid"

// clientExternalID is how a service account linked to an identity provider
// client is stored, apart from the subjects of people's sign-ins.
func clientExternalID(clientID string) string { return "client:" + clientID }

func serviceAccountToPublic(u db.User, acts []ServiceAccountAllowedActions) ServiceAccount {
	out := ServiceAccount{
		Id:          openapi_types.UUID(u.ID.Bytes),
		Name:        u.Name,
		Email:       u.Email,
		Description: u.Description,
		Status:      ServiceAccountStatus(u.Status),
		CreatedAt:   pgTimestampToTime(u.CreatedAt),
	}
	if acts == nil {
		acts = []ServiceAccountAllowedActions{}
	}
	out.AllowedActions = &acts
	if u.AuthProviderID.Valid && u.ExternalID != nil {
		pid := openapi_types.UUID(u.AuthProviderID.Bytes)
		cid := strings.TrimPrefix(*u.ExternalID, "client:")
		out.IdentityProviderId, out.ClientId = &pid, &cid
	}
	if u.HomeWorkspaceID.Valid {
		ws := openapi_types.UUID(u.HomeWorkspaceID.Bytes)
		out.HomeWorkspaceId = &ws
	}
	return out
}

// serviceAccountManager is the caller who may manage service accounts: a
// person, never an agent or another service account (#653).
func (h *core) serviceAccountManager(ctx context.Context) (authz.Principal, bool) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil || p.IsAgent() {
		return authz.Principal{}, false
	}
	u, err := h.queries.GetUserByID(ctx, p.UserID)
	if err != nil || u.Kind == "service_account" {
		return authz.Principal{}, false
	}
	return p, true
}

// managesServiceAccount: a global admin manages every service account, a
// workspace admin those created in their workspace.
func (h *core) managesServiceAccount(ctx context.Context, p authz.Principal, sa db.User) bool {
	if p.IsAdmin() {
		return true
	}
	if !sa.HomeWorkspaceID.Valid {
		return false
	}
	role, err := h.authz().WorkspaceRole(ctx, p, sa.HomeWorkspaceID)
	return err == nil && role >= authz.WorkspaceAdmin
}

// managedServiceAccount loads a service account its caller manages; false
// for anything else (404, no existence oracle).
func (h *core) managedServiceAccount(ctx context.Context, id openapi_types.UUID) (authz.Principal, db.User, bool, error) {
	p, ok := h.serviceAccountManager(ctx)
	if !ok {
		return p, db.User{}, false, nil
	}
	sa, err := h.queries.GetServiceAccount(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return p, db.User{}, false, nil
	}
	if err != nil {
		return p, db.User{}, false, err
	}
	if !h.managesServiceAccount(ctx, p, sa) {
		return p, db.User{}, false, nil
	}
	return p, sa, true, nil
}

// allServiceAccountActions: whoever manages a service account may do all of it.
var allServiceAccountActions = []ServiceAccountAllowedActions{"view", "update", "delete", "manage_tokens"}

func serviceAccountNotFound() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "service account not found"}
}

// identityLink validates an identity provider link: both or neither, and an
// enabled OIDC provider.
func (h *core) identityLink(ctx context.Context, providerID *openapi_types.UUID, clientID *string) (authProvider string, pid pgtype.UUID, external *string, fieldErr *FieldError, err error) {
	cid := ""
	if clientID != nil {
		cid = strings.TrimSpace(*clientID)
	}
	switch {
	case providerID == nil && cid == "":
		return "service", pgtype.UUID{}, nil, nil, nil
	case providerID == nil || cid == "":
		return "", pgtype.UUID{}, nil, &FieldError{Field: "client_id", Code: FieldCodeRequired}, nil
	}
	ap, err := h.queries.GetAuthProviderByID(ctx, pgtype.UUID{Bytes: *providerID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ap.Type != "oidc") {
		return "", pgtype.UUID{}, nil, &FieldError{Field: "identity_provider_id", Code: FieldCodeInvalidFormat}, nil
	}
	if err != nil {
		return "", pgtype.UUID{}, nil, nil, err
	}
	ext := clientExternalID(cid)
	return "oidc", ap.ID, &ext, nil, nil
}

func newServiceAccountEmail(name string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	base := slug.From(name)
	if base == "" {
		base = "service-account"
	}
	return base + "-" + hex.EncodeToString(b) + "@" + serviceAccountEmailDomain
}

// ListServiceAccounts — GET /service-accounts (#653).
func (h *AuthHandlers) ListServiceAccounts(ctx context.Context, _ ListServiceAccountsRequestObject) (ListServiceAccountsResponseObject, error) {
	p, ok := h.serviceAccountManager(ctx)
	if !ok {
		return ListServiceAccounts200JSONResponse{}, nil
	}
	var rows []db.User
	var err error
	if p.IsAdmin() {
		rows, err = h.queries.ListServiceAccounts(ctx)
	} else {
		var ws []pgtype.UUID
		if ws, err = h.queries.ListAdminWorkspaceIDs(ctx, p.UserID); err == nil && len(ws) > 0 {
			rows, err = h.queries.ListServiceAccountsOfWorkspaces(ctx, ws)
		}
	}
	if err != nil {
		return nil, err
	}
	out := ListServiceAccounts200JSONResponse{}
	for _, sa := range rows {
		if h.managesServiceAccount(ctx, p, sa) {
			out = append(out, serviceAccountToPublic(sa, allServiceAccountActions))
		}
	}
	return out, nil
}

// CreateServiceAccount — POST /service-accounts (#653).
func (h *AuthHandlers) CreateServiceAccount(ctx context.Context, request CreateServiceAccountRequestObject) (CreateServiceAccountResponseObject, error) {
	p, ok := h.serviceAccountManager(ctx)
	if !ok {
		return CreateServiceAccount403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only people may create service accounts"}}, nil
	}
	if request.Body == nil || !requiredName(request.Body.Name) {
		return CreateServiceAccount422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields("name is required", FieldError{Field: "name", Code: FieldCodeRequired})}, nil
	}
	body := request.Body
	ev := audit.Event{Action: audit.ServiceAccountCreate, ResourceType: "service_account", Changes: map[string]audit.Change{"name": {To: body.Name}}}
	var home pgtype.UUID
	role := "viewer"
	if body.Role != nil {
		role = string(*body.Role)
	}
	if authz.ParseWorkspaceRole(role) == authz.WorkspaceNone {
		return CreateServiceAccount422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields("role must be admin, editor or viewer", FieldError{Field: "role", Code: FieldCodeInvalidFormat})}, nil
	}
	switch {
	case body.WorkspaceId != nil:
		home = pgtype.UUID{Bytes: *body.WorkspaceId, Valid: true}
		if _, err := h.authz().RequireWorkspaceRole(ctx, p, home, authz.WorkspaceAdmin); err != nil {
			if errors.Is(err, authz.ErrNotFound) {
				return CreateServiceAccount404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
			}
			if errors.Is(err, authz.ErrForbidden) {
				h.audited(ctx, ev.Denied(audit.ReasonForbidden))
				return CreateServiceAccount403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only a workspace admin may create service accounts in it"}}, nil
			}
			return nil, err
		}
	case !p.IsAdmin():
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return CreateServiceAccount403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "name a workspace you administer"}}, nil
	}
	authProvider, pid, external, fieldErr, err := h.identityLink(ctx, body.IdentityProviderId, body.ClientId)
	if err != nil {
		return nil, err
	}
	if fieldErr != nil {
		return CreateServiceAccount422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields("identity_provider_id and client_id go together and name an OIDC provider", *fieldErr)}, nil
	}
	var sa db.User
	err = h.tx.InTx(ctx, func(q db.Querier) error {
		var err error
		sa, err = q.CreateServiceAccount(ctx, db.CreateServiceAccountParams{
			Name: strings.TrimSpace(body.Name), Email: newServiceAccountEmail(body.Name),
			AuthProvider: authProvider, AuthProviderID: pid, ExternalID: external,
			HomeWorkspaceID: home, Description: body.Description,
		})
		if err != nil {
			return err
		}
		if home.Valid {
			n, err := q.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{WorkspaceID: home, UserID: sa.ID, Role: role, GrantedBy: p.UserID})
			if err == nil && n == 0 {
				err = errors.New("service account: not added to its workspace")
			}
			return err
		}
		return nil
	})
	if isUniqueViolation(err) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return CreateServiceAccount409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: "a service account already signs in as this client"}}, nil
	}
	if err != nil {
		return nil, err
	}
	ev.ResourceID = idString(sa.ID)
	h.audited(ctx, ev.Allowed())
	return CreateServiceAccount201JSONResponse(serviceAccountToPublic(sa, allServiceAccountActions)), nil
}

// GetServiceAccount — GET /service-accounts/{id} (#653).
func (h *AuthHandlers) GetServiceAccount(ctx context.Context, request GetServiceAccountRequestObject) (GetServiceAccountResponseObject, error) {
	_, sa, ok, err := h.managedServiceAccount(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetServiceAccount404JSONResponse{NotFoundJSONResponse: serviceAccountNotFound()}, nil
	}
	return GetServiceAccount200JSONResponse(serviceAccountToPublic(sa, allServiceAccountActions)), nil
}

// UpdateServiceAccount — PATCH /service-accounts/{id} (#653).
func (h *AuthHandlers) UpdateServiceAccount(ctx context.Context, request UpdateServiceAccountRequestObject) (UpdateServiceAccountResponseObject, error) {
	_, sa, ok, err := h.managedServiceAccount(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return UpdateServiceAccount404JSONResponse{NotFoundJSONResponse: serviceAccountNotFound()}, nil
	}
	if request.Body == nil || !requiredName(request.Body.Name) {
		return UpdateServiceAccount422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields("name is required", FieldError{Field: "name", Code: FieldCodeRequired})}, nil
	}
	authProvider, pid, external, fieldErr, err := h.identityLink(ctx, request.Body.IdentityProviderId, request.Body.ClientId)
	if err != nil {
		return nil, err
	}
	if fieldErr != nil {
		return UpdateServiceAccount422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields("identity_provider_id and client_id go together and name an OIDC provider", *fieldErr)}, nil
	}
	ev := audit.Event{Action: audit.ServiceAccountUpdate, ResourceType: "service_account", ResourceID: idString(sa.ID)}
	updated, err := h.queries.UpdateServiceAccount(ctx, db.UpdateServiceAccountParams{
		ID: sa.ID, Name: strings.TrimSpace(request.Body.Name), Description: request.Body.Description,
		AuthProvider: authProvider, AuthProviderID: pid, ExternalID: external,
	})
	if isUniqueViolation(err) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return UpdateServiceAccount409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: "a service account already signs in as this client"}}, nil
	}
	if err != nil {
		return nil, err
	}
	// A new identity-provider link drops the token of the old one.
	if sa.AuthProviderID != updated.AuthProviderID || !strEq(sa.ExternalID, updated.ExternalID) {
		if err := h.queries.DeleteUserIDPToken(ctx, sa.ID); err != nil {
			return nil, err
		}
	}
	h.audited(ctx, ev.Allowed())
	return UpdateServiceAccount200JSONResponse(serviceAccountToPublic(updated, allServiceAccountActions)), nil
}

func strEq(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// DeleteServiceAccount — DELETE /service-accounts/{id} (#653): its tokens,
// agents and memberships go with it.
func (h *AuthHandlers) DeleteServiceAccount(ctx context.Context, request DeleteServiceAccountRequestObject) (DeleteServiceAccountResponseObject, error) {
	_, sa, ok, err := h.managedServiceAccount(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return DeleteServiceAccount404JSONResponse{NotFoundJSONResponse: serviceAccountNotFound()}, nil
	}
	if _, err := h.queries.DeleteServiceAccount(ctx, sa.ID); err != nil {
		return nil, err
	}
	h.catalogCache.Clear()
	h.audited(ctx, audit.Event{Action: audit.ServiceAccountDelete, ResourceType: "service_account", ResourceID: idString(sa.ID), Outcome: audit.Allowed})
	return DeleteServiceAccount204Response{}, nil
}

// ListServiceAccountTokens — GET /service-accounts/{id}/tokens (#653).
func (h *AuthHandlers) ListServiceAccountTokens(ctx context.Context, request ListServiceAccountTokensRequestObject) (ListServiceAccountTokensResponseObject, error) {
	_, sa, ok, err := h.managedServiceAccount(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ListServiceAccountTokens404JSONResponse{NotFoundJSONResponse: serviceAccountNotFound()}, nil
	}
	keys, err := h.apiKeys.List(ctx, sa.ID)
	if err != nil {
		return nil, err
	}
	out := ListServiceAccountTokens200JSONResponse{}
	for _, k := range keys {
		out = append(out, dbApiKeyToPublic(k))
	}
	return out, nil
}

// CreateServiceAccountToken — POST /service-accounts/{id}/tokens (#653). The
// secret is shown once and never stored.
func (h *AuthHandlers) CreateServiceAccountToken(ctx context.Context, request CreateServiceAccountTokenRequestObject) (CreateServiceAccountTokenResponseObject, error) {
	_, sa, ok, err := h.managedServiceAccount(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return CreateServiceAccountToken404JSONResponse{NotFoundJSONResponse: serviceAccountNotFound()}, nil
	}
	if request.Body == nil || !requiredName(request.Body.Name) {
		return CreateServiceAccountToken422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields("name is required", FieldError{Field: "name", Code: FieldCodeRequired})}, nil
	}
	if request.Body.ExpiresAt != nil && !request.Body.ExpiresAt.After(time.Now()) {
		return CreateServiceAccountToken422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields("expires_at must be in the future", FieldError{Field: "expires_at", Code: FieldCodeOutOfRange})}, nil
	}
	key, secret, err := h.apiKeys.Create(ctx, sa.ID, request.Body.Name, request.Body.ExpiresAt)
	if err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{Action: audit.ServiceAccountTokenCreate, ResourceType: "service_account", ResourceID: idString(sa.ID), Outcome: audit.Allowed,
		Changes: map[string]audit.Change{"token": {To: key.Name}}})
	pub := dbApiKeyToPublic(key)
	return CreateServiceAccountToken201JSONResponse{
		Id: pub.Id, Name: pub.Name, KeyPrefix: pub.KeyPrefix, ExpiresAt: pub.ExpiresAt, CreatedAt: pub.CreatedAt, Secret: secret,
	}, nil
}

// RevokeServiceAccountToken — DELETE /service-accounts/{id}/tokens/{token_id}.
func (h *AuthHandlers) RevokeServiceAccountToken(ctx context.Context, request RevokeServiceAccountTokenRequestObject) (RevokeServiceAccountTokenResponseObject, error) {
	_, sa, ok, err := h.managedServiceAccount(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return RevokeServiceAccountToken404JSONResponse{NotFoundJSONResponse: serviceAccountNotFound()}, nil
	}
	if err := h.apiKeys.Delete(ctx, sa.ID, pgtype.UUID{Bytes: request.TokenId, Valid: true}); err != nil {
		return nil, err
	}
	h.audited(ctx, audit.Event{Action: audit.ServiceAccountTokenRevoke, ResourceType: "service_account", ResourceID: idString(sa.ID), Outcome: audit.Allowed,
		Changes: map[string]audit.Change{"token": {From: request.TokenId.String()}}})
	return RevokeServiceAccountToken204Response{}, nil
}
