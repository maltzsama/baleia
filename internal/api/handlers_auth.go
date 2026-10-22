package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/galileostudio/baleia/internal/mail"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/version"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// newTokenFamily returns a fresh refresh-token family id (#51). Every session
// starts a new family; rotated tokens inherit it.
func newTokenFamily() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

// ── Setup ─────────────────────────────────────────────────────────────────────

// errAlreadySetUp aborts a first-admin setup that lost the race.
var errAlreadySetUp = errors.New("already set up")

func (h *Handlers) GetSetupStatus(ctx context.Context, request GetSetupStatusRequestObject) (GetSetupStatusResponseObject, error) {
	configured, err := h.queries.HasUsers(ctx)
	if err != nil {
		return nil, err
	}
	return GetSetupStatus200JSONResponse{
		IsConfigured: configured,
		Version:      version.Version,
	}, nil
}

func (h *Handlers) CreateFirstAdmin(ctx context.Context, request CreateFirstAdminRequestObject) (CreateFirstAdminResponseObject, error) {
	alreadySetUp := CreateFirstAdmin409JSONResponse{
		ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: "already set up"},
	}
	configured, err := h.queries.HasUsers(ctx)
	if err != nil {
		return nil, err
	}
	if configured {
		return alreadySetUp, nil
	}
	if len(request.Body.Password) < minPasswordLength {
		return CreateFirstAdmin422JSONResponse{UnprocessableEntityJSONResponse: shortPassword()}, nil
	}

	hash, err := auth.HashPasswordWithCost(request.Body.Password, h.settings.Auth.PasswordCost)
	if err != nil {
		return nil, err
	}

	// The check above is only a fast path: two concurrent setups both pass
	// it, so the lock and a second check make exactly one of them win (#375).
	var user db.User
	err = h.inTx(ctx, func(q db.Querier) error {
		if err := q.LockFirstAdminSetup(ctx); err != nil {
			return err
		}
		if configured, err := q.HasUsers(ctx); err != nil {
			return err
		} else if configured {
			return errAlreadySetUp
		}
		user, err = q.CreateUser(ctx, db.CreateUserParams{
			Name:         request.Body.Name,
			Email:        string(request.Body.Email),
			PasswordHash: &hash,
			Role:         "admin",
			AuthProvider: "local",
		})
		return err
	})
	if errors.Is(err, errAlreadySetUp) {
		return alreadySetUp, nil
	}
	if err != nil {
		return nil, err
	}

	accessToken, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, h.settings.Auth.JWTSecret, h.settings.Auth.AccessTTL)
	if err != nil {
		return nil, err
	}

	refreshToken, refreshHash, err := auth.GenerateRefreshToken()
	if err != nil {
		return nil, err
	}

	if _, err := h.queries.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(h.settings.Auth.RefreshTTL), Valid: true},
		FamilyID:  newTokenFamily(),
	}); err != nil {
		return nil, err
	}

	h.ensurePersonalWorkspace(ctx, user.ID)

	return CreateFirstAdmin201JSONResponse{
		Token:        accessToken,
		RefreshToken: &refreshToken,
		ExpiresAt:    time.Now().Add(h.settings.Auth.AccessTTL),
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

func (h *Handlers) Login(ctx context.Context, request LoginRequestObject) (resp LoginResponseObject, err error) {
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

	email := string(*request.Body.Email)

	user, err := h.queries.GetUserByEmail(ctx, email)
	if err != nil {
		// Unknown e-mail: compare against a dummy hash anyway so the response
		// time does not reveal whether the account exists (#51).
		_ = auth.CheckPassword(h.dummyPasswordHash(), request.Body.Password)
		h.loginDelay().Failure(identifier)
		failed("invalid_credentials")
		return invalidCredentialsLogin(), nil
	}

	// An SSO-only account and a wrong password are indistinguishable (#51).
	if user.AuthProvider != "local" || user.PasswordHash == nil {
		_ = auth.CheckPassword(h.dummyPasswordHash(), request.Body.Password)
		h.loginDelay().Failure(identifier)
		failed("invalid_credentials")
		return invalidCredentialsLogin(), nil
	}

	if !auth.CheckPassword(*user.PasswordHash, request.Body.Password) {
		h.loginDelay().Failure(identifier)
		failed("invalid_credentials")
		return invalidCredentialsLogin(), nil
	}
	// The password is correct, so revealing the suspension is safe (#283).
	if err := auth.RequireActive(user.Status); err != nil {
		failed("account_suspended")
		return Login403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{
			Code: ErrorCodeAccountSuspended, Message: "account suspended",
		}}, nil
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
func (h *Handlers) loginLDAP(ctx context.Context, body *LoginRequest, ev audit.Event, identifier string, failed func(string)) (LoginResponseObject, error) {
	invalid := func() (LoginResponseObject, error) {
		h.loginDelay().Failure(identifier)
		failed("invalid_credentials")
		return invalidCredentialsLogin(), nil
	}

	username := ""
	if body.Username != nil {
		username = strings.TrimSpace(*body.Username)
	}
	if username == "" || body.Password == "" {
		return invalid()
	}

	apID := pgtype.UUID{Bytes: *body.ProviderId, Valid: true}
	ap, err := h.queries.GetAuthProviderByID(ctx, apID)
	if err != nil || ap.Type != "ldap" || !ap.Enabled {
		return invalid()
	}
	cfg, err := identityldap.OpenConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
	if err != nil {
		// Fail closed: an unreadable config never falls back to another method.
		return invalid()
	}

	authenticate := h.ldapAuthenticate
	if authenticate == nil {
		authenticate = identityldap.Authenticate
	}
	result, err := authenticate(ctx, cfg, username, body.Password)
	if err != nil {
		return invalid()
	}

	user, err := h.queries.GetUserByExternalID(ctx, db.GetUserByExternalIDParams{
		AuthProviderID: ap.ID,
		ExternalID:     &result.ExternalID,
	})
	if err != nil || user.AuthProvider != "ldap" {
		return invalid()
	}
	// A suspended directory account is indistinguishable from bad credentials
	// (#284): never reveal the suspension to an unauthenticated caller.
	if err := auth.RequireActive(user.Status); err != nil {
		return invalid()
	}
	if result.Role != user.Role {
		if err := h.queries.UpdateUserRole(ctx, db.UpdateUserRoleParams{ID: user.ID, Role: result.Role}); err != nil {
			return nil, err
		}
		user.Role = result.Role
	}
	h.applyTeams(ctx, user.ID, result.Teams)
	h.loginDelay().Success(identifier)
	return h.issueLogin(ctx, user, ev, audit.AuthLDAP)
}

// issueLogin mints the access and refresh tokens after the credential check
// succeeded, so local and directory logins behave identically.
func (h *Handlers) issueLogin(ctx context.Context, user db.User, ev audit.Event, method string) (LoginResponseObject, error) {
	accessToken, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, h.settings.Auth.JWTSecret, h.settings.Auth.AccessTTL)
	if err != nil {
		return nil, err
	}

	refreshToken, refreshHash, err := auth.GenerateRefreshToken()
	if err != nil {
		return nil, err
	}

	if _, err := h.queries.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(h.settings.Auth.RefreshTTL), Valid: true},
		FamilyID:  newTokenFamily(),
	}); err != nil {
		return nil, err
	}

	h.ensurePersonalWorkspace(ctx, user.ID)
	h.audited(audit.AsUser(ctx, user.ID.String(), method), withID(ev, user.ID).Allowed())

	return Login200JSONResponse{
		Token:        accessToken,
		RefreshToken: &refreshToken,
		ExpiresAt:    time.Now().Add(h.settings.Auth.AccessTTL),
		User:         dbUserToPublic(user),
	}, nil
}

func (h *Handlers) Logout(ctx context.Context, request LogoutRequestObject) (LogoutResponseObject, error) {
	// Browsers send the refresh token in the HttpOnly cookie; API clients may
	// still send it in the body (#51).
	var bodyToken *string
	if request.Body != nil {
		bodyToken = request.Body.RefreshToken
	}
	token := refreshTokenFromBody(bodyToken, ctx)
	if token != "" {
		hash := auth.HashRefreshToken(token)
		if rt, err := h.queries.GetRefreshTokenByHashAny(ctx, hash); err == nil {
			// Logout ends the whole rotation family, not only this token. A
			// client disconnect must not cancel the revoke halfway (#370).
			actx := audit.AsUser(ctx, idString(rt.UserID), audit.AuthRefreshTok)
			ev := audit.Event{Action: audit.AuthLogout, ResourceType: "user", ResourceID: idString(rt.UserID)}
			if err := h.queries.RevokeRefreshFamily(context.WithoutCancel(ctx), rt.FamilyID); err != nil {
				h.audited(actx, ev.Failed(audit.ReasonInternal))
				return nil, fmt.Errorf("logout: revoke refresh family: %w", err)
			}
			h.audited(actx, ev.Allowed())
		}
	}
	return Logout204Response{}, nil
}

func (h *Handlers) GetMe(ctx context.Context, request GetMeRequestObject) (GetMeResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return GetMe401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	user, err := h.queries.GetUserByID(ctx, uid)
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
	if ws, err := h.queries.PersonalWorkspaceID(ctx, user.ID); err == nil {
		id := openapi_types.UUID(ws.Bytes)
		me.PersonalWorkspaceId = &id
	}
	return GetMe200JSONResponse(me), nil
}

// features are the navigation flags of /auth/me (#104). Agents exist when
// BALEIA_AGENTS_ENABLED is on; the assistant (#62) does not exist yet;
// retrievers exist when a Trino engine can host their destination (an enabled
// Iceberg catalog).
func (h *Handlers) features(ctx context.Context) Features {
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
func (h *Handlers) globalActions(ctx context.Context, p authz.Principal) []GlobalAction {
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
func (h *Handlers) ListAuthProviders(ctx context.Context, _ ListAuthProvidersRequestObject) (ListAuthProvidersResponseObject, error) {
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

func (h *Handlers) ListOIDCProviders(ctx context.Context, request ListOIDCProvidersRequestObject) (ListOIDCProvidersResponseObject, error) {
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

func (h *Handlers) OidcRedirect(ctx context.Context, request OidcRedirectRequestObject) (OidcRedirectResponseObject, error) {
	providerID := request.ProviderId.String()
	provider, _, err := h.cachedOIDCProvider(ctx, providerID)
	if err != nil {
		return OidcRedirect503JSONResponse{ServiceUnavailableJSONResponse: ServiceUnavailableJSONResponse{
			Code: ErrorCodeIdpUnavailable, Message: "identity provider unavailable",
		}}, nil
	}

	verifier, challenge, err := auth.GeneratePKCE()
	if err != nil {
		return nil, err
	}
	nonce, err := auth.GenerateNonce()
	if err != nil {
		return nil, err
	}
	state := randomState()

	var redirect *string
	if request.Params.Next != nil && safeRelativePath(*request.Params.Next) {
		redirect = request.Params.Next
	}
	apID := pgtype.UUID{Bytes: request.ProviderId, Valid: true}
	if err := h.queries.CreateOIDCLoginAttempt(ctx, db.CreateOIDCLoginAttemptParams{
		State:        state,
		ProviderID:   apID,
		Nonce:        &nonce,
		CodeVerifier: verifier,
		Redirect:     redirect,
		ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(auth.OIDCAttemptTTL), Valid: true},
	}); err != nil {
		return nil, err
	}

	authURL, err := provider.AuthURL(state, challenge, nonce)
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

func (h *Handlers) OidcCallback(ctx context.Context, request OidcCallbackRequestObject) (resp OidcCallbackResponseObject, err error) {
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

	providerID := request.ProviderId.String()
	provider, cfg, err := h.cachedOIDCProvider(ctx, providerID)
	if err != nil {
		outcome = metrics.OutcomeError
		return OidcCallback503JSONResponse{ServiceUnavailableJSONResponse: ServiceUnavailableJSONResponse{
			Code: ErrorCodeIdpUnavailable, Message: "identity provider unavailable",
		}}, nil
	}

	state := request.Params.State
	if state == "" || auth.OIDCCookieFromContext(ctx) != state {
		return fail(oidcErrorSSOFailed, metrics.OutcomeInvalidCredentials), nil
	}

	attempt, err := h.queries.ConsumeOIDCLoginAttempt(ctx, state)
	if err != nil {
		h.audited(audit.WithAuthMethod(ctx, audit.AuthOIDC), audit.Event{Action: audit.AuthLogin, ResourceType: "user", Outcome: audit.Denied, Reason: "invalid_state"})
		return fail(oidcErrorSSOFailed, metrics.OutcomeInvalidCredentials), nil
	}

	apID := pgtype.UUID{Bytes: request.ProviderId, Valid: true}
	if apID != attempt.ProviderID {
		return fail(oidcErrorSSOFailed, metrics.OutcomeInvalidCredentials), nil
	}

	nonce := ""
	if attempt.Nonce != nil {
		nonce = *attempt.Nonce
	}
	info, err := provider.Exchange(ctx, request.Params.Code, attempt.CodeVerifier, nonce)
	if err != nil {
		h.audited(audit.WithAuthMethod(ctx, audit.AuthOIDC), audit.Event{Action: audit.AuthLogin, ResourceType: "user", Outcome: audit.Denied, Reason: "oidc_exchange_failed"})
		return fail(oidcErrorSSOFailed, metrics.OutcomeInvalidCredentials), nil
	}

	user, err := h.provisionOIDCUser(ctx, apID, cfg, info)
	if err != nil {
		if errors.Is(err, errOIDCNotProvisioned) {
			return fail(oidcErrorNotProvisioned, metrics.OutcomeInvalidCredentials), nil
		}
		return fail(oidcErrorSSOFailed, metrics.OutcomeError), nil
	}
	// OIDC reports suspension explicitly on the login page (#284).
	if err := auth.RequireActive(user.Status); err != nil {
		h.audited(audit.WithAuthMethod(ctx, audit.AuthOIDC), audit.Event{
			Action: audit.AuthLogin, ResourceType: "user", ResourceID: idString(user.ID),
			Outcome: audit.Denied, Reason: "account_suspended",
		})
		return fail(oidcErrorAccountSuspended, metrics.OutcomeInvalidCredentials), nil
	}

	refreshToken, refreshHash, err := auth.GenerateRefreshToken()
	if err != nil {
		return fail(oidcErrorSSOFailed, metrics.OutcomeError), nil
	}
	if _, err := h.queries.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(h.settings.Auth.RefreshTTL), Valid: true},
		FamilyID:  newTokenFamily(),
	}); err != nil {
		return fail(oidcErrorSSOFailed, metrics.OutcomeError), nil
	}

	empty := ""
	res.OIDCCookie = &empty
	res.RefreshCookie = &refreshToken
	res.Location = oidcRedirectTarget(attempt.Redirect)
	h.audited(audit.AsUser(ctx, user.ID.String(), audit.AuthOIDC), audit.Event{Action: audit.AuthLogin, ResourceType: "user", ResourceID: idString(user.ID), Outcome: audit.Allowed})
	return OidcCallback302Response{}, nil
}

// ── Refresh Token ─────────────────────────────────────────────────────────────

func (h *Handlers) RefreshToken(ctx context.Context, request RefreshTokenRequestObject) (resp RefreshTokenResponseObject, err error) {
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
	hash := auth.HashRefreshToken(token)

	ev := audit.Event{Action: audit.AuthRefresh, ResourceType: "user"}
	// Atomic rotation: of two concurrent refreshes with the same token,
	// exactly one updates the row (#51).
	rt, err := h.queries.ConsumeRefreshToken(ctx, hash)
	if err != nil {
		// A revoked row with this hash means a rotated token was replayed:
		// revoke the whole family, not just this request (#51).
		reused := false
		if errors.Is(err, pgx.ErrNoRows) {
			if old, lookupErr := h.queries.GetRefreshTokenByHashAny(ctx, hash); lookupErr == nil && old.Revoked {
				// The family may be stolen: a failed revoke is a server
				// error, never a quiet 401 that leaves it usable (#370).
				if revokeErr := h.queries.RevokeRefreshFamily(context.WithoutCancel(ctx), old.FamilyID); revokeErr != nil {
					h.audited(audit.WithAuthMethod(ctx, audit.AuthRefreshTok), ev.Failed(audit.ReasonReuseDetected))
					return nil, fmt.Errorf("refresh: revoke reused family: %w", revokeErr)
				}
				h.audited(audit.WithAuthMethod(ctx, audit.AuthRefreshTok), ev.Denied(audit.ReasonReuseDetected))
				reused = true
			}
		}
		if !reused {
			h.audited(audit.WithAuthMethod(ctx, audit.AuthRefreshTok), ev.Denied("invalid_token"))
		}
		return RefreshToken401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid or expired refresh token"},
		}, nil
	}

	user, err := h.queries.GetUserByID(ctx, rt.UserID)
	if err != nil {
		return RefreshToken401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "user not found"},
		}, nil
	}
	// A suspension ends every session and blocks a refresh even if a token
	// survived (#283).
	if err := auth.RequireActive(user.Status); err != nil {
		h.revokeUserRefreshTokens(ctx, user.ID)
		h.audited(audit.WithAuthMethod(ctx, audit.AuthRefreshTok), ev.Denied("account_suspended"))
		return RefreshToken401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeAccountSuspended, Message: "account suspended"},
		}, nil
	}

	accessToken, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, h.settings.Auth.JWTSecret, h.settings.Auth.AccessTTL)
	if err != nil {
		return nil, err
	}

	newRefreshToken, newHash, err := auth.GenerateRefreshToken()
	if err != nil {
		return nil, err
	}

	if _, err := h.queries.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: newHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(h.settings.Auth.RefreshTTL), Valid: true},
		FamilyID:  rt.FamilyID,
	}); err != nil {
		return nil, err
	}

	h.audited(audit.AsUser(ctx, user.ID.String(), audit.AuthRefreshTok), withID(ev, user.ID).Allowed())
	return RefreshToken200JSONResponse{
		Token:        accessToken,
		RefreshToken: &newRefreshToken,
		ExpiresAt:    time.Now().Add(h.settings.Auth.AccessTTL),
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

func (h *Handlers) RequestPasswordReset(ctx context.Context, request RequestPasswordResetRequestObject) (RequestPasswordResetResponseObject, error) {

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
	user, err := h.queries.GetUserByEmail(ctx, string(request.Body.Email))
	if err != nil {
		return RequestPasswordReset204Response{}, nil
	}

	token := randomState()
	expires := time.Now().Add(h.settings.Auth.ResetTTL)

	// Only the hash is stored; the raw token travels in the e-mail link (#51).
	if err := h.queries.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		TokenHash: auth.HashToken(token),
		UserID:    user.ID,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("create password reset token: %w", err)
	}

	resetURL := h.settings.Mail.PublicURL + "/reset-password?token=" + token
	locale := mail.LocaleOrDefault(user.Locale, h.settings.Mail.DefaultLocale)
	if err := h.mailSender().Send(ctx, user.Email, locale, mail.TemplatePasswordReset, map[string]any{
		"Name":     user.Name,
		"ResetURL": resetURL,
	}); err != nil {
		return nil, fmt.Errorf("send password reset e-mail: %w", err)
	}

	return RequestPasswordReset204Response{}, nil
}

func (h *Handlers) ConfirmPasswordReset(ctx context.Context, request ConfirmPasswordResetRequestObject) (ConfirmPasswordResetResponseObject, error) {
	ev := audit.Event{Action: audit.AuthPasswordResetConfirm, ResourceType: "user"}
	// Validate before consuming: a short password must not burn the link.
	if len(request.Body.NewPassword) < minPasswordLength {
		return ConfirmPasswordReset422JSONResponse{UnprocessableEntityJSONResponse: shortPassword()}, nil
	}
	// Consuming the hash makes the link single use even before it expires (#51).
	row, err := h.queries.ConsumePasswordResetToken(ctx, auth.HashToken(request.Body.Token))
	if err != nil {
		h.audited(ctx, ev.Denied("invalid_token"))
		return ConfirmPasswordReset401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid or expired token"},
		}, nil
	}

	hash, err := auth.HashPasswordWithCost(request.Body.NewPassword, h.settings.Auth.PasswordCost)
	if err != nil {
		return nil, err
	}

	if err := h.queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		ID:           row.UserID,
		PasswordHash: &hash,
	}); err != nil {
		return nil, err
	}

	// A reset ends every existing session (#51).
	h.revokeUserRefreshTokens(ctx, row.UserID)

	h.audited(audit.AsUser(ctx, row.UserID.String(), audit.AuthPassword), withID(ev, row.UserID).Allowed())
	return ConfirmPasswordReset204Response{}, nil
}

// revokeUserRefreshTokens ends every refresh token of a user after a lifecycle
// event (password change/reset, role change, disable). Access tokens expire
// within the configured access TTL. A failure is logged, not fatal: the
// triggering change already succeeded.
func (h *Handlers) revokeUserRefreshTokens(ctx context.Context, userID pgtype.UUID) {
	if err := h.queries.RevokeUserRefreshTokens(ctx, userID); err != nil {
		slog.Error("RevokeUserRefreshTokens failed", "user_id", idString(userID), "err", err)
	}
}

// ── Profile ───────────────────────────────────────────────────────────────────

func (h *Handlers) GetProfile(ctx context.Context, request GetProfileRequestObject) (GetProfileResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return GetProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	user, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		return GetProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "user not found"}}, nil
	}

	return GetProfile200JSONResponse(dbUserToPublic(user)), nil
}

func (h *Handlers) UpdateProfile(ctx context.Context, request UpdateProfileRequestObject) (UpdateProfileResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return UpdateProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	current, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		return UpdateProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "user not found"}}, nil
	}

	name := current.Name
	email := current.Email
	if request.Body.Name != nil {
		name = *request.Body.Name
	}
	if request.Body.Email != nil {
		email = string(*request.Body.Email)
	}
	locale := current.Locale
	if request.Body.Locale != nil {
		// The generated enum type does not validate on decode; reject anything
		// outside the supported locales with a translatable field code (#67).
		switch *request.Body.Locale {
		case LocaleEn, LocalePtBR:
			value := string(*request.Body.Locale)
			locale = &value
		default:
			return UpdateProfile422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
				"locale must be en or pt-BR",
				FieldError{Field: "locale", Code: FieldCodeNotAllowed},
			)}, nil
		}
	}

	updated, err := h.queries.UpdateUser(ctx, db.UpdateUserParams{
		ID:     uid,
		Name:   name,
		Email:  email,
		Locale: locale,
	})
	if err != nil {
		return nil, err
	}

	return UpdateProfile200JSONResponse(dbUserToPublic(updated)), nil
}

func (h *Handlers) UpdatePassword(ctx context.Context, request UpdatePasswordRequestObject) (UpdatePasswordResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return UpdatePassword401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}

	user, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		return UpdatePassword401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "user not found"}}, nil
	}

	if user.PasswordHash == nil || !auth.CheckPassword(*user.PasswordHash, request.Body.CurrentPassword) {
		return UpdatePassword422JSONResponse{
			UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "current password is incorrect"),
		}, nil
	}
	if len(request.Body.NewPassword) < minPasswordLength {
		return UpdatePassword422JSONResponse{UnprocessableEntityJSONResponse: shortPassword()}, nil
	}

	hash, err := auth.HashPasswordWithCost(request.Body.NewPassword, h.settings.Auth.PasswordCost)
	if err != nil {
		return nil, err
	}

	if err := h.queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		ID:           uid,
		PasswordHash: &hash,
	}); err != nil {
		return nil, err
	}

	// A password change ends the user's other sessions (#51).
	h.revokeUserRefreshTokens(ctx, uid)

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

// oidcCacheEntry is one discovered provider, keyed by id + config version.
type oidcCacheEntry struct {
	provider *auth.OIDCProvider
	cfg      auth.OIDCProviderConfig
	expires  time.Time
}

// cachedOIDCProvider returns the provider from the discovery cache (#280). The
// key includes updated_at, so a config change misses immediately; entries
// expire after an hour.
func (h *Handlers) cachedOIDCProvider(ctx context.Context, providerID string) (*auth.OIDCProvider, auth.OIDCProviderConfig, error) {
	var apID pgtype.UUID
	if err := apID.Scan(providerID); err != nil {
		return nil, auth.OIDCProviderConfig{}, err
	}
	ap, err := h.queries.GetAuthProviderByID(ctx, apID)
	if err != nil {
		return nil, auth.OIDCProviderConfig{}, err
	}
	version := ap.ConfigEnc
	if ap.UpdatedAt.Valid {
		version = ap.UpdatedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	key := providerID + ":" + version
	if v, ok := h.oidcCache.Load(key); ok {
		entry := v.(*oidcCacheEntry)
		if time.Now().Before(entry.expires) {
			return entry.provider, entry.cfg, nil
		}
		h.oidcCache.Delete(key)
	}
	provider, cfg, err := h.newOIDCProvider(ctx, providerID, ap)
	if err != nil {
		return nil, auth.OIDCProviderConfig{}, err
	}
	h.oidcCache.Store(key, &oidcCacheEntry{provider: provider, cfg: cfg, expires: time.Now().Add(time.Hour)})
	return provider, cfg, nil
}

func (h *Handlers) loadOIDCProvider(ctx context.Context, providerID string) (*auth.OIDCProvider, auth.OIDCProviderConfig, error) {
	var apID pgtype.UUID
	if err := apID.Scan(providerID); err != nil {
		return nil, auth.OIDCProviderConfig{}, err
	}
	ap, err := h.queries.GetAuthProviderByID(ctx, apID)
	if err != nil {
		return nil, auth.OIDCProviderConfig{}, err
	}
	return h.newOIDCProvider(ctx, providerID, ap)
}

func (h *Handlers) newOIDCProvider(ctx context.Context, providerID string, ap db.AuthProvider) (*auth.OIDCProvider, auth.OIDCProviderConfig, error) {
	cfg, err := auth.OpenOIDCConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
	if err != nil {
		return nil, auth.OIDCProviderConfig{}, err
	}
	redirectURL := h.settings.Mail.PublicURL + "/api/v1/auth/oidc/" + providerID + "/callback"
	provider, err := auth.NewOIDCProvider(ctx, auth.OIDCConfig{
		IssuerURL:    cfg.IssuerURL,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  redirectURL,
		Claims:       cfg.Claims,
	})
	if err != nil {
		return nil, auth.OIDCProviderConfig{}, err
	}
	return provider, cfg, nil
}

var errOIDCNotProvisioned = errors.New("oidc: not provisioned")

// provisionOIDCUser applies the provisioning policy (#251): unknown subjects
// are denied by default, auto-create needs a verified e-mail in an allowed
// domain, and the IdP mapping is applied on every login.
func (h *Handlers) provisionOIDCUser(ctx context.Context, apID pgtype.UUID, cfg auth.OIDCProviderConfig, info *auth.OIDCUserInfo) (db.User, error) {
	mapped, err := identity.MapIdentity(identity.MappingInput{
		Groups:      info.Groups,
		RoleMapping: cfg.RoleMapping,
		TeamMapping: cfg.TeamMapping,
		DefaultRole: cfg.DefaultRole,
	})
	if err != nil {
		return db.User{}, err
	}

	user, err := h.queries.GetUserByExternalID(ctx, db.GetUserByExternalIDParams{
		AuthProviderID: apID,
		ExternalID:     &info.Subject,
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return db.User{}, err
		}
		if !cfg.AutoCreate || !info.EmailVerified || len(cfg.AllowedEmailDomains) == 0 || !domainAllowed(info.Email, cfg.AllowedEmailDomains) {
			return db.User{}, errOIDCNotProvisioned
		}
		user, err = h.queries.CreateUser(ctx, db.CreateUserParams{
			Name:           info.Name,
			Email:          info.Email,
			AuthProvider:   "oidc",
			AuthProviderID: apID,
			ExternalID:     &info.Subject,
			Role:           mapped.Role,
		})
		if err != nil {
			if !isUniqueViolation(err) {
				return db.User{}, err
			}
			// A concurrent login may have created the same subject; an
			// existing e-mail account is never linked.
			user, err = h.queries.GetUserByExternalID(ctx, db.GetUserByExternalIDParams{
				AuthProviderID: apID,
				ExternalID:     &info.Subject,
			})
			if err != nil {
				return db.User{}, errOIDCNotProvisioned
			}
		} else {
			h.applyTeams(ctx, user.ID, mapped.Teams)
			return user, nil
		}
	}

	if mapped.Role != user.Role {
		if err := h.queries.UpdateUserRole(ctx, db.UpdateUserRoleParams{ID: user.ID, Role: mapped.Role}); err != nil {
			return db.User{}, err
		}
		user.Role = mapped.Role
	}
	h.applyTeams(ctx, user.ID, mapped.Teams)
	return user, nil
}

func (h *Handlers) applyTeams(ctx context.Context, userID pgtype.UUID, teams []identity.TeamAssignment) {
	for _, t := range teams {
		teamID, err := uuidFromString(t.TeamID)
		if err != nil {
			continue
		}
		if err := h.queries.UpsertTeamMember(ctx, db.UpsertTeamMemberParams{
			TeamID: teamID,
			UserID: userID,
			Role:   t.TeamRole,
		}); err != nil {
			slog.Warn("team mapping failed", "team_id", t.TeamID, "err", err)
		}
	}
}

func domainAllowed(email string, domains []string) bool {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, d := range domains {
		if strings.ToLower(strings.TrimPrefix(d, "@")) == domain {
			return true
		}
	}
	return false
}

func randomState() string {
	b := make([]byte, 16)
	rand.Read(b)
	// RawURLEncoding: URL-safe and no '=' padding, so the token survives being
	// embedded raw in a query string (password reset link, OIDC state).
	return base64.RawURLEncoding.EncodeToString(b)
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
