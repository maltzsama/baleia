package auth_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/oidctest"
)

func newProvider(t *testing.T, idp *oidctest.StubIDP) *auth.OIDCProvider {
	t.Helper()
	provider, err := auth.NewOIDCProvider(context.Background(), auth.OIDCConfig{
		IssuerURL:    idp.URL,
		ClientID:     idp.ClientID,
		ClientSecret: "secret",
		RedirectURL:  "http://localhost/api/v1/auth/oidc/test/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestGeneratePKCE(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	verifier, challenge, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := auth.GenerateNonce()
	if err != nil {
		t.Fatal(err)
	}
	idp, err := oidctest.New(challenge, "client")
	if err != nil {
		t.Fatal(err)
	}
	defer idp.Close()
	idp.Nonce = nonce
	provider := newProvider(t, idp)

	authURL, err := provider.AuthURL("state-1", challenge, nonce)
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
	if q.Get("nonce") != nonce {
		t.Errorf("nonce = %q, want %q", q.Get("nonce"), nonce)
	}
	if q.Get("code_challenge") != challenge || q.Get("code_challenge_method") != "S256" {
		t.Errorf("PKCE params = challenge %q method %q", q.Get("code_challenge"), q.Get("code_challenge_method"))
	}

	info, err := provider.Exchange(context.Background(), "code-1", verifier, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if info.Subject != idp.Subject || info.Email != idp.Email || info.Name != idp.Name {
		t.Errorf("user info = %+v", info)
	}
}

// TestOIDC_BadVerifier: a wrong verifier is rejected by the provider (#250).
func TestOIDC_BadVerifier(t *testing.T) {
	t.Parallel()
	_, challenge, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := auth.GenerateNonce()
	if err != nil {
		t.Fatal(err)
	}
	idp, err := oidctest.New(challenge, "client")
	if err != nil {
		t.Fatal(err)
	}
	defer idp.Close()
	idp.Nonce = nonce
	provider := newProvider(t, idp)

	if _, err := provider.Exchange(context.Background(), "code-1", "wrong-verifier", nonce); err == nil {
		t.Fatal("the provider accepted a mismatched PKCE verifier")
	}
}

// TestOIDC_NonceMismatch: the ID token must carry the nonce from the attempt.
func TestOIDC_NonceMismatch(t *testing.T) {
	t.Parallel()
	verifier, challenge, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := auth.GenerateNonce()
	if err != nil {
		t.Fatal(err)
	}
	idp, err := oidctest.New(challenge, "client")
	if err != nil {
		t.Fatal(err)
	}
	defer idp.Close()
	idp.Nonce = "wrong-nonce"
	provider := newProvider(t, idp)

	if _, err := provider.Exchange(context.Background(), "code-1", verifier, nonce); err == nil {
		t.Fatal("the provider accepted a mismatched nonce")
	}
}

// TestOIDC_NoPKCE: neither the authorization URL nor the exchange works
// without a challenge/verifier (#250 forbids a no-PKCE path).
func TestOIDC_NoPKCE(t *testing.T) {
	t.Parallel()
	_, challenge, _ := auth.GeneratePKCE()
	nonce, _ := auth.GenerateNonce()
	idp, err := oidctest.New(challenge, "client")
	if err != nil {
		t.Fatal(err)
	}
	defer idp.Close()
	provider := newProvider(t, idp)

	if _, err := provider.AuthURL("state-1", "", nonce); err == nil {
		t.Error("AuthURL accepted an empty challenge")
	}
	if _, err := provider.AuthURL("state-1", challenge, ""); err == nil {
		t.Error("AuthURL accepted an empty nonce")
	}
	if _, err := provider.Exchange(context.Background(), "code-1", "", nonce); err == nil {
		t.Error("Exchange accepted an empty verifier")
	}
	if _, err := provider.Exchange(context.Background(), "code-1", "verifier", ""); err == nil {
		t.Error("Exchange accepted an empty nonce")
	}
}
