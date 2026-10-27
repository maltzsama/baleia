package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

const (
	keyA = "key-a-0123456789abcdef0123456789abcdef"
	keyB = "key-b-0123456789abcdef0123456789abcdef"
)

func mustKeyring(t *testing.T, current, previous string) *Keyring {
	t.Helper()
	k, err := NewKeyring(current, previous)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return k
}

func TestNewKeyringRejectsWeakKeys(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "short", strings.Repeat("x", MinKeyLen-1)} {
		if _, err := NewKeyring(bad, ""); err == nil {
			t.Errorf("NewKeyring accepted current key %q", bad)
		}
	}
	if _, err := NewKeyring(keyA, "short"); err == nil {
		t.Error("NewKeyring accepted a short previous key")
	}
	if _, err := NewKeyring(keyA, keyA); err == nil {
		t.Error("NewKeyring accepted previous == current")
	}
}

// TestRegistryKeyringIsDomainSeparated proves the registry keyring never opens
// a credential envelope and vice versa, even with the same secret.
func TestRegistryKeyringIsDomainSeparated(t *testing.T) {
	t.Parallel()
	cred := mustKeyring(t, keyA, "")
	reg, err := NewRegistryKeyring(keyA, "")
	if err != nil {
		t.Fatalf("NewRegistryKeyring: %v", err)
	}
	env, err := cred.Seal([]byte("secret"), "aad")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := reg.Open(env, "aad"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("registry keyring opened a credential envelope: %v", err)
	}
	regEnv, err := reg.Seal([]byte("secret"), "")
	if err != nil {
		t.Fatalf("registry Seal: %v", err)
	}
	if _, err := cred.Open(regEnv, ""); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("credential keyring opened a registry envelope: %v", err)
	}
	if !reg.IsCurrent(regEnv) {
		t.Error("registry envelope is not current under its own keyring")
	}
}

func TestSealOpen_RoundTrip(t *testing.T) {
	t.Parallel()
	k := mustKeyring(t, keyA, "")
	env, err := k.Seal([]byte(`{"password":"s3cr3t"}`), "catalog_connections:1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !strings.HasPrefix(env, "v1:") || strings.Contains(env, "s3cr3t") {
		t.Fatalf("bad envelope %q", env)
	}
	got, err := k.Open(env, "catalog_connections:1")
	if err != nil || string(got) != `{"password":"s3cr3t"}` {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestSealIsRandomised(t *testing.T) {
	t.Parallel()
	k := mustKeyring(t, keyA, "")
	a, _ := k.Seal([]byte("x"), "aad")
	b, _ := k.Seal([]byte("x"), "aad")
	if a == b {
		t.Fatal("two seals of the same plaintext are identical (nonce reuse)")
	}
}

func TestOpen_WrongAAD(t *testing.T) {
	t.Parallel()
	k := mustKeyring(t, keyA, "")
	env, _ := k.Seal([]byte("secret"), "catalog_connections:A")
	if _, err := k.Open(env, "catalog_connections:B"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt for another row's AAD, got %v", err)
	}
}

func TestOpen_Tampered(t *testing.T) {
	t.Parallel()
	k := mustKeyring(t, keyA, "")
	env, _ := k.Seal([]byte("secret"), "aad")
	parts := strings.SplitN(env, ":", 3)
	raw, _ := base64.RawStdEncoding.DecodeString(parts[2])
	raw[len(raw)-1] ^= 0xff
	tampered := parts[0] + ":" + parts[1] + ":" + base64.RawStdEncoding.EncodeToString(raw)
	if _, err := k.Open(tampered, "aad"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	for _, malformed := range []string{"v1:onlytwo", "v1:" + parts[1] + ":!!notbase64!!", "v1:" + parts[1] + ":" + "QQ"} {
		if _, err := k.Open(malformed, "aad"); !errors.Is(err, ErrCorrupt) {
			t.Errorf("Open(%q) = %v, want ErrCorrupt", malformed, err)
		}
	}
}

func TestOpen_PreviousKey(t *testing.T) {
	t.Parallel()
	old := mustKeyring(t, keyA, "")
	env, _ := old.Seal([]byte("secret"), "aad")

	rotated := mustKeyring(t, keyB, keyA)
	got, err := rotated.Open(env, "aad")
	if err != nil || string(got) != "secret" {
		t.Fatalf("rotated keyring Open = %q, %v", got, err)
	}

	// Writes always use the current key, and the old keyring cannot read them.
	fresh, _ := rotated.Seal([]byte("secret"), "aad")
	if _, err := old.Open(fresh, "aad"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("old keyring opened a current-key envelope: %v", err)
	}
}

func TestOpen_UnknownKey(t *testing.T) {
	t.Parallel()
	env, _ := mustKeyring(t, keyA, "").Seal([]byte("secret"), "aad")
	if _, err := mustKeyring(t, keyB, "").Open(env, "aad"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("want ErrUnknownKey, got %v", err)
	}
}

// legacySeal reproduces the pre-envelope scheme: AES-256-GCM under sha256(key),
// no AAD, raw base64.
func legacySeal(t *testing.T, plaintext []byte, secret string) string {
	t.Helper()
	hash := sha256.Sum256([]byte(secret))
	block, _ := aes.NewCipher(hash[:])
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, plaintext, nil))
}

func TestOpen_Legacy(t *testing.T) {
	t.Parallel()
	legacy := legacySeal(t, []byte("secret"), "jwt-secret-0123456789")
	if _, err := mustKeyring(t, keyA, "").Open(legacy, "aad"); !errors.Is(err, ErrLegacyFormat) {
		t.Fatalf("request-time Open must refuse legacy values, got %v", err)
	}
	got, err := OpenLegacy(legacy, "jwt-secret-0123456789")
	if err != nil || !bytes.Equal(got, []byte("secret")) {
		t.Fatalf("OpenLegacy = %q, %v", got, err)
	}
	if _, err := OpenLegacy(legacy, "another-secret-0123456789"); err == nil {
		t.Fatal("OpenLegacy opened with the wrong secret")
	}
}
