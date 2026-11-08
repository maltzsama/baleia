package repo

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestProvidersRoundTrip(t *testing.T) {
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
	store := NewProviders(gdb)
	created, err := store.CreateAuthProvider(ctx, db.CreateAuthProviderParams{
		Name: "corp", Type: "oidc", ConfigEnc: "sealed", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAuthProvider: %v", err)
	}
	if _, err := store.GetAuthProviderByID(ctx, created.ID); err != nil {
		t.Fatalf("GetAuthProviderByID: %v", err)
	}
	if list, err := store.ListAuthProviders(ctx); err != nil || len(list) != 1 {
		t.Fatalf("ListAuthProviders = %v, %v", list, err)
	}
	if _, err := store.UpdateAuthProvider(ctx, db.UpdateAuthProviderParams{
		ID: created.ID, Name: "corp2", ConfigEnc: "sealed2", Enabled: false,
	}); err != nil {
		t.Fatalf("UpdateAuthProvider: %v", err)
	}
	if list, err := store.ListAuthProviders(ctx); err != nil || len(list) != 0 {
		t.Fatalf("disabled provider still listed: %v, %v", list, err)
	}
	if list, err := store.ListAllAuthProviders(ctx); err != nil || len(list) != 1 {
		t.Fatalf("ListAllAuthProviders = %v, %v", list, err)
	}

	hash := "x"
	external := "ext-1"
	user, err := NewUsers(gdb).CreateUser(ctx, db.CreateUserParams{
		Name: "SSO User", Email: "sso@example.test", PasswordHash: &hash,
		Role: "member", AuthProvider: "oidc", AuthProviderID: created.ID, ExternalID: &external,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if n, err := store.CountUsersByAuthProvider(ctx, created.ID); err != nil || n != 1 {
		t.Fatalf("CountUsersByAuthProvider = %d, %v", n, err)
	}

	if err := store.SuspendUsersByAuthProvider(ctx, created.ID); err != nil {
		t.Fatalf("SuspendUsersByAuthProvider: %v", err)
	}
	detached, err := NewUsers(gdb).GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if detached.Status != "suspended" || detached.AuthProvider != "local" || detached.AuthProviderID.Valid {
		t.Fatalf("detached user = %+v", detached)
	}

	if err := store.DeleteAuthProvider(ctx, created.ID); err != nil {
		t.Fatalf("DeleteAuthProvider: %v", err)
	}
}
