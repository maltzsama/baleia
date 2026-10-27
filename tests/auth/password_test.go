package auth_test

import (
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
)

func TestHashPassword(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword("my-secret-password")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if hash == "my-secret-password" {
		t.Fatal("hash should not equal plaintext password")
	}
}

func TestCheckPasswordSuccess(t *testing.T) {
	t.Parallel()
	hash, _ := auth.HashPassword("my-password")
	if !auth.CheckPassword(hash, "my-password") {
		t.Fatal("CheckPassword should return true for correct password")
	}
}

func TestCheckPasswordWrong(t *testing.T) {
	t.Parallel()
	hash, _ := auth.HashPassword("my-password")
	if auth.CheckPassword(hash, "wrong-password") {
		t.Fatal("CheckPassword should return false for wrong password")
	}
}

func TestHashPasswordDeterministic(t *testing.T) {
	t.Parallel()
	h1, _ := auth.HashPassword("same-password")
	h2, _ := auth.HashPassword("same-password")
	if h1 == h2 {
		t.Fatal("bcrypt hashes of same password should differ due to salt")
	}
}
