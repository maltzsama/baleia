package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/crypto"
)

func TestEncodeCredentialsNil(t *testing.T) {
	got, err := encodeCredentials(nil, "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil for nil credentials, got %q", *got)
	}
}

// TestEncodeCredentialsEncrypts pins the security invariant of the update path:
// the stored value is ciphertext that never contains the secret in cleartext,
// and it round-trips back to the original credentials.
func TestEncodeCredentialsEncrypts(t *testing.T) {
	key := "test-key"
	creds := map[string]interface{}{
		"username": "alice",
		"password": "s3cr3t-value",
	}

	enc, err := encodeCredentials(creds, key)
	if err != nil {
		t.Fatalf("encodeCredentials: %v", err)
	}
	if enc == nil {
		t.Fatal("want non-nil encrypted value")
	}
	if strings.Contains(*enc, "s3cr3t-value") || strings.Contains(*enc, "alice") {
		t.Fatalf("stored value leaks the secret in cleartext: %q", *enc)
	}

	plain := crypto.TryDecrypt(*enc, key)
	var decoded map[string]interface{}
	if err := json.Unmarshal(plain, &decoded); err != nil {
		t.Fatalf("decrypt/unmarshal failed: %v (plain=%q)", err, string(plain))
	}
	if decoded["username"] != "alice" || decoded["password"] != "s3cr3t-value" {
		t.Fatalf("round-trip mismatch: %v", decoded)
	}
}

// TestEncodeCredentialsWrongKeyFailsClosed ensures a wrong key cannot recover
// the secret: TryDecrypt falls back to the raw ciphertext, which must not
// contain the secret in cleartext and must not parse as the original JSON.
func TestEncodeCredentialsWrongKeyFailsClosed(t *testing.T) {
	creds := map[string]interface{}{"password": "s3cr3t-value"}
	enc, err := encodeCredentials(creds, "right-key")
	if err != nil {
		t.Fatalf("encodeCredentials: %v", err)
	}
	raw := crypto.TryDecrypt(*enc, "wrong-key")

	if strings.Contains(string(raw), "s3cr3t-value") {
		t.Fatalf("wrong key recovered the secret in cleartext: %q", string(raw))
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err == nil {
		t.Fatalf("decrypt with the wrong key unexpectedly produced valid credentials: %v", decoded)
	}
}
