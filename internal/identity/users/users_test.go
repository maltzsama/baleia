package users

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/mail"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

// fakeStore is one in-memory database for both the plain and the
// transactional writes; failTx makes a transaction fail after its writes.
type fakeStore struct {
	users   map[[16]byte]db.User
	resets  map[string]pgtype.UUID
	revoked map[[16]byte]bool
	keysDel map[[16]byte]bool
	deleteE error
}

func newStore() *fakeStore {
	return &fakeStore{users: map[[16]byte]db.User{}, resets: map[string]pgtype.UUID{}, revoked: map[[16]byte]bool{}, keysDel: map[[16]byte]bool{}}
}

func (f *fakeStore) GetUserByID(_ context.Context, id pgtype.UUID) (db.User, error) {
	if u, ok := f.users[id.Bytes]; ok {
		return u, nil
	}
	return db.User{}, pgx.ErrNoRows
}

func (f *fakeStore) GetUserByEmail(_ context.Context, email string) (db.User, error) {
	for _, u := range f.users {
		if u.Email == email {
			return u, nil
		}
	}
	return db.User{}, pgx.ErrNoRows
}

func (f *fakeStore) ListUsers(context.Context) ([]db.User, error) {
	out := make([]db.User, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeStore) ListDirectoryReconcileRuns(context.Context, db.ListDirectoryReconcileRunsParams) ([]db.DirectoryReconcileRun, error) {
	return nil, nil
}

func (f *fakeStore) UpdateUserRole(_ context.Context, arg db.UpdateUserRoleParams) error {
	u := f.users[arg.ID.Bytes]
	u.Role = arg.Role
	f.users[arg.ID.Bytes] = u
	return nil
}

func (f *fakeStore) DeleteUser(_ context.Context, id pgtype.UUID) error {
	if f.deleteE != nil {
		return f.deleteE
	}
	delete(f.users, id.Bytes)
	return nil
}

func (f *fakeStore) ReinstateUser(_ context.Context, id pgtype.UUID) (db.User, error) {
	u := f.users[id.Bytes]
	u.Status, u.SuspendedReason = auth.StatusActive, nil
	f.users[id.Bytes] = u
	return u, nil
}

func (f *fakeStore) CreateUser(_ context.Context, arg db.CreateUserParams) (db.User, error) {
	u := db.User{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Name: arg.Name, Email: arg.Email, Role: arg.Role, AuthProvider: arg.AuthProvider, PasswordHash: arg.PasswordHash, Status: auth.StatusActive}
	f.users[u.ID.Bytes] = u
	return u, nil
}

func (f *fakeStore) CreatePasswordResetToken(_ context.Context, arg db.CreatePasswordResetTokenParams) error {
	f.resets[arg.TokenHash] = arg.UserID
	return nil
}

func (f *fakeStore) SuspendUser(_ context.Context, arg db.SuspendUserParams) (db.User, error) {
	u := f.users[arg.ID.Bytes]
	u.Status, u.SuspendedReason = auth.StatusSuspended, arg.SuspendedReason
	f.users[arg.ID.Bytes] = u
	return u, nil
}

func (f *fakeStore) RevokeUserRefreshTokens(_ context.Context, id pgtype.UUID) error {
	f.revoked[id.Bytes] = true
	return nil
}

func (f *fakeStore) DeleteAPIKeysByUser(_ context.Context, id pgtype.UUID) error {
	f.keysDel[id.Bytes] = true
	return nil
}

func (f *fakeStore) tx(_ context.Context, fn func(TxStore) error) error { return fn(f) }

type fakeMailer struct{ sent []map[string]any }

func (m *fakeMailer) Send(_ context.Context, _, _ string, template mail.Template, data map[string]any) error {
	if template != mail.TemplateInvite {
		return errors.New("unexpected template")
	}
	m.sent = append(m.sent, data)
	return nil
}

type fakeSessions struct{ revoked []pgtype.UUID }

func (s *fakeSessions) RevokeAll(_ context.Context, id pgtype.UUID) {
	s.revoked = append(s.revoked, id)
}

func setup() (*Service, *fakeStore, *fakeMailer, *fakeSessions) {
	st, m, ss := newStore(), &fakeMailer{}, &fakeSessions{}
	return New(st, st.tx, m, ss, Config{PublicURL: "https://baleia.example", DefaultLocale: "en", PasswordCost: bcrypt.MinCost}), st, m, ss
}

func TestInvite(t *testing.T) {
	ctx := context.Background()
	s, st, m, _ := setup()
	u, err := s.Invite(ctx, "ana@x.io", "member")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "ana@x.io" || u.Role != "member" || u.AuthProvider != "local" || u.PasswordHash == nil {
		t.Fatalf("invited = %+v", u)
	}
	if len(m.sent) != 1 {
		t.Fatalf("sent = %d e-mails, want 1", len(m.sent))
	}
	link, err := url.Parse(m.sent[0]["InviteURL"].(string))
	if err != nil || link.Path != "/accept-invite" {
		t.Fatalf("link = %v, %v", link, err)
	}
	if st.resets[auth.HashToken(link.Query().Get("token"))] != u.ID {
		t.Error("the link's token hash must be stored for the account")
	}

	if _, err := s.Invite(ctx, "ana@x.io", "admin"); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("second invite = %v, want ErrEmailTaken", err)
	}
}

func TestChangeRole(t *testing.T) {
	ctx := context.Background()
	s, st, _, ss := setup()
	u, _ := st.CreateUser(ctx, db.CreateUserParams{Email: "ana@x.io", Role: "member"})
	before, after, err := s.ChangeRole(ctx, u.ID, "admin")
	if err != nil || before.Role != "member" || after.Role != "admin" {
		t.Fatalf("ChangeRole = %q → %q, %v", before.Role, after.Role, err)
	}
	if len(ss.revoked) != 1 {
		t.Error("a role change ends the account's sessions")
	}
	if _, _, err := s.ChangeRole(ctx, pgtype.UUID{Bytes: uuid.New(), Valid: true}, "admin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown = %v, want ErrNotFound", err)
	}
}

func TestRemove(t *testing.T) {
	ctx := context.Background()
	s, st, _, ss := setup()
	u, _ := st.CreateUser(ctx, db.CreateUserParams{Email: "ana@x.io"})

	refused := errors.New("still referenced")
	st.deleteE = refused
	if err := s.Remove(ctx, u.ID); !errors.Is(err, refused) {
		t.Fatalf("refused delete = %v, want the database error", err)
	}
	if len(ss.revoked) != 0 {
		t.Fatal("a refused delete must not end sessions")
	}
	st.deleteE = nil
	if err := s.Remove(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.users[u.ID.Bytes]; ok || len(ss.revoked) != 1 {
		t.Error("the account is deleted and its sessions ended")
	}
	if err := s.Remove(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second remove = %v, want ErrNotFound", err)
	}
}

func TestSuspendReinstate(t *testing.T) {
	ctx := context.Background()
	s, st, _, _ := setup()
	u, _ := st.CreateUser(ctx, db.CreateUserParams{Email: "ana@x.io"})

	for _, c := range []struct{ asked, recorded string }{
		{"policy", ReasonPolicy},
		{"admin", ReasonAdmin},
		{"anything else", ReasonAdmin},
	} {
		_, after, recorded, err := s.Suspend(ctx, u.ID, c.asked)
		if err != nil || recorded != c.recorded || after.Status != auth.StatusSuspended || *after.SuspendedReason != c.recorded {
			t.Fatalf("Suspend(%q) = %+v, %q, %v", c.asked, after, recorded, err)
		}
	}
	if !st.revoked[u.ID.Bytes] || !st.keysDel[u.ID.Bytes] {
		t.Error("a suspension ends sessions and deletes API keys")
	}

	before, after, err := s.Reinstate(ctx, u.ID)
	if err != nil || before.Status != auth.StatusSuspended || after.Status != auth.StatusActive {
		t.Fatalf("Reinstate = %q → %q, %v", before.Status, after.Status, err)
	}
}

type failingMailer struct{}

func (failingMailer) Send(context.Context, string, string, mail.Template, map[string]any) error {
	return errors.New("smtp down")
}

// TestInvite_FailedMailNeverLogsTheToken: the invitation link is a bearer
// credential; a failed send is logged without it.
func TestInvite_FailedMailNeverLogsTheToken(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	st := newStore()
	s := New(st, st.tx, failingMailer{}, &fakeSessions{}, Config{PublicURL: "https://baleia.example", PasswordCost: bcrypt.MinCost})
	if _, err := s.Invite(context.Background(), "ana@x.io", "member"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "token=") || strings.Contains(logs.String(), "accept-invite") {
		t.Fatalf("log leaks the invitation link: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "failed to send invite email") {
		t.Error("the failed send must still be logged")
	}
}
