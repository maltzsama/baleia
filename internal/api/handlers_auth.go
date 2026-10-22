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
	"github.com/galileostudio/baleia/internal/identity/account"
	"github.com/galileostudio/baleia/internal/identity/login"
	"github.com/galileostudio/baleia/internal/identity/oidclogin"
	"github.com/galileostudio/baleia/internal/identity/passwordreset"
	"github.com/galileostudio/baleia/internal/identity/provision"
	"github.com/galileostudio/baleia/internal/identity/session"
	"github.com/galileostudio/baleia/internal/identity/setup"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/version"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ── Setup ─────────────────────────────────────────────────────────────────────

func (h *AuthHandlers) GetSetupStatus(ctx context.Context, request GetSetupStatusRequestObject) (GetSetupStatusResponseObject, error) {
	configured, err := h.setup.Configured(ctx)
	if err != nil {
		return nil, err
	}
	return GetSetupStatus200JSONResponse{
		IsConfigured: configured,
		Version:      version.Version,
	}, nil
}

func (h *AuthHandlers) CreateFirstAdmin(ctx context.Context, request CreateFirstAdminRequestObject) (CreateFirstAdminResponseObject, error) {
	alreadySetUp := CreateFirstAdmin409JSONResponse{
		ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: "already set up"},
	}
	// A fast path only: the service checks again under a lock (#375).
	configured, err := h.setup.Configured(ctx)
	if err != nil {
		return nil, err
	}
	if configured {
		return alreadySetUp, nil
	}
	if len(request.Body.Password) < minPasswordLength {
		return CreateFirstAdmin422JSONResponse{UnprocessableEntityJSONResponse: shortPassword()}, nil
	}

	user, err := h.setup.CreateFirstAdmin(ctx, request.Body.Name, string(request.Body.Email), request.Body.Password)
	if errors.Is(err, setup.ErrAlreadySetUp) {
		return alreadySetUp, nil
	}
	if err != nil {
		return nil, err
	}

	pair, err := h.sessions.Issue(ctx, user)
	if err != nil {
		return nil, err
	}

	h.ensurePersonalWorkspace(ctx, user.ID)

	return CreateFirstAdmin201JSONResponse{
		Token:        pair.AccessToken,
		RefreshToken: &pair.RefreshToken,
		ExpiresAt:    pair.ExpiresAt,
		User:         dbUserToPublic(user),
	}, nil
}

// ── Auth ──────────────────────────────────────────────────────────────────────

// minPasswordLength matches the OpenAPI minLength and the UI hint. It is
// enforced here because oapi-codegen does not validate request bodies.
const minPasswordLength = 8

func shortPassword() UnprocessableEntityJSONResponse {
	return unprocessable(ErrorCodeValidationError, "password must be at least 8 characters")
}

func (h *AuthHandlers) Login(ctx context.Context, request LoginRequestObject) (resp LoginResponseObject, err error) {
	kind := metrics.AuthPassword
	method := audit.AuthPassword
	if request.Body.ProviderId != nil {
		kind = metrics.AuthLDAP
		method = audit.AuthLDAP
	}
	defer func() { h.metrics.auth.Attempt(kind, authOutcome(resp, err)) }()

	if request.Body.ProviderId == nil && request.Body.Email == nil {
		return Login422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "email is required for local login")}, nil
	}

	identifier := loginIdentifier(request.Body)
	ev := audit.Event{Action: audit.AuthLogin, ResourceType: "user"}
	// A failed login is recorded by a fingerprint of the identifier, never the
	// e-mail/username itself or the password.
	failed := func(reason string) {
		actx := audit.WithAuthMethod(audit.WithAnonymous(ctx, audit.EmailFingerprint(identifier)), method)
		h.audited(actx, ev.Denied(reason))
	}
	// Progressive per-account delay (#51): repeated failures slow down, but a
	// cancelled request stops waiting and never hard-locks the account.
	if err := h.loginDelay().Wait(ctx, identifier); err != nil {
		return nil, err
	}

	if request.Body.ProviderId != nil {
		return h.loginLDAP(ctx, request.Body, ev, identifier, failed)
	}

	user, err := h.logins.Password(ctx, string(*request.Body.Email), request.Body.Password)
	switch {
	case errors.Is(err, login.ErrInvalidCredentials):
		h.loginDelay().Failure(identifier)
		failed("invalid_credentials")
		return invalidCredentialsLogin(), nil
	case errors.Is(err, login.ErrSuspended):
		// The password is correct, so revealing the suspension is safe (#283).
		failed("account_suspended")
		return Login403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{
			Code: ErrorCodeAccountSuspended, Message: "account suspended",
		}}, nil
	case err != nil:
		return nil, err
	}
	h.loginDelay().Success(identifier)
	return h.issueLogin(ctx, user, ev, audit.AuthPassword)
}

func invalidCredentialsLogin() LoginResponseObject {
	return Login401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeInvalidCredentials, Message: "invalid credentials"}}
}

// loginIdentifier keys the progressive delay and the audit fingerprint. LDAP
// usernames are namespaced so they never collide with local e-mails.
func loginIdentifier(body *LoginRequest) string {
	if body.ProviderId != nil {
		if body.Username != nil {
			return "ldap:" + strings.TrimSpace(*body.Username)
		}
		return "ldap:"
	}
	if body.Email != nil {
		return string(*body.Email)
	}
	return ""
}

// loginLDAP authenticates against an LDAP provider (#255). Unknown directory
// users are never auto-provisioned: the account must already be linked by its
// stable id (default deny). Every failure is the uniform
// invalid_credentials error.
func (h *AuthHandlers) loginLDAP(ctx context.Context, body *LoginRequest, ev audit.Event, identifier string, failed func(string)) (LoginResponseObject, error) {
	username := ""
	if body.Username != nil {
		username = strings.TrimSpace(*body.Username)
	}
	user, err := h.logins.Directory(ctx, pgtype.UUID{Bytes: *body.ProviderId, Valid: true}, username, body.Password)
	if errors.Is(err, login.ErrInvalidCredentials) {
		h.loginDelay().Failure(identifier)
		failed("invalid_credentials")
		return invalidCredentialsLogin(), nil
	}
	if err != nil {
		return nil, err
	}
	h.loginDelay().Success(identifier)
	return h.issueLogin(ctx, user, ev, audit.AuthLDAP)
}

// issueLogin mints the access and refresh tokens after the credential check
// succeeded, so local and directory logins behave identically.
func (h *AuthHandlers) issueLogin(ctx context.Context, user db.User, ev audit.Event, method string) (LoginResponseObject, error) {
	pair, err := h.sessions.Issue(ctx, user)
	if err != nil {
		return nil, err
	}

	h.ensurePersonalWorkspace(ctx, user.ID)
	h.audited(audit.AsUser(ctx, user.ID.String(), method), withID(ev, user.ID).Allowed())

	return Login200JSONResponse{
		Token:        pair.AccessToken,
		RefreshToken: &pair.RefreshToken,
		ExpiresAt:    pair.ExpiresAt,
		User:         dbUserToPublic(user),
	}, nil
}

func (h *AuthHandlers) Logout(ctx context.Context, request LogoutRequestObject) (LogoutResponseObject, error) {
	// Browsers send the refresh token in the HttpOnly cookie; API clients may
	// still send it in the body (#51).
	var bodyToken *string
	if request.Body != nil {
		bodyToken = request.Body.RefreshToken
	}
	token := refreshTokenFromBody(bodyToken, ctx)
	if token == "" {
		return Logout204Response{}, nil
	}
	// Logout ends the whole rotation family, not only this token.
	userID, found, err := h.sessions.End(ctx, token)
	if !found {
		return Logout204Response{}, nil
	}
	actx := audit.AsUser(ctx, idString(userID), audit.AuthRefreshTok)
	ev := audit.Event{Action: audit.AuthLogout, ResourceType: "user", ResourceID: idString(userID)}
	if err != nil {
		h.audited(actx, ev.Failed(audit.ReasonInternal))
		return nil, fmt.Errorf("logout: %w", err)
	}
	h.audited(actx, ev.Allowed())
	return Logout204Response{}, nil
}

func (h *AuthHandlers) GetMe(ctx context.Context, request GetMeRequestObject) (GetMeResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return GetMe401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	user, err := h.accounts.Get(ctx, uid)
	if err != nil {
		return GetMe401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "user not found"}}, nil
	}

	principal, _ := authz.PrincipalFromContext(ctx)
	me := Me{
		Id:            openapi_types.UUID(user.ID.Bytes),
		Name:          user.Name,
		Email:         openapi_types.Email(user.Email),
		Role:          MeRole(user.Role),
		AuthProvider:  MeAuthProvider(user.AuthProvider),
		Status:        MeStatus(user.Status),
		CreatedAt:     pgTimestampToTime(user.CreatedAt),
		Features:      h.features(ctx),
		GlobalActions: h.globalActions(ctx, principal),
	}
	if user.Locale != nil {
		locale := Locale(*user.Locale)
		me.Locale = &locale
	}
	if ws, ok := h.accounts.PersonalWorkspace(ctx, user.ID); ok {
		id := openapi_types.UUID(ws.Bytes)
		me.PersonalWorkspaceId = &id
	}
	return GetMe200JSONResponse(me), nil
}

// features are the navigation flags of /auth/me (#104). Agents exist when
// BALEIA_AGENTS_ENABLED is on; the assistant (#62) does not exist yet;
// retrievers exist when a Trino engine can host their destination (an enabled
// Iceberg catalog).
func (h *core) features(ctx context.Context) Features {
	f := Features{Agents: h.agentsEnabled()}
	cluster, err := h.queries.GetDefaultTrinoCluster(ctx)
	if err != nil || !cluster.EngineID.Valid {
		return f
	}
	if cats, err := h.queries.ListEnabledIcebergCatalogs(ctx); err == nil && len(cats) > 0 {
		f.Retrievers = true
	}
	return f
}

// globalActions are the actions outside any resource (#104).
func (h *core) globalActions(ctx context.Context, p authz.Principal) []GlobalAction {
	acts := []GlobalAction{CreateConnection, CreateWorkspace}
	if p.IsAdmin() {
		return append(acts, CreateSharedWorkspace, ManageUsers, ManageIdentity, ViewAllAudit, ViewSystem, ManageAssistant)
	}
	if owns, err := h.queries.UserOwnsTeam(ctx, p.UserID); err == nil && owns {
		acts = append(acts, CreateSharedWorkspace)
	}
	return acts
}

// ── OIDC ──────────────────────────────────────────────────────────────────────

// ListAuthProviders — GET /auth/providers (#289). Public, enabled providers
// only; it never returns configuration or secrets. It supersedes
// /auth/oidc/providers and adds LDAP.
func (h *AuthHandlers) ListAuthProviders(ctx context.Context, _ ListAuthProvidersRequestObject) (ListAuthProvidersResponseObject, error) {
	providers, err := h.queries.ListAuthProviders(ctx)
	if err != nil {
		return nil, err
	}

	result := make(ListAuthProviders200JSONResponse, 0, len(providers))
	for _, p := range providers {
		result = append(result, AuthProvider{
			Id:   openapi_types.UUID(p.ID.Bytes),
			Name: p.Name,
			Type: AuthProviderType(p.Type),
		})
	}
	return result, nil
}

func (h *AuthHandlers) ListOIDCProviders(ctx context.Context, request ListOIDCProvidersRequestObject) (ListOIDCProvidersResponseObject, error) {
	providers, err := h.queries.ListAuthProviders(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]OIDCProvider, 0, len(providers))
	for _, p := range providers {
		id := openapi_types.UUID(p.ID.Bytes)
		result = append(result, OIDCProvider{
			Id:   id,
			Name: p.Name,
			Type: OIDCProviderType(p.Type),
		})
	}
	return ListOIDCProviders200JSONResponse(result), nil
}

func (h *AuthHandlers) OidcRedirect(ctx context.Context, request OidcRedirectRequestObject) (OidcRedirectResponseObject, error) {
	var redirect *string
	if request.Params.Next != nil && safeRelativePath(*request.Params.Next) {
		redirect = request.Params.Next
	}
	authURL, state, err := h.oidc.Begin(ctx, pgtype.UUID{Bytes: request.ProviderId, Valid: true}, redirect)
	if errors.Is(err, oidclogin.ErrUnavailable) {
		return OidcRedirect503JSONResponse{ServiceUnavailableJSONResponse: ServiceUnavailableJSONResponse{
			Code: ErrorCodeIdpUnavailable, Message: "identity provider unavailable",
		}}, nil
	}
	if err != nil {
		return nil, err
	}
	res := OIDCResultFromContext(ctx)
	if res == nil {
		return nil, errors.New("oidc: missing response result")
	}
	res.Location = authURL
	res.OIDCCookie = &state
	return OidcRedirect302Response{}, nil
}

func (h *AuthHandlers) OidcCallback(ctx context.Context, request OidcCallbackRequestObject) (resp OidcCallbackResponseObject, err error) {
	outcome := metrics.OutcomeSuccess
	defer func() { h.metrics.auth.Attempt(metrics.AuthOIDC, outcome) }()
	res := OIDCResultFromContext(ctx)
	if res == nil {
		return nil, errors.New("oidc: missing response result")
	}
	fail := func(code, metric string) OidcCallbackResponseObject {
		outcome = metric
		res.Location = oidcFailureLocation(code)
		empty := ""
		res.OIDCCookie = &empty
		return OidcCallback302Response{}
	}
	denied := func(reason string) {
		h.audited(audit.WithAuthMethod(ctx, audit.AuthOIDC), audit.Event{Action: audit.AuthLogin, ResourceType: "user", Outcome: audit.Denied, Reason: reason})
	}

	user, redirect, err := h.oidc.Finish(ctx, pgtype.UUID{Bytes: request.ProviderId, Valid: true},
		request.Params.State, auth.OIDCCookieFromContext(ctx), request.Params.Code)
	switch {
	case errors.Is(err, oidclogin.ErrUnavailable):
		outcome = metrics.OutcomeError
		return OidcCallback503JSONResponse{ServiceUnavailableJSONResponse: ServiceUnavailableJSONResponse{
			Code: ErrorCodeIdpUnavailable, Message: "identity provider unavailable",
		}}, nil
	case errors.Is(err, oidclogin.ErrStateMismatch):
		return fail(oidcErrorSSOFailed, metrics.OutcomeInvalidCredentials), nil
	case errors.Is(err, oidclogin.ErrInvalidState):
		denied("invalid_state")
		return fail(oidcErrorSSOFailed, metrics.OutcomeInvalidCredentials), nil
	case errors.Is(err, oidclogin.ErrExchange):
		denied("oidc_exchange_failed")
		return fail(oidcErrorSSOFailed, metrics.OutcomeInvalidCredentials), nil
	case errors.Is(err, provision.ErrNotProvisioned):
		return fail(oidcErrorNotProvisioned, metrics.OutcomeInvalidCredentials), nil
	case errors.Is(err, oidclogin.ErrSuspended):
		// OIDC reports suspension explicitly on the login page (#284).
		h.audited(audit.WithAuthMethod(ctx, audit.AuthOIDC), audit.Event{
			Action: audit.AuthLogin, ResourceType: "user", ResourceID: idString(user.ID),
			Outcome: audit.Denied, Reason: "account_suspended",
		})
		return fail(oidcErrorAccountSuspended, metrics.OutcomeInvalidCredentials), nil
	case err != nil:
		return fail(oidcErrorSSOFailed, metrics.OutcomeError), nil
	}

	refreshToken, err := h.sessions.IssueRefresh(ctx, user)
	if err != nil {
		return fail(oidcErrorSSOFailed, metrics.OutcomeError), nil
	}

	empty := ""
	res.OIDCCookie = &empty
	res.RefreshCookie = &refreshToken
	res.Location = oidcRedirectTarget(redirect)
	h.audited(audit.AsUser(ctx, user.ID.String(), audit.AuthOIDC), audit.Event{Action: audit.AuthLogin, ResourceType: "user", ResourceID: idString(user.ID), Outcome: audit.Allowed})
	return OidcCallback302Response{}, nil
}

// ── Refresh Token ─────────────────────────────────────────────────────────────

func (h *AuthHandlers) RefreshToken(ctx context.Context, request RefreshTokenRequestObject) (resp RefreshTokenResponseObject, err error) {
	defer func() { h.metrics.auth.Attempt(metrics.AuthRefresh, authOutcome(resp, err)) }()

	// Browsers rely on the HttpOnly cookie; API clients may send the token in
	// the body (#51).
	var bodyToken *string
	if request.Body != nil {
		bodyToken = request.Body.RefreshToken
	}
	token := refreshTokenFromBody(bodyToken, ctx)
	if token == "" {
		return RefreshToken401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "missing refresh token"},
		}, nil
	}

	ev := audit.Event{Action: audit.AuthRefresh, ResourceType: "user"}
	actx := audit.WithAuthMethod(ctx, audit.AuthRefreshTok)
	pair, user, err := h.sessions.Rotate(ctx, token)
	switch {
	case errors.Is(err, session.ErrReuseNotContained):
		// The family may be stolen: a failed revoke is a server error, never
		// a quiet 401 that leaves it usable (#370).
		h.audited(actx, ev.Failed(audit.ReasonReuseDetected))
		return nil, fmt.Errorf("refresh: %w", err)
	case errors.Is(err, session.ErrReused):
		h.audited(actx, ev.Denied(audit.ReasonReuseDetected))
		return RefreshToken401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid or expired refresh token"},
		}, nil
	case errors.Is(err, session.ErrInvalid):
		h.audited(actx, ev.Denied("invalid_token"))
		return RefreshToken401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid or expired refresh token"},
		}, nil
	case errors.Is(err, session.ErrUserNotFound):
		return RefreshToken401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "user not found"},
		}, nil
	case errors.Is(err, session.ErrSuspended):
		h.audited(actx, ev.Denied("account_suspended"))
		return RefreshToken401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeAccountSuspended, Message: "account suspended"},
		}, nil
	case err != nil:
		return nil, err
	}

	h.audited(audit.AsUser(ctx, user.ID.String(), audit.AuthRefreshTok), withID(ev, user.ID).Allowed())
	return RefreshToken200JSONResponse{
		Token:        pair.AccessToken,
		RefreshToken: &pair.RefreshToken,
		ExpiresAt:    pair.ExpiresAt,
	}, nil
}

// refreshTokenFromBody prefers the body token (API clients) and falls back to
// the HttpOnly cookie (browsers, #51).
func refreshTokenFromBody(bodyToken *string, ctx context.Context) string {
	if bodyToken != nil && *bodyToken != "" {
		return *bodyToken
	}
	return auth.RefreshCookieFromContext(ctx)
}

// ── Password Reset ────────────────────────────────────────────────────────────

func (h *AuthHandlers) RequestPasswordReset(ctx context.Context, request RequestPasswordResetRequestObject) (RequestPasswordResetResponseObject, error) {

	if h.settings.Mail.SMTPHost == "" || h.settings.Mail.SMTPPort == "" {
		return RequestPasswordReset503JSONResponse{
			Code:    ErrorCodeServiceUnavailable,
			Message: "email service not configured",
		}, nil
	}

	// Recorded the same whether the e-mail exists or not (the answer does
	// not reveal it either), by the e-mail's fingerprint.
	h.audited(audit.WithAnonymous(ctx, audit.EmailFingerprint(string(request.Body.Email))),
		audit.Event{Action: audit.AuthPasswordResetRequest, ResourceType: "user", Outcome: audit.Allowed})
	if err := h.resets.Request(ctx, string(request.Body.Email)); err != nil {
		return nil, err
	}
	return RequestPasswordReset204Response{}, nil
}

func (h *AuthHandlers) ConfirmPasswordReset(ctx context.Context, request ConfirmPasswordResetRequestObject) (ConfirmPasswordResetResponseObject, error) {
	ev := audit.Event{Action: audit.AuthPasswordResetConfirm, ResourceType: "user"}
	// Validate before consuming: a short password must not burn the link.
	if len(request.Body.NewPassword) < minPasswordLength {
		return ConfirmPasswordReset422JSONResponse{UnprocessableEntityJSONResponse: shortPassword()}, nil
	}
	userID, err := h.resets.Confirm(ctx, request.Body.Token, request.Body.NewPassword)
	if errors.Is(err, passwordreset.ErrInvalidToken) {
		h.audited(ctx, ev.Denied("invalid_token"))
		return ConfirmPasswordReset401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid or expired token"},
		}, nil
	}
	if err != nil {
		return nil, err
	}

	h.audited(audit.AsUser(ctx, userID.String(), audit.AuthPassword), withID(ev, userID).Allowed())
	return ConfirmPasswordReset204Response{}, nil
}

// ── Profile ───────────────────────────────────────────────────────────────────

func (h *AuthHandlers) GetProfile(ctx context.Context, request GetProfileRequestObject) (GetProfileResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return GetProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	user, err := h.accounts.Get(ctx, uid)
	if err != nil {
		return GetProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "user not found"}}, nil
	}

	return GetProfile200JSONResponse(dbUserToPublic(user)), nil
}

func (h *AuthHandlers) UpdateProfile(ctx context.Context, request UpdateProfileRequestObject) (UpdateProfileResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return UpdateProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	change := account.Profile{Name: request.Body.Name}
	if request.Body.Email != nil {
		email := string(*request.Body.Email)
		change.Email = &email
	}
	if request.Body.Locale != nil {
		// The generated enum type does not validate on decode; reject anything
		// outside the supported locales with a translatable field code (#67).
		switch *request.Body.Locale {
		case LocaleEn, LocalePtBR:
			value := string(*request.Body.Locale)
			change.Locale = &value
		default:
			return UpdateProfile422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
				"locale must be en or pt-BR",
				FieldError{Field: "locale", Code: FieldCodeNotAllowed},
			)}, nil
		}
	}

	updated, err := h.accounts.UpdateProfile(ctx, uid, change)
	if errors.Is(err, account.ErrNotFound) {
		return UpdateProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "user not found"}}, nil
	}
	if err != nil {
		return nil, err
	}

	return UpdateProfile200JSONResponse(dbUserToPublic(updated)), nil
}

func (h *AuthHandlers) UpdatePassword(ctx context.Context, request UpdatePasswordRequestObject) (UpdatePasswordResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return UpdatePassword401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	// A password change ends the user's other sessions (#51).
	switch err := h.accounts.ChangePassword(ctx, uid, request.Body.CurrentPassword, request.Body.NewPassword); {
	case errors.Is(err, account.ErrNotFound):
		return UpdatePassword401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "user not found"}}, nil
	case errors.Is(err, account.ErrWrongPassword):
		return UpdatePassword422JSONResponse{
			UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "current password is incorrect"),
		}, nil
	case errors.Is(err, account.ErrPasswordTooShort):
		return UpdatePassword422JSONResponse{UnprocessableEntityJSONResponse: shortPassword()}, nil
	case err != nil:
		return nil, err
	}

	return UpdatePassword204Response{}, nil
}

// ── OIDC manual (rotas com cookie + redirect) ─────────────────────────────────

func safeRelativePath(next string) bool {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return false
	}
	return !strings.ContainsAny(next, "\\\r\n\t\x00")
}

// OIDC failure codes land on /login?error=<code> (#252). not_provisioned and
// account_suspended are emitted by the provisioning and suspension slices.
const (
	oidcErrorNotProvisioned   = "not_provisioned"
	oidcErrorAccountSuspended = "account_suspended"
	oidcErrorIDPUnavailable   = "idp_unavailable"
	oidcErrorSSOFailed        = "sso_failed"
)

func oidcFailureLocation(code string) string {
	// Keep the vocabulary bounded; an unknown code is a generic SSO failure.
	switch code {
	case oidcErrorNotProvisioned, oidcErrorAccountSuspended, oidcErrorIDPUnavailable, oidcErrorSSOFailed:
	default:
		code = oidcErrorSSOFailed
	}
	return "/login?error=" + code
}

// oidcRedirectTarget honours only a stored same-origin path (#252).
func oidcRedirectTarget(redirect *string) string {
	if redirect != nil && safeRelativePath(*redirect) {
		return *redirect
	}
	return "/"
}

// authOutcome classifies a strict auth handler's result for
// baleia_auth_attempts_total.
func authOutcome(resp any, err error) string {
	switch {
	case err != nil:
		return metrics.OutcomeError
	case resp == nil:
		return metrics.OutcomeError
	}
	switch resp.(type) {
	case Login200JSONResponse, RefreshToken200JSONResponse:
		return metrics.OutcomeSuccess
	}
	return metrics.OutcomeInvalidCredentials
}
