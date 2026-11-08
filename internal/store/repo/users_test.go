package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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
	updated, err := store.GetUserByID(ctx, created.ID)
	if err != nil || updated.Role != "admin" {
		t.Fatalf("updated user = %+v, %v; want admin role", updated, err)
	}
	if err := store.DeleteUser(ctx, created.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := store.GetUserByID(ctx, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetUserByID after delete = %v, want pgx.ErrNoRows", err)
	}
}

func TestPasswordResetTokenIsSingleUse(t *testing.T) {
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
	user, err := store.CreateUser(ctx, db.CreateUserParams{
		Name:         "Reset",
		Email:        "reset@example.test",
		PasswordHash: &hash,
		Role:         "member",
		AuthProvider: "local",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	const token = "reset-token-hash"
	if err := store.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		TokenHash: token,
		UserID:    user.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}

	consumed, err := store.ConsumePasswordResetToken(ctx, token)
	if err != nil || !consumed.UsedAt.Valid {
		t.Fatalf("ConsumePasswordResetToken = %+v, %v", consumed, err)
	}
	if _, err := store.ConsumePasswordResetToken(ctx, token); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("replayed token = %v, want pgx.ErrNoRows", err)
	}
}

func TestPasswordResetTokenConcurrentConsume(t *testing.T) {
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
	user, err := store.CreateUser(ctx, db.CreateUserParams{
		Name:         "Concurrent",
		Email:        "concurrent@example.test",
		PasswordHash: &hash,
		Role:         "member",
		AuthProvider: "local",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	const token = "concurrent-token-hash"
	if err := store.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		TokenHash: token,
		UserID:    user.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := store.ConsumePasswordResetToken(ctx, token)
			errs <- err
		}()
	}
	close(start)

	var consumed, gone int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			consumed++
		case errors.Is(err, pgx.ErrNoRows):
			gone++
		default:
			t.Fatalf("unexpected consume error: %v", err)
		}
	}
	if consumed != 1 || gone != 1 {
		t.Fatalf("consumed=%d noRows=%d, want exactly one of each", consumed, gone)
	}
}
