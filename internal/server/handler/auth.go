package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

type AuthHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

func NewAuthHandler(queries *db.Queries, cfg *config.Config) *AuthHandler {
	return &AuthHandler{queries: queries, cfg: cfg}
}

// POST /api/v1/auth/setup
func (h *AuthHandler) Setup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	users, err := h.queries.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(users) > 0 {
		http.Error(w, "already set up", http.StatusConflict)
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	user, err := h.queries.CreateUser(r.Context(), db.CreateUserParams{
		Name:         body.Name,
		Email:        body.Email,
		PasswordHash: &hash,
		Role:         "admin",
		AuthProvider: "local",
	})
	if err != nil {
		http.Error(w, "could not create user", http.StatusInternalServerError)
		return
	}

	token, err := auth.NewToken(user.ID.String(), user.Role, h.cfg.JWTSecret)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{
		"token": token,
		"user":  publicUser(user),
	})
}

// POST /api/v1/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	user, err := h.queries.GetUserByEmail(r.Context(), body.Email)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	if user.AuthProvider != "local" {
		http.Error(w, "use SSO to login", http.StatusBadRequest)
		return
	}

	if user.PasswordHash == nil || !auth.CheckPassword(*user.PasswordHash, body.Password) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := auth.NewToken(user.ID.String(), user.Role, h.cfg.JWTSecret)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  publicUser(user),
	})
}

// GET /api/v1/auth/me
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())

	var uid pgtype.UUID
	if err := uid.Scan(userID); err != nil {
		http.Error(w, "invalid user", http.StatusUnauthorized)
		return
	}

	user, err := h.queries.GetUserByID(r.Context(), uid)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	respondJSON(w, http.StatusOK, publicUser(user))
}

// GET /api/v1/auth/oidc/:provider
func (h *AuthHandler) OIDCRedirect(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("provider")
	provider, err := h.loadOIDCProvider(r, providerID)
	if err != nil {
		http.Error(w, "provider not found", http.StatusNotFound)
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

// GET /api/v1/auth/oidc/:provider/callback
func (h *AuthHandler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("provider")
	provider, err := h.loadOIDCProvider(r, providerID)
	if err != nil {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}

	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	info, err := provider.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "oidc exchange failed", http.StatusUnauthorized)
		return
	}

	// Busca provider ID no banco
	var apID pgtype.UUID
	if err := apID.Scan(providerID); err != nil {
		http.Error(w, "invalid provider", http.StatusBadRequest)
		return
	}

	// Tenta achar usuário existente pelo external_id
	user, err := h.queries.GetUserByExternalID(r.Context(), db.GetUserByExternalIDParams{
		AuthProviderID: apID,
		ExternalID:     &info.Subject,
	})
	if err != nil {
		// Cria novo usuário OIDC
		user, err = h.queries.CreateUser(r.Context(), db.CreateUserParams{
			Name:           info.Name,
			Email:          info.Email,
			AuthProvider:   "oidc",
			AuthProviderID: apID,
			ExternalID:     &info.Subject,
			Role:           "member",
		})
		if err != nil {
			http.Error(w, "could not create user", http.StatusInternalServerError)
			return
		}
	}

	token, err := auth.NewToken(user.ID.String(), user.Role, h.cfg.JWTSecret)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Redireciona pro frontend com o token
	http.Redirect(w, r, "/?token="+token, http.StatusFound)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func publicUser(u db.User) map[string]any {
	return map[string]any{
		"id":    u.ID,
		"name":  u.Name,
		"email": u.Email,
		"role":  u.Role,
	}
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func randomState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func (h *AuthHandler) loadOIDCProvider(r *http.Request, providerID string) (*auth.OIDCProvider, error) {
	var apID pgtype.UUID
	if err := apID.Scan(providerID); err != nil {
		return nil, err
	}

	ap, err := h.queries.GetAuthProviderByID(r.Context(), apID)
	if err != nil {
		return nil, err
	}

	// config_enc é JSON com issuer_url, client_id, client_secret
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
