package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"
	"log/slog"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/version"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
)

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

	hash, err := auth.HashPassword(request.Body.Password)
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

	accessToken, err := auth.NewAccessToken(user.ID.String(), user.Role, h.cfg.JWTSecret)
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
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(auth.RefreshTokenTTL), Valid: true},
	}); err != nil {
		return nil, err
	}

	h.ensurePersonalWorkspace(ctx, user.ID)

	return CreateFirstAdmin201JSONResponse{
		Token:        accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(auth.AccessTokenTTL),
		User:         dbUserToPublic(user),
	}, nil
}

// ── Auth ──────────────────────────────────────────────────────────────────────

func (h *Handlers) Login(ctx context.Context, request LoginRequestObject) (LoginResponseObject, error) {
	user, err := h.queries.GetUserByEmail(ctx, string(request.Body.Email))
	if err != nil {
		return Login401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid credentials"}}, nil
	}

	if user.AuthProvider != "local" {
		return Login401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "use SSO to login"}}, nil
	}

	if user.PasswordHash == nil || !auth.CheckPassword(*user.PasswordHash, request.Body.Password) {
		return Login401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid credentials"}}, nil
	}

	accessToken, err := auth.NewAccessToken(user.ID.String(), user.Role, h.cfg.JWTSecret)
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
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(auth.RefreshTokenTTL), Valid: true},
	}); err != nil {
		return nil, err
	}

	h.ensurePersonalWorkspace(ctx, user.ID)

	return Login200JSONResponse{
		Token:        accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(auth.AccessTokenTTL),
		User:         dbUserToPublic(user),
	}, nil
}

func (h *Handlers) Logout(ctx context.Context, request LogoutRequestObject) (LogoutResponseObject, error) {
	if request.Body != nil && request.Body.RefreshToken != nil && *request.Body.RefreshToken != "" {
		hash := auth.HashRefreshToken(*request.Body.RefreshToken)
		if rt, err := h.queries.GetRefreshTokenByHash(ctx, hash); err == nil {
			_ = h.queries.RevokeRefreshToken(ctx, rt.ID)
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

	return GetMe200JSONResponse(dbUserToPublic(user)), nil
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

func (h *Handlers) RefreshToken(ctx context.Context, request RefreshTokenRequestObject) (RefreshTokenResponseObject, error) {
	hash := auth.HashRefreshToken(request.Body.RefreshToken)

	rt, err := h.queries.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
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

	_ = h.queries.RevokeRefreshToken(ctx, rt.ID)

	accessToken, err := auth.NewAccessToken(user.ID.String(), user.Role, h.cfg.JWTSecret)
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
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(auth.RefreshTokenTTL), Valid: true},
	}); err != nil {
		return nil, err
	}

	return RefreshToken200JSONResponse{
		Token:        accessToken,
		RefreshToken: newRefreshToken,
		ExpiresAt:    time.Now().Add(auth.AccessTokenTTL),
	}, nil
}

// ── Password Reset ────────────────────────────────────────────────────────────

func (h *Handlers) RequestPasswordReset(ctx context.Context, request RequestPasswordResetRequestObject) (RequestPasswordResetResponseObject, error) {

	if h.cfg.SMTPHost == "" || h.cfg.SMTPPort == "" {
		return RequestPasswordReset503JSONResponse{
			Code:    "service_unavailable",
			Message: "email service not configured",
		}, nil
	}

	user, err := h.queries.GetUserByEmail(ctx, string(request.Body.Email))
	if err != nil {
		return RequestPasswordReset204Response{}, nil
	}

	token := randomState()
	expires := time.Now().Add(1 * time.Hour)

	if err := h.queries.SetPasswordResetToken(ctx, db.SetPasswordResetTokenParams{
		Email:                  string(request.Body.Email),
		PasswordResetToken:     &token,
		PasswordResetExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		slog.Error("SetPasswordResetToken failed", "err", err)
		return nil, err
	}

	resetURL := h.cfg.PublicURL + "/reset-password?token=" + token
	if err := sendResetEmail(h.cfg, user.Email, resetURL); err != nil {
		slog.Error("sendResetEmail failed", "err", err)
		return nil, err
	}

	return RequestPasswordReset204Response{}, nil
}

func (h *Handlers) ConfirmPasswordReset(ctx context.Context, request ConfirmPasswordResetRequestObject) (ConfirmPasswordResetResponseObject, error) {
	user, err := h.queries.GetUserByResetToken(ctx, &request.Body.Token)
	if err != nil {
		return ConfirmPasswordReset401JSONResponse{
			UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: "unauthorized", Message: "invalid or expired token"},
		}, nil
	}

	hash, err := auth.HashPassword(request.Body.NewPassword)
	if err != nil {
		return nil, err
	}

	if err := h.queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		ID:           user.ID,
		PasswordHash: &hash,
	}); err != nil {
		return nil, err
	}

	return ConfirmPasswordReset204Response{}, nil
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

	hash, err := auth.HashPassword(request.Body.NewPassword)
	if err != nil {
		return nil, err
	}

	if err := h.queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		ID:           uid,
		PasswordHash: &hash,
	}); err != nil {
		return nil, err
	}

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

	accessToken, err := auth.NewAccessToken(user.ID.String(), user.Role, h.cfg.JWTSecret)
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
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(auth.RefreshTokenTTL), Valid: true},
	}); err != nil {
		respondError(w, http.StatusInternalServerError, "internal_error", "token error")
		return
	}

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