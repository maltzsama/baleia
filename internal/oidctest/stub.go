// Package oidctest provides an in-process fake OIDC provider for tests. It is
// never imported by production code.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// StubIDP is a minimal OIDC provider: discovery, JWKS and a token endpoint
// that enforces an S256 PKCE challenge (unless ExpectedChallenge is empty).
type StubIDP struct {
	*httptest.Server

	ClientID          string
	Subject           string
	Email             string
	EmailVerified     bool
	Name              string
	Nonce             string
	Groups            []string
	ExtraClaims       map[string]any
	ExpectedChallenge string

	key *rsa.PrivateKey
}

// New starts a stub provider. An empty expectedChallenge accepts any non-empty
// verifier, which is useful when the caller generates the verifier itself.
func New(expectedChallenge, clientID string) (*StubIDP, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	s := &StubIDP{
		ClientID:          clientID,
		Subject:           "user-1",
		Email:             "user@example.test",
		EmailVerified:     true,
		Name:              "Test User",
		ExpectedChallenge: expectedChallenge,
		key:               key,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("/jwks", s.jwks)
	mux.HandleFunc("/token", s.token)
	s.Server = httptest.NewServer(mux)
	return s, nil
}

func (s *StubIDP) discovery(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                s.URL,
		"authorization_endpoint":                s.URL + "/authorize",
		"token_endpoint":                        s.URL + "/token",
		"jwks_uri":                              s.URL + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (s *StubIDP) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := s.key.PublicKey
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA", "kid": "test", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

func (s *StubIDP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	verifier := r.Form.Get("code_verifier")
	if verifier == "" || (s.ExpectedChallenge != "" && auth.PKCEChallenge(verifier) != s.ExpectedChallenge) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":            s.URL,
		"sub":            s.Subject,
		"aud":            s.ClientID,
		"exp":            now.Add(5 * time.Minute).Unix(),
		"iat":            now.Unix(),
		"email":          s.Email,
		"email_verified": s.EmailVerified,
		"name":           s.Name,
		"nonce":          s.Nonce,
		"groups":         s.Groups,
	}
	for k, v := range s.ExtraClaims {
		claims[k] = v
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
