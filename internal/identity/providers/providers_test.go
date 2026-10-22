package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeStore struct {
	providers map[[16]byte]db.AuthProvider
	linked    map[[16]byte]int64
	detached  map[[16]byte]bool
	// raceLinked makes the delete fail as if a user had been linked after
	// the count.
	raceLinked bool
}

func newStore() *fakeStore {
	return &fakeStore{providers: map[[16]byte]db.AuthProvider{}, linked: map[[16]byte]int64{}, detached: map[[16]byte]bool{}}
}

func (f *fakeStore) ListAllAuthProviders(context.Context) ([]db.AuthProvider, error) {
	out := make([]db.AuthProvider, 0, len(f.providers))
	for _, ap := range f.providers {
		out = append(out, ap)
	}
	return out, nil
}

func (f *fakeStore) ListAuthProviders(ctx context.Context) ([]db.AuthProvider, error) {
	all, _ := f.ListAllAuthProviders(ctx)
	var out []db.AuthProvider
	for _, ap := range all {
		if ap.Enabled {
			out = append(out, ap)
		}
	}
	return out, nil
}

func (f *fakeStore) GetAuthProviderByID(_ context.Context, id pgtype.UUID) (db.AuthProvider, error) {
	if ap, ok := f.providers[id.Bytes]; ok {
		return ap, nil
	}
	return db.AuthProvider{}, pgx.ErrNoRows
}

func (f *fakeStore) CountUsersByAuthProvider(_ context.Context, id pgtype.UUID) (int64, error) {
	return f.linked[id.Bytes], nil
}

func (f *fakeStore) CreateAuthProvider(_ context.Context, arg db.CreateAuthProviderParams) (db.AuthProvider, error) {
	ap := db.AuthProvider{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Name: arg.Name, Type: arg.Type, ConfigEnc: arg.ConfigEnc, Enabled: arg.Enabled}
	f.providers[ap.ID.Bytes] = ap
	return ap, nil
}

func (f *fakeStore) UpdateAuthProvider(_ context.Context, arg db.UpdateAuthProviderParams) (db.AuthProvider, error) {
	ap := f.providers[arg.ID.Bytes]
	ap.Name, ap.ConfigEnc, ap.Enabled = arg.Name, arg.ConfigEnc, arg.Enabled
	f.providers[arg.ID.Bytes] = ap
	return ap, nil
}

func (f *fakeStore) RevokeRefreshTokensByAuthProvider(_ context.Context, id pgtype.UUID) error {
	f.detached[id.Bytes] = true
	return nil
}
func (f *fakeStore) DeleteAPIKeysByAuthProvider(context.Context, pgtype.UUID) error { return nil }
func (f *fakeStore) SuspendUsersByAuthProvider(context.Context, pgtype.UUID) error  { return nil }

func (f *fakeStore) DeleteAuthProvider(_ context.Context, id pgtype.UUID) error {
	if f.raceLinked {
		// A login linked a user after the count: the database refuses.
		return &pgconn.PgError{Code: "23514", ConstraintName: "chk_oidc_auth"}
	}
	delete(f.providers, id.Bytes)
	return nil
}

func (f *fakeStore) tx(_ context.Context, fn func(TxStore) error) error { return fn(f) }

func TestCreate_SealsWithTheGeneratedID(t *testing.T) {
	st := newStore()
	s := New(st, st.tx)
	var sealedFor pgtype.UUID
	ap, err := s.Create(context.Background(), "corp", "oidc", true, func(id pgtype.UUID) (string, error) {
		sealedFor = id
		return "sealed:" + id.String(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sealedFor != ap.ID || ap.ConfigEnc != "sealed:"+ap.ID.String() {
		t.Fatalf("provider = %+v; the config must be sealed for its own id", ap)
	}

	failing := errors.New("cannot seal")
	if _, err := s.Create(context.Background(), "bad", "ldap", true, func(pgtype.UUID) (string, error) { return "", failing }); !errors.Is(err, failing) {
		t.Errorf("seal failure = %v", err)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	seal := func(id pgtype.UUID) (string, error) { return id.String(), nil }

	free, _ := s.Create(ctx, "free", "oidc", true, seal)
	if err := s.Delete(ctx, free.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, free.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted provider = %v, want ErrNotFound", err)
	}

	used, _ := s.Create(ctx, "used", "ldap", true, seal)
	st.linked[used.ID.Bytes] = 2
	if err := s.Delete(ctx, used.ID, false); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete with linked users = %v, want ErrInUse", err)
	}
	if err := s.Delete(ctx, used.ID, true); err != nil {
		t.Fatal(err)
	}
	if !st.detached[used.ID.Bytes] {
		t.Error("suspending users first ends their sessions")
	}
	if err := s.Delete(ctx, used.ID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

func TestListEnabled(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	seal := func(id pgtype.UUID) (string, error) { return id.String(), nil }
	_, _ = s.Create(ctx, "on", "oidc", true, seal)
	_, _ = s.Create(ctx, "off", "oidc", false, seal)
	if on, _ := s.ListEnabled(ctx); len(on) != 1 || on[0].Name != "on" {
		t.Errorf("enabled = %+v", on)
	}
	if all, _ := s.List(ctx); len(all) != 2 {
		t.Errorf("all = %d, want 2", len(all))
	}
}

type brokenStore struct{ *fakeStore }

var errDown = errors.New("connection refused")

func (brokenStore) GetAuthProviderByID(context.Context, pgtype.UUID) (db.AuthProvider, error) {
	return db.AuthProvider{}, errDown
}

// TestGet_StoreFailureIsNotNotFound: a database outage is an error, never a
// missing provider.
func TestGet_StoreFailureIsNotNotFound(t *testing.T) {
	st := brokenStore{newStore()}
	s := New(st, st.tx)
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := s.Get(context.Background(), id); !errors.Is(err, errDown) || errors.Is(err, ErrNotFound) {
		t.Errorf("Get = %v, want the store error", err)
	}
	if err := s.Delete(context.Background(), id, true); !errors.Is(err, errDown) {
		t.Errorf("Delete = %v, want the store error", err)
	}
}

// TestDelete_UserLinkedDuringDelete: a user linked between the count and the
// delete is a conflict, not a server error.
func TestDelete_UserLinkedDuringDelete(t *testing.T) {
	st := newStore()
	s := New(st, st.tx)
	ap, _ := s.Create(context.Background(), "p", "oidc", true, func(id pgtype.UUID) (string, error) { return id.String(), nil })
	st.raceLinked = true
	if err := s.Delete(context.Background(), ap.ID, false); !errors.Is(err, ErrInUse) {
		t.Fatalf("Delete = %v, want ErrInUse", err)
	}
}
