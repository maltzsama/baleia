package api

import (
	"encoding/json"

	"github.com/galileostudio/baleia/internal/crypto"
)

// encodeCredentials marshals a credentials value and encrypts it for storage in
// catalog_connections.credentials_enc. A nil value yields a nil result, so a
// missing field preserves whatever secret is already stored. The returned
// string is always ciphertext — never plaintext.
func encodeCredentials(creds any, key string) (*string, error) {
	if creds == nil {
		return nil, nil
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
