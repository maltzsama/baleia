package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSign(t *testing.T) {
	t.Parallel()
	secret := []byte("s3cret")
	body := []byte(`{"type":"execution.finished"}`)
	want := hmac.New(sha256.New, secret)
	want.Write(body)
	expected := "sha256=" + hex.EncodeToString(want.Sum(nil))
	if got := Sign(secret, body); got != expected {
		t.Fatalf("Sign = %q, want %q", got, expected)
	}
	if Sign(secret, []byte("other")) == expected {
		t.Fatal("a different body must sign differently")
	}
}
