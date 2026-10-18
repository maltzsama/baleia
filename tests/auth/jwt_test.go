package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/galileostudio/baleia/internal/auth"
)

func TestNewAccessToken(t *testing.T) {
	secret := "test-secret"
	token, err := auth.NewAccessToken("user-1", "admin", secret)
	if err != nil {
		t.Fatalf("NewAccessToken failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	claims, err := auth.ParseToken(token, secret)
	if err != nil {
		t.Fatalf("ParseToken failed: %v", err)
	}

	if claims.UserID != "user-1" {
		t.Errorf("expected user-1, got %s", claims.UserID)
	}
	if claims.Role != "admin" {
		t.Errorf("expected admin, got %s", claims.Role)
	}
	if claims.ID == "" {
		t.Error("expected a jti claim for audit correlation")
	}

	expiresIn := time.Until(claims.ExpiresAt.Time)
	if expiresIn <= 0 || expiresIn > auth.AccessTokenTTL {
		t.Errorf("expected expiry within %v, got %v", auth.AccessTokenTTL, expiresIn)
	}
}

func TestParseTokenInvalid(t *testing.T) {
	_, err := auth.ParseToken("not.a.token", "secret")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestParseTokenWrongSecret(t *testing.T) {
	token, err := auth.NewAccessToken("user-1", "member", "secret-a")
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.ParseToken(token, "secret-b")
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	token, hash, err := auth.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken failed: %v", err)
	}
	if len(token) == 0 {
		t.Fatal("expected non-empty token")
	}
	if len(hash) == 0 {
		t.Fatal("expected non-empty hash")
	}
	if token == hash {
		t.Fatal("token should not equal its hash")
	}
}

func TestHashRefreshToken(t *testing.T) {
	token, _, _ := auth.GenerateRefreshToken()
	hash1 := auth.HashRefreshToken(token)
	hash2 := auth.HashRefreshToken(token)
	if hash1 != hash2 {
		t.Fatal("HashRefreshToken must be deterministic")
	}
}

func TestGenerateRefreshTokenUnique(t *testing.T) {
	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, _, err := auth.GenerateRefreshToken()
		if err != nil {
			t.Fatal(err)
		}
		if tokens[token] {
			t.Fatal("generated duplicate refresh token")
		}
		tokens[token] = true
	}
}

func TestExpiredToken(t *testing.T) {
	secret := "test-secret"
	claims := auth.Claims{
		UserID: "user-1",
		Role:   "member",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.ParseToken(tokenStr, secret)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}
