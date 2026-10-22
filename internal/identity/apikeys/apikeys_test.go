package apikeys

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeStore struct {
	keys      []db.ApiKey
	deleteErr error
}

func (f *fakeStore) ListAPIKeysByUser(_ context.Context, user pgtype.UUID) ([]db.ApiKey, error) {
	var out []db.ApiKey
	for _, k := range f.keys {
		if k.UserID == user {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeStore) CreateAPIKey(_ context.Context, arg db.CreateAPIKeyParams) (db.ApiKey, error) {
	k := db.ApiKey{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, UserID: arg.UserID, Name: arg.Name, KeyHash: arg.KeyHash, Prefix: arg.Prefix, Scopes: arg.Scopes, ExpiresAt: arg.ExpiresAt}
	f.keys = append(f.keys, k)
	return k, nil
}

func (f *fakeStore) DeleteAPIKey(context.Context, db.DeleteAPIKeyParams) error { return f.deleteErr }

func TestCreate_StoresOnlyTheHash(t *testing.T) {
	st := &fakeStore{}
	s := New(st)
	user := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	exp := time.Now().Add(time.Hour)

	key, secret, err := s.Create(context.Background(), user, "ci", &exp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "bal_") || len(secret) < 40 {
		t.Fatalf("secret = %q", secret)
	}
	if key.KeyHash != auth.HashAPIKey(secret) || key.KeyHash == secret || key.Prefix != secret[:8] {
		t.Errorf("stored = %+v; want the hash and the prefix, never the secret", key)
	}
	if !key.ExpiresAt.Valid || !key.ExpiresAt.Time.Equal(exp) || len(key.Scopes) != 1 || key.Scopes[0] != "*" {
		t.Errorf("stored = %+v", key)
	}
	if keys, _ := s.List(context.Background(), user); len(keys) != 1 {
		t.Errorf("list = %d keys, want 1", len(keys))
	}

	_, other, _ := s.Create(context.Background(), user, "never", nil)
	if other == secret {
		t.Error("every key gets its own secret")
	}
}

func TestDelete(t *testing.T) {
	st := &fakeStore{}
	s := New(st)
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if err := s.Delete(context.Background(), id, id); err != nil {
		t.Fatal(err)
	}
	down := errors.New("db down")
	st.deleteErr = down
	if err := s.Delete(context.Background(), id, id); !errors.Is(err, down) {
		t.Errorf("failed delete = %v, want the store error", err)
	}
}
