package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestUsersRoundTrip(t *testing.T) {
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
	store := NewUsers(gdb)
	hash := "x"
	created, err := store.CreateUser(ctx, db.CreateUserParams{
		Name:         "Alice",
		Email:        "alice@example.test",
		PasswordHash: &hash,
		Role:         "member",
		AuthProvider: "local",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.ID.Valid != true || created.Email != "alice@example.test" {
		t.Fatalf("created = %+v", created)
	}

	if _, err := store.GetUserByID(ctx, created.ID); err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if _, err := store.GetUserByEmail(ctx, "alice@example.test"); err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	users, err := store.ListUsers(ctx)
	if err != nil || len(users) == 0 {
		t.Fatalf("ListUsers = %v, %v", users, err)
	}

	if _, err := store.CreateUser(ctx, db.CreateUserParams{
		Name:         "Alice Again",
		Email:        "alice@example.test",
		PasswordHash: &hash,
		Role:         "member",
		AuthProvider: "local",
	}); err == nil {
		t.Fatal("duplicate e-mail accepted")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
			t.Fatalf("duplicate error = %v, want SQLSTATE %s", err, uniqueViolation)
		}
	}

	reason := "admin"
	suspended, err := store.SuspendUser(ctx, db.SuspendUserParams{ID: created.ID, SuspendedReason: &reason})
	if err != nil || suspended.Status != "suspended" {
		t.Fatalf("SuspendUser = %+v, %v", suspended, err)
	}
	reinstated, err := store.ReinstateUser(ctx, created.ID)
	if err != nil || reinstated.Status != "active" {
		t.Fatalf("ReinstateUser = %+v, %v", reinstated, err)
	}

	if err := store.UpdateUserRole(ctx, db.UpdateUserRoleParams{ID: created.ID, Role: "admin"}); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}
	if err := store.DeleteUser(ctx, created.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := store.GetUserByID(ctx, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetUserByID after delete = %v, want pgx.ErrNoRows", err)
	}
}
