// Package crypto seals connection credentials for storage.
//
// Credentials are encrypted with AES-256-GCM under a key derived (HKDF-SHA256)
// from BALEIA_ENCRYPTION_KEY, which is separate from the JWT signing secret.
// Every envelope is versioned, names the key that sealed it, and is bound to
// its owning row through the GCM additional authenticated data, so a ciphertext
// copied to another row does not open.
//
// There is no fallback anywhere in this package: a value that cannot be opened
// yields a typed error, never the input.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MinKeyLen is the minimum accepted length of an encryption key.
const MinKeyLen = 32

const (
	// hkdfInfo domain-separates credential envelopes.
	hkdfInfo = "baleia/credentials/v1"
	// registryHKDFInfo domain-separates catalog-registry envelopes (Phase B of
	// #31 / D4): a credential key can never open a registry value, even if an
	// operator reuses the same secret by mistake.
	registryHKDFInfo = "baleia/registry/v1"
	envelopeV1       = "v1"
	keyIDHexChars    = 8
)

var (
	// ErrUnknownKey means the envelope was sealed with a key this keyring does
	// not hold (rotated away, or a different deployment's key).
	ErrUnknownKey = errors.New("crypto: envelope encrypted with unknown key")
	// ErrCorrupt means the envelope is malformed, tampered with, or bound to a
	// different row than the one it is being opened for.
	ErrCorrupt = errors.New("crypto: envelope corrupt or tampered")
	// ErrLegacyFormat means the value predates the versioned envelope and must
	// be converted with `baleia migrate-credentials`.
	ErrLegacyFormat = errors.New("crypto: legacy envelope; run `make migrate-credentials` (or `/migrate -cmd=credentials` in the image)")
)

type derivedKey struct {
	id   string
	aead cipher.AEAD
}

// Keyring seals with the current key and opens with the current or, during a
// rotation, the previous key.
type Keyring struct {
	current  derivedKey
	previous *derivedKey
}

// NewKeyring derives the credential keys. current is required; previous may be
// empty.
func NewKeyring(current, previous string) (*Keyring, error) {
	return newKeyring(current, previous, hkdfInfo)
}

// NewRegistryKeyring derives the catalog-registry keys (Phase B of #31 / D4).
// It uses a derivation domain distinct from credentials so the two keyrings
// never open each other's envelopes. The Trino plugin must mirror this
// derivation (HKDF-SHA256, info "baleia/registry/v1") to decrypt values.
func NewRegistryKeyring(current, previous string) (*Keyring, error) {
	return newKeyring(current, previous, registryHKDFInfo)
}

func newKeyring(current, previous, info string) (*Keyring, error) {
	cur, err := deriveKey(current, info)
	if err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	k := &Keyring{current: cur}
	if previous != "" {
		prev, err := deriveKey(previous, info)
		if err != nil {
			return nil, fmt.Errorf("previous encryption key: %w", err)
		}
		if prev.id == cur.id {
			return nil, errors.New("previous encryption key must differ from the current one")
		}
		k.previous = &prev
	}
	return k, nil
}

func deriveKey(secret, info string) (derivedKey, error) {
	if len(secret) < MinKeyLen {
		return derivedKey{}, fmt.Errorf("must be at least %d characters", MinKeyLen)
	}
	raw, err := hkdf.Key(sha256.New, []byte(secret), nil, info, 32)
	if err != nil {
		return derivedKey{}, err
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return derivedKey{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return derivedKey{}, err
	}
	sum := sha256.Sum256(raw)
	return derivedKey{id: hex.EncodeToString(sum[:])[:keyIDHexChars], aead: aead}, nil
}

// Seal encrypts plaintext under the current key and returns
// "v1:<key-id>:<base64(nonce|ciphertext)>". aad binds the envelope to its row.
func (k *Keyring) Seal(plaintext []byte, aad string) (string, error) {
	nonce := make([]byte, k.current.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := k.current.aead.Seal(nonce, nonce, plaintext, []byte(aad))
	return envelopeV1 + ":" + k.current.id + ":" + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Open decrypts an envelope produced by Seal with the same aad.
func (k *Keyring) Open(envelope, aad string) ([]byte, error) {
	if !strings.HasPrefix(envelope, envelopeV1+":") {
		return nil, ErrLegacyFormat
	}
	parts := strings.SplitN(envelope, ":", 3)
	if len(parts) != 3 {
		return nil, ErrCorrupt
	}
	key := k.lookup(parts[1])
	if key == nil {
		return nil, ErrUnknownKey
	}
	raw, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(raw) < key.aead.NonceSize() {
		return nil, ErrCorrupt
	}
	nonce, ciphertext := raw[:key.aead.NonceSize()], raw[key.aead.NonceSize():]
	plain, err := key.aead.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, ErrCorrupt
	}
	return plain, nil
}

// IsCurrent reports whether envelope was sealed with the current key. Anything
// else (legacy, previous key, unknown) needs re-sealing during a rotation.
func (k *Keyring) IsCurrent(envelope string) bool {
	parts := strings.SplitN(envelope, ":", 3)
	return len(parts) == 3 && parts[0] == envelopeV1 && parts[1] == k.current.id
}

func (k *Keyring) lookup(id string) *derivedKey {
	if id == k.current.id {
		return &k.current
	}
	if k.previous != nil && id == k.previous.id {
		return k.previous
	}
	return nil
}

// OpenLegacy decrypts a pre-envelope value (AES-256-GCM under sha256(secret),
// no AAD). It exists only for the one-shot `migrate-credentials` command and
// must never be called while serving requests.
func OpenLegacy(encoded, secret string) ([]byte, error) {
	ciphertext, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < aead.NonceSize() {
		return nil, io.ErrUnexpectedEOF
	}
	nonce, ciphertext := ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():]
	return aead.Open(nil, nonce, ciphertext, nil)
}
