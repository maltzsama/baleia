package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// AccessTokenTTL is the default access-token lifetime. A deployment can
	// override it with BALEIA_ACCESS_TOKEN_TTL.
	AccessTokenTTL = 15 * time.Minute
	// RefreshTokenTTL is the default refresh-token lifetime. A deployment can
	// override it with BALEIA_REFRESH_TOKEN_TTL.
	RefreshTokenTTL = 30 * 24 * time.Hour
)

type Claims struct {
	UserID string `json:"uid"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// NewAccessToken signs an access token with the default lifetime.
func NewAccessToken(userID, role, secret string) (string, error) {
	return NewAccessTokenWithTTL(userID, role, secret, AccessTokenTTL)
}

// NewAccessTokenWithTTL signs an access token that expires after ttl. The jti
// claim is an opaque id used only to correlate audit records, never for
// authorization.
func NewAccessTokenWithTTL(userID, role, secret string, ttl time.Duration) (string, error) {
	jti, err := randomID()
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func GenerateRefreshToken() (token string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken hashes an opaque bearer token (refresh or password-reset) with
// SHA-256 and encodes it URL-safe. The tokens are high-entropy random values,
// so a fast hash is enough; only the hash is ever stored.
func HashToken(token string) string {
	hashBytes := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(hashBytes[:])
}

// HashRefreshToken is HashToken for refresh tokens.
func HashRefreshToken(token string) string { return HashToken(token) }

// randomID returns a URL-safe 128-bit random id for the jti claim.
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func ParseToken(tokenStr, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
