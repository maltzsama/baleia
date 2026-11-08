package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestAPIKeysRoundTrip(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:         gormlogger.Discard,
		TranslateError: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	ctx := context.Background()
	hash := "x"
	user := model.User{
		ID:           model.UUID(NewUUID()),
		Name:         "Key Owner",
		Email:        "keys@example.test",
		PasswordHash: &hash,
		Role:         "member",
		AuthProvider: "local",
		Status:       "active",
		Kind:         "human",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := gdb.WithContext(ctx).Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	userID := PGUUID(string(user.ID))

	store := NewAPIKeys(gdb)
	created, err := store.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID:  userID,
		Name:    "ci",
		KeyHash: "hash",
		Prefix:  "bal_",
		Scopes:  []string{"*"},
	})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if !created.ID.Valid || created.CreatedAt.Time.IsZero() {
		t.Fatalf("created = %+v", created)
	}
	if string(created.Prefix) != "bal_" {
		t.Fatalf("prefix = %q, want bal_", created.Prefix)
	}

	keys, err := store.ListAPIKeysByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "ci" {
		t.Fatalf("keys = %+v, want the created key", keys)
	}

	if err := store.DeleteAPIKey(ctx, db.DeleteAPIKeyParams{ID: created.ID, UserID: userID}); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	keys, err = store.ListAPIKeysByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser after delete: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("keys = %+v, want none", keys)
	}
}
