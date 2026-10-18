package auth

import (
	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

// HashAPIKey is the lookup hash for an X-Api-Key secret. The raw key is never
// stored; hex matches the pre-#276 on-disk format so existing keys keep working.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// DefaultBcryptCost is the production default (BALEIA_BCRYPT_COST). bcrypt
// refuses values below bcrypt.MinCost (10).
const DefaultBcryptCost = 12

// HashPassword hashes with bcrypt's default cost. Tests and seeds use it; the
// login paths hash at the configured cost via HashPasswordWithCost.
func HashPassword(password string) (string, error) {
	return HashPasswordWithCost(password, bcrypt.DefaultCost)
}

// HashPasswordWithCost hashes a password at an explicit bcrypt cost.
func HashPasswordWithCost(password string, cost int) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
