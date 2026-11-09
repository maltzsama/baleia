package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"gorm.io/gorm"
)

func TestAPIKeysRoundTrip(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)

	ctx := context.Background()
	store := NewAPIKeys(gdb)
	first := createAPIKeyUser(t, gdb, "first@example.test")
	second := createAPIKeyUser(t, gdb, "second@example.test")
	firstID := PGUUID(string(first.ID))
	secondID := PGUUID(string(second.ID))

	firstKey, err := store.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID:  firstID,
		Name:    "first",
		KeyHash: "hash-first",
		Prefix:  "bal_",
		Scopes:  []string{"*"},
	})
	if err != nil {
		t.Fatalf("CreateAPIKey(first): %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID:  secondID,
		Name:    "second",
		KeyHash: "hash-second",
		Prefix:  "bal_",
		Scopes:  []string{"*"},
	}); err != nil {
		t.Fatalf("CreateAPIKey(second): %v", err)
	}

	// Each listing sees only its owner's key.
	firstKeys, err := store.ListAPIKeysByUser(ctx, firstID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser(first): %v", err)
	}
	if len(firstKeys) != 1 || firstKeys[0].Name != "first" {
		t.Fatalf("first keys = %+v, want only first", firstKeys)
	}
	secondKeys, err := store.ListAPIKeysByUser(ctx, secondID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser(second): %v", err)
	}
	if len(secondKeys) != 1 || secondKeys[0].Name != "second" {
		t.Fatalf("second keys = %+v, want only second", secondKeys)
	}

	// The wrong owner cannot delete the key.
	if err := store.DeleteAPIKey(ctx, db.DeleteAPIKeyParams{ID: firstKey.ID, UserID: secondID}); err != nil {
		t.Fatalf("DeleteAPIKey(wrong owner): %v", err)
	}
	firstKeys, err = store.ListAPIKeysByUser(ctx, firstID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser after wrong delete: %v", err)
	}
	if len(firstKeys) != 1 {
		t.Fatalf("first keys after wrong-owner delete = %+v, want the key", firstKeys)
	}

	if err := store.DeleteAPIKey(ctx, db.DeleteAPIKeyParams{ID: firstKey.ID, UserID: firstID}); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	firstKeys, err = store.ListAPIKeysByUser(ctx, firstID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser after delete: %v", err)
	}
	if len(firstKeys) != 0 {
		t.Fatalf("first keys = %+v, want none", firstKeys)
	}
}

func createAPIKeyUser(t *testing.T, gdb *gorm.DB, email string) model.User {
	t.Helper()
	hash := "x"
	user := model.User{
		ID:           model.UUID(NewUUID()),
		Name:         email,
		Email:        model.Text(email),
		PasswordHash: &hash,
		Role:         "member",
		AuthProvider: "local",
		Status:       "active",
		Kind:         "human",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := gdb.Create(&user).Error; err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return user
}
