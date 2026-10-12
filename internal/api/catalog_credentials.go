package api

import (
	"encoding/json"
	"fmt"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/crypto"
)

// encodeCredentials marshals a credentials value and encrypts it for storage in
// catalog_connections.credentials_enc. A nil value yields a nil result, so a
// missing field preserves whatever secret is already stored. The returned
// string is always ciphertext — never plaintext.
//
// It refuses to encrypt with a missing or weak key, so credentials can never be
// stored under the shipped development default even if startup validation is
// bypassed.
func encodeCredentials(creds any, key string) (*string, error) {
	if creds == nil {
		return nil, nil
	}
	if err := config.ValidateSecret(key); err != nil {
		return nil, fmt.Errorf("refusing to encrypt credentials: %w", err)
	}
	b, err := json.Marshal(creds)
	if err != nil {
		return nil, err
	}
	enc, err := crypto.Encrypt(b, key)
	if err != nil {
		return nil, err
	}
	return &enc, nil
}
