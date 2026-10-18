package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/metrics"
	"github.com/galileostudio/baleia/internal/version"
	"github.com/go-chi/chi/v5/middleware"
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

func (h *Handlers) GetSetupStatus(ctx context.Context, request GetSetupStatusRequestObject) (GetSetupStatusResponseObject, error) {
	users, err := h.queries.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	return GetSetupStatus200JSONResponse{
		IsConfigured: len(users) > 0,
		Version:      version.Version,
	}, nil
}

func (h *Handlers) CreateFirstAdmin(ctx context.Context, request CreateFirstAdminRequestObject) (CreateFirstAdminResponseObject, error) {
	users, err := h.queries.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	if len(users) > 0 {
		return CreateFirstAdmin409JSONResponse{
			ConflictJSONResponse: ConflictJSONResponse{Code: "conflict", Message: "already set up"},
		}, nil
	}

	hash, err := auth.HashPasswordWithCost(request.Body.Password, h.cfg.PasswordCost())
	if err != nil {
		return nil, err
	}

	user, err := h.queries.CreateUser(ctx, db.CreateUserParams{
		Name:         request.Body.Name,
		Email:        string(request.Body.Email),
		PasswordHash: &hash,
		Role:         "admin",
		AuthProvider: "local",
	})
	if err != nil {
		return nil, err
	}

	accessToken, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, h.cfg.JWTSecret, h.cfg.AccessTTL())
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
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(h.cfg.RefreshTTL()), Valid: true},
		FamilyID:  newTokenFamily(),
	}); err != nil {
		return nil, err
	}

	h.ensurePersonalWorkspace(ctx, user.ID)

	return CreateFirstAdmin201JSONResponse{
		Token:        accessToken,
		RefreshToken: &refreshToken,
		ExpiresAt:    time.Now().Add(h.cfg.AccessTTL()),
		User:         dbUserToPublic(user),
	}, nil
}

// ── Auth ──────────────────────────────────────────────────────────────────────

func (h *Handlers) Login(ctx context.Context, request LoginRequestObject) (resp LoginResponseObject, err error) {
	defer func() { h.metrics.auth.Attempt(metrics.AuthPassword, authOutcome(resp, err)) }()
	ev := audit.Event{Action: audit.AuthLogin, ResourceType: "user"}
	// A failed login is recorded by a fingerprint of the e-mail, never the
	// e-mail itself or the password.
	failed := func(reason string) {
		actx := audit.WithAuthMethod(audit.WithAnonymous(ctx, audit.EmailFingerprint(string(request.Body.Email))), audit.AuthPassword)
		h.audited(actx, ev.Denied(reason))
	}
	// Progressive per-account delay (#51): repeated failures slow down, but a
	// cancelled request stops waiting and never hard-locks the account.
	if err := h.loginDelay().Wait(ctx, string(request.Body.Email)); err != nil {
		return nil, err
	}

	user, err := h.queries.GetUserByEmail(ctx, string(request.Body.Email))
	if err != nil {
		// Unknown e-mail: compare against a dummy hash anyway so the response
		// time does not reveal whether the account exists (#51).
		_ = auth.CheckPassword(h.dummyPasswordHash(), request.Body.Password)
		h.loginDelay().Failure(string(request.Body.Email))
		failed("invalid_credentials")
		return Login401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "invalid_credentials", Message: "invalid credentials"}}, nil
	}

	// An SSO-only account and a wrong password are indistinguishable (#51).
	if user.AuthProvider != "local" || user.PasswordHash == nil {
		_ = auth.CheckPassword(h.dummyPasswordHash(), request.Body.Password)
		h.loginDelay().Failure(string(request.Body.Email))
		failed("invalid_credentials")
		return Login401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "invalid_credentials", Message: "invalid credentials"}}, nil
	}

	if !auth.CheckPassword(*user.PasswordHash, request.Body.Password) {
		h.loginDelay().Failure(string(request.Body.Email))
		failed("invalid_credentials")
		return Login401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "invalid_credentials", Message: "invalid credentials"}}, nil
	}
	h.loginDelay().Success(string(request.Body.Email))

	accessToken, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, h.cfg.JWTSecret, h.cfg.AccessTTL())
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
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(h.cfg.RefreshTTL()), Valid: true},
		FamilyID:  newTokenFamily(),
	}); err != nil {
		return nil, err
	}

	h.ensurePersonalWorkspace(ctx, user.ID)
	h.audited(audit.AsUser(ctx, user.ID.String(), audit.AuthPassword), withID(ev, user.ID).Allowed())

	return Login200JSONResponse{
		Token:        accessToken,
		RefreshToken: &refreshToken,
		ExpiresAt:    time.Now().Add(h.cfg.AccessTTL()),
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
			// Logout ends the whole rotation family, not only this token.
			_ = h.queries.RevokeRefreshFamily(ctx, rt.FamilyID)
			h.audited(audit.AsUser(ctx, idString(rt.UserID), audit.AuthRefreshTok),
				audit.Event{Action: audit.AuthLogout, ResourceType: "user", ResourceID: idString(rt.UserID), Outcome: audit.Allowed})
		}
	}
	return Logout204Response{}, nil
}

func (h *Handlers) GetMe(ctx context.Context, request GetMeRequestObject) (GetMeResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return GetMe401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	user, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		return GetMe401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "user not found"}}, nil
	}

	principal, _ := authz.PrincipalFromContext(ctx)
	me := Me{
		Id:            openapi_types.UUID(user.ID.Bytes),
		Name:          user.Name,
		Email:         openapi_types.Email(user.Email),
		Role:          MeRole(user.Role),
		AuthProvider:  MeAuthProvider(user.AuthProvider),
		CreatedAt:     pgTimestampToTime(user.CreatedAt),
		Features:      h.features(ctx),
		GlobalActions: h.globalActions(ctx, principal),
	}
	if ws, err := h.queries.PersonalWorkspaceID(ctx, user.ID); err == nil {
		id := openapi_types.UUID(ws.Bytes)
		me.PersonalWorkspaceId = &id
	}
	return GetMe200JSONResponse(me), nil
}

// features are the navigation flags of /auth/me (#104). Agents (#59) and the
// assistant (#62) do not exist yet; retrievers exist when a Trino engine can
// host their destination (an enabled Iceberg catalog).
func (h *Handlers) features(ctx context.Context) Features {
	f := Features{}
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
	return OidcRedirect302Response{}, nil
}

func (h *Handlers) OidcCallback(ctx context.Context, request OidcCallbackRequestObject) (OidcCallbackResponseObject, error) {
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
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "missing refresh token"},
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
				_ = h.queries.RevokeRefreshFamily(ctx, old.FamilyID)
				h.audited(audit.WithAuthMethod(ctx, audit.AuthRefreshTok), ev.Denied(audit.ReasonReuseDetected))
				reused = true
			}
		}
		if !reused {
			h.audited(audit.WithAuthMethod(ctx, audit.AuthRefreshTok), ev.Denied("invalid_token"))
		}
		return RefreshToken401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid or expired refresh token"},
		}, nil
	}

	user, err := h.queries.GetUserByID(ctx, rt.UserID)
	if err != nil {
		return RefreshToken401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "user not found"},
		}, nil
	}

	accessToken, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, h.cfg.JWTSecret, h.cfg.AccessTTL())
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
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(h.cfg.RefreshTTL()), Valid: true},
		FamilyID:  rt.FamilyID,
	}); err != nil {
		return nil, err
	}

	h.audited(audit.AsUser(ctx, user.ID.String(), audit.AuthRefreshTok), withID(ev, user.ID).Allowed())
	return RefreshToken200JSONResponse{
		Token:        accessToken,
		RefreshToken: &newRefreshToken,
		ExpiresAt:    time.Now().Add(h.cfg.AccessTTL()),
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

	if h.cfg.SMTPHost == "" || h.cfg.SMTPPort == "" {
		return RequestPasswordReset503JSONResponse{
			Code:    "service_unavailable",
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
	expires := time.Now().Add(h.cfg.ResetTTL())

	// Only the hash is stored; the raw token travels in the e-mail link (#51).
	if err := h.queries.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		TokenHash: auth.HashToken(token),
		UserID:    user.ID,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		slog.Error("CreatePasswordResetToken failed", "err", err)
		return nil, err
	}

	resetURL := h.cfg.PublicURL + "/reset-password?token=" + token
	if err := h.sendMail(user.Email, resetURL); err != nil {
		slog.Error("sendResetEmail failed", "err", err)
		return nil, err
	}

	return RequestPasswordReset204Response{}, nil
}

func (h *Handlers) ConfirmPasswordReset(ctx context.Context, request ConfirmPasswordResetRequestObject) (ConfirmPasswordResetResponseObject, error) {
	ev := audit.Event{Action: audit.AuthPasswordResetConfirm, ResourceType: "user"}
	// Consuming the hash makes the link single use even before it expires (#51).
	row, err := h.queries.ConsumePasswordResetToken(ctx, auth.HashToken(request.Body.Token))
	if err != nil {
		h.audited(ctx, ev.Denied("invalid_token"))
		return ConfirmPasswordReset401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid or expired token"},
		}, nil
	}

	hash, err := auth.HashPasswordWithCost(request.Body.NewPassword, h.cfg.PasswordCost())
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
		return GetProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	user, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		return GetProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "user not found"}}, nil
	}

	return GetProfile200JSONResponse(dbUserToPublic(user)), nil
}

func (h *Handlers) UpdateProfile(ctx context.Context, request UpdateProfileRequestObject) (UpdateProfileResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	uid, err := uuidFromString(userID)
	if err != nil {
		return UpdateProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	current, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		return UpdateProfile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "user not found"}}, nil
	}

	name := current.Name
	email := current.Email
	if request.Body.Name != nil {
		name = *request.Body.Name
	}
	if request.Body.Email != nil {
		email = string(*request.Body.Email)
	}

	updated, err := h.queries.UpdateUser(ctx, db.UpdateUserParams{
		ID:    uid,
		Name:  name,
		Email: email,
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
		return UpdatePassword401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid user"}}, nil
	}

	user, err := h.queries.GetUserByID(ctx, uid)
	if err != nil {
		return UpdatePassword401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "user not found"}}, nil
	}

	if user.PasswordHash == nil || !auth.CheckPassword(*user.PasswordHash, request.Body.CurrentPassword) {
		return UpdatePassword422JSONResponse{
			UnprocessableEntityJSONResponse: UnprocessableEntityJSONResponse{
				Code:    "validation_error",
				Message: "current password is incorrect",
				Errors: []struct {
					Field   *string `json:"field,omitempty"`
					Message *string `json:"message,omitempty"`
				}{},
			},
		}, nil
	}

	hash, err := auth.HashPasswordWithCost(request.Body.NewPassword, h.cfg.PasswordCost())
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

func (h *Handlers) HandleOIDCRedirect(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("provider_id")
	provider, err := h.loadOIDCProvider(r, providerID)
	if err != nil {
		respondError(w, http.StatusNotFound, "not_found", "provider not found")
		return
	}

	state := randomState()
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, provider.AuthURL(state), http.StatusFound)
}

func (h *Handlers) HandleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
	w = ww
	defer func() { h.metrics.auth.Attempt(metrics.AuthOIDC, statusOutcome(ww.Status())) }()
	providerID := r.PathValue("provider_id")
	provider, err := h.loadOIDCProvider(r, providerID)
	if err != nil {
		respondError(w, http.StatusNotFound, "not_found", "provider not found")
		return
	}

	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		respondError(w, http.StatusBadRequest, "bad_request", "invalid state")
		return
	}

	info, err := provider.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		h.audited(audit.WithAuthMethod(r.Context(), audit.AuthOIDC), audit.Event{Action: audit.AuthLogin, ResourceType: "user", Outcome: audit.Denied, Reason: "oidc_exchange_failed"})
		respondError(w, http.StatusUnauthorized, "unauthorized", "oidc exchange failed")
		return
	}

	var apID pgtype.UUID
	if err := apID.Scan(providerID); err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", "invalid provider")
		return
	}

	user, err := h.queries.GetUserByExternalID(r.Context(), db.GetUserByExternalIDParams{
		AuthProviderID: apID,
		ExternalID:     &info.Subject,
	})
	if err != nil {
		user, err = h.queries.CreateUser(r.Context(), db.CreateUserParams{
			Name:           info.Name,
			Email:          info.Email,
			AuthProvider:   "oidc",
			AuthProviderID: apID,
			ExternalID:     &info.Subject,
			Role:           "member",
		})
		if err != nil {
			respondError(w, http.StatusInternalServerError, "internal_error", "could not create user")
			return
		}
	}

	accessToken, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, h.cfg.JWTSecret, h.cfg.AccessTTL())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal_error", "token error")
		return
	}

	refreshToken, refreshHash, err := auth.GenerateRefreshToken()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal_error", "token error")
		return
	}

	if _, err := h.queries.CreateRefreshToken(r.Context(), db.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(h.cfg.RefreshTTL()), Valid: true},
		FamilyID:  newTokenFamily(),
	}); err != nil {
		respondError(w, http.StatusInternalServerError, "internal_error", "token error")
		return
	}

	h.audited(audit.AsUser(r.Context(), user.ID.String(), audit.AuthOIDC), audit.Event{Action: audit.AuthLogin, ResourceType: "user", ResourceID: idString(user.ID), Outcome: audit.Allowed})
	http.Redirect(w, r, "/?token="+accessToken+"&refresh_token="+refreshToken, http.StatusFound)
}

func (h *Handlers) loadOIDCProvider(r *http.Request, providerID string) (*auth.OIDCProvider, error) {
	var apID pgtype.UUID
	if err := apID.Scan(providerID); err != nil {
		return nil, err
	}

	ap, err := h.queries.GetAuthProviderByID(r.Context(), apID)
	if err != nil {
		return nil, err
	}

	var cfg struct {
		IssuerURL    string `json:"issuer_url"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal([]byte(ap.ConfigEnc), &cfg); err != nil {
		return nil, err
	}

	redirectURL := h.cfg.PublicURL + "/api/v1/auth/oidc/" + providerID + "/callback"

	return auth.NewOIDCProvider(r.Context(), auth.OIDCConfig{
		IssuerURL:    cfg.IssuerURL,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  redirectURL,
	})
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

// statusOutcome classifies a raw handler's status.
func statusOutcome(status int) string {
	switch {
	case status == 0 || status < 400:
		return metrics.OutcomeSuccess
	case status >= 500:
		return metrics.OutcomeError
	}
	return metrics.OutcomeInvalidCredentials
}
