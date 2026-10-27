package setup

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

// fakeStore models the setup lock with a mutex held for the transaction.
type fakeStore struct {
	mu    sync.Mutex
	users []db.User
}

func (f *fakeStore) HasUsers(context.Context) (bool, error) { return len(f.users) > 0, nil }

// LockFirstAdminSetup is taken by the Tx below; inside it is a no-op.
func (f *fakeStore) LockFirstAdminSetup(context.Context) error { return nil }

func (f *fakeStore) CreateUser(_ context.Context, arg db.CreateUserParams) (db.User, error) {
	u := db.User{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Name: arg.Name, Email: arg.Email, PasswordHash: arg.PasswordHash, Role: arg.Role, AuthProvider: arg.AuthProvider}
	f.users = append(f.users, u)
	return u, nil
}

func (f *fakeStore) tx(_ context.Context, fn func(Store) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(f)
}

func TestCreateFirstAdmin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &fakeStore{}
	s := New(st, st.tx, bcrypt.MinCost)

	if ok, _ := s.Configured(ctx); ok {
		t.Fatal("a fresh installation is not configured")
	}
	u, err := s.CreateFirstAdmin(ctx, "Ana", "ana@x.io", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "admin" || u.AuthProvider != "local" || u.PasswordHash == nil {
		t.Fatalf("user = %+v", u)
	}
	if bcrypt.CompareHashAndPassword([]byte(*u.PasswordHash), []byte("correct horse")) != nil {
		t.Error("the password must be stored hashed")
	}
	if ok, _ := s.Configured(ctx); !ok {
		t.Error("configured after the first admin")
	}
	if _, err := s.CreateFirstAdmin(ctx, "Bia", "bia@x.io", "correct horse"); !errors.Is(err, ErrAlreadySetUp) {
		t.Errorf("second setup = %v, want ErrAlreadySetUp", err)
	}
}

// TestCreateFirstAdmin_Concurrent: of concurrent setups exactly one wins (#375).
func TestCreateFirstAdmin_Concurrent(t *testing.T) {
	t.Parallel()
	st := &fakeStore{}
	s := New(st, st.tx, bcrypt.MinCost)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.CreateFirstAdmin(context.Background(), "Admin", "admin@x.io", "correct horse")
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ErrAlreadySetUp):
			t.Errorf("err = %v", err)
		}
	}
	if won != 1 || len(st.users) != 1 {
		t.Fatalf("won = %d, users = %d; want exactly one", won, len(st.users))
	}
}
