package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

// TestBearerContext (#653): a Baleia access token authenticates as before; a
// token that is not one is offered to the service account authenticator,
// which signs the service account in or refuses.
func TestBearerContext(t *testing.T) {
	t.Parallel()
	const secret = "test-secret-0123456789abcdef-0123456789"
	user := uuid.NewString()
	baleia, err := auth.NewAccessTokenWithTTL(user, "member", secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sa := db.User{ID: db.UUID{Bytes: uuid.New(), Valid: true}, Role: "member", Kind: "service_account"}
	calls := 0
	authSA := func(_ context.Context, raw string) (db.User, error) {
		calls++
		if raw == "client-token" {
			return sa, nil
		}
		return db.User{}, errors.New("no")
	}

	ctx, ok := bearerContext(context.Background(), baleia, secret, authSA)
	if !ok || auth.UserIDFromContext(ctx) != user || calls != 0 {
		t.Fatalf("Baleia token: ok=%t user=%q calls=%d", ok, auth.UserIDFromContext(ctx), calls)
	}
	ctx, ok = bearerContext(context.Background(), "client-token", secret, authSA)
	if !ok || auth.UserIDFromContext(ctx) != sa.ID.String() {
		t.Fatalf("client token: ok=%t user=%q", ok, auth.UserIDFromContext(ctx))
	}
	if _, ok := bearerContext(context.Background(), "garbage", secret, authSA); ok {
		t.Error("an unknown token was accepted")
	}
	if _, ok := bearerContext(context.Background(), "client-token", secret, nil); ok {
		t.Error("without an authenticator only Baleia tokens are accepted")
	}
}
