package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// stubIDP is a minimal OIDC provider: discovery, JWKS and a token endpoint
// that enforces the S256 PKCE challenge.
type stubIDP struct {
	*httptest.Server
	key               *rsa.PrivateKey
	clientID          string
	expectedChallenge string
}

func newStubIDP(t *testing.T, expectedChallenge string) *stubIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &stubIDP{key: key, clientID: "client", expectedChallenge: expectedChallenge}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                s.URL,
			"authorization_endpoint":                s.URL + "/authorize",
			"token_endpoint":                        s.URL + "/token",
			"jwks_uri":                              s.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", s.jwks)
	mux.HandleFunc("/token", s.token)
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *stubIDP) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := s.key.PublicKey
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA", "kid": "test", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

func (s *stubIDP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	verifier := r.Form.Get("code_verifier")
	if verifier == "" || auth.PKCEChallenge(verifier) != s.expectedChallenge {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   s.URL,
		"sub":   "user-1",
		"aud":   s.clientID,
		"exp":   now.Add(5 * time.Minute).Unix(),
		"iat":   now.Unix(),
		"email": "user@example.test",
		"name":  "Test User",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test"
	raw, err := token.SignedString(s.key)
	if err != nil {
		http.Error(w, "sign", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "access",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     raw,
	})
}

func newProvider(t *testing.T, idp *stubIDP) *auth.OIDCProvider {
	t.Helper()
	provider, err := auth.NewOIDCProvider(context.Background(), auth.OIDCConfig{
		IssuerURL:    idp.URL,
		ClientID:     idp.clientID,
		ClientSecret: "secret",
		RedirectURL:  "http://localhost/api/v1/auth/oidc/test/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Errorf("verifier length = %d, want 43..128", len(verifier))
	}
	if challenge != auth.PKCEChallenge(verifier) {
		t.Error("challenge is not the S256 hash of the verifier")
	}
}

// TestOIDC_FullFlowWithPKCE: the authorization URL carries S256 PKCE and the
// stub provider only accepts the matching verifier (#250).
func TestOIDC_FullFlowWithPKCE(t *testing.T) {
	verifier, challenge, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	idp := newStubIDP(t, challenge)
	provider := newProvider(t, idp)

	authURL, err := provider.AuthURL("state-1", challenge)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("state") != "state-1" {
		t.Errorf("state = %q", q.Get("state"))
	}
	if q.Get("code_challenge") != challenge || q.Get("code_challenge_method") != "S256" {
		t.Errorf("PKCE params = challenge %q method %q", q.Get("code_challenge"), q.Get("code_challenge_method"))
	}

	info, err := provider.Exchange(context.Background(), "code-1", verifier)
	if err != nil {
		t.Fatal(err)
	}
	if info.Subject != "user-1" || info.Email != "user@example.test" || info.Name != "Test User" {
		t.Errorf("user info = %+v", info)
	}
}

// TestOIDC_BadVerifier: a wrong verifier is rejected by the provider (#250).
func TestOIDC_BadVerifier(t *testing.T) {
	_, challenge, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	idp := newStubIDP(t, challenge)
	provider := newProvider(t, idp)

	if _, err := provider.Exchange(context.Background(), "code-1", "wrong-verifier"); err == nil {
		t.Fatal("the provider accepted a mismatched PKCE verifier")
	}
}

// TestOIDC_NoPKCE: neither the authorization URL nor the exchange works
// without a challenge/verifier (#250 forbids a no-PKCE path).
func TestOIDC_NoPKCE(t *testing.T) {
	_, challenge, _ := auth.GeneratePKCE()
	idp := newStubIDP(t, challenge)
	provider := newProvider(t, idp)

	if _, err := provider.AuthURL("state-1", ""); err == nil {
		t.Error("AuthURL accepted an empty challenge")
	}
	if _, err := provider.Exchange(context.Background(), "code-1", ""); err == nil {
		t.Error("Exchange accepted an empty verifier")
	}
}
