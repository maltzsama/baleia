package passwordreset

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/mail"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

type fakeStore struct {
	users     map[string]db.User
	resets    map[string]pgtype.UUID // token hash → user, removed when consumed
	passwords map[[16]byte]string
}

func (f *fakeStore) GetUserByEmail(_ context.Context, email string) (db.User, error) {
	u, ok := f.users[email]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeStore) CreatePasswordResetToken(_ context.Context, arg db.CreatePasswordResetTokenParams) error {
	f.resets[arg.TokenHash] = arg.UserID
	return nil
}

func (f *fakeStore) ConsumePasswordResetToken(_ context.Context, hash string) (db.PasswordResetToken, error) {
	user, ok := f.resets[hash]
	if !ok {
		return db.PasswordResetToken{}, pgx.ErrNoRows
	}
	delete(f.resets, hash)
	return db.PasswordResetToken{UserID: user, TokenHash: hash}, nil
}

func (f *fakeStore) UpdateUserPassword(_ context.Context, arg db.UpdateUserPasswordParams) error {
	f.passwords[arg.ID.Bytes] = *arg.PasswordHash
	return nil
}

type sentMail struct {
	to, locale string
	template   mail.Template
	data       map[string]any
}

type fakeMailer struct {
	sent []sentMail
	err  error
	// release, when set, holds every send until it is closed.
	release chan struct{}
}

func (m *fakeMailer) Send(_ context.Context, to, locale string, template mail.Template, data map[string]any) error {
	if m.release != nil {
		<-m.release
	}
	m.sent = append(m.sent, sentMail{to, locale, template, data})
	return m.err
}

type fakeSessions struct{ revoked []pgtype.UUID }

func (s *fakeSessions) RevokeAll(_ context.Context, id pgtype.UUID) {
	s.revoked = append(s.revoked, id)
}

func setup(t *testing.T) (*Service, *fakeStore, *fakeMailer, *fakeSessions, db.User) {
	t.Helper()
	pt := "pt-BR"
	u := db.User{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Email: "ana@x.io", Name: "Ana", Locale: &pt, AuthProvider: "local", Status: "active"}
	st := &fakeStore{users: map[string]db.User{u.Email: u}, resets: map[string]pgtype.UUID{}, passwords: map[[16]byte]string{}}
	m, ss := &fakeMailer{}, &fakeSessions{}
	return New(st, m, ss, Config{PublicURL: "https://baleia.example", DefaultLocale: "en", TokenTTL: time.Hour, PasswordCost: bcrypt.MinCost}), st, m, ss, u
}

// linkToken extracts the raw token from the e-mailed link.
func linkToken(t *testing.T, m sentMail) string {
	t.Helper()
	link, _ := m.data["ResetURL"].(string)
	if !strings.HasPrefix(link, "https://baleia.example/reset-password?") {
		t.Fatalf("link = %q", link)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("token")
}

func TestRequest_MailsAHashedSingleUseLink(t *testing.T) {
	t.Parallel()
	s, st, m, _, u := setup(t)
	if err := s.Request(context.Background(), u.Email); err != nil {
		t.Fatal(err)
	}
	drain(t, s)
	if len(m.sent) != 1 || m.sent[0].to != u.Email || m.sent[0].locale != "pt-BR" || m.sent[0].template != mail.TemplatePasswordReset {
		t.Fatalf("sent = %+v", m.sent)
	}
	token := linkToken(t, m.sent[0])
	if _, stored := st.resets[token]; stored {
		t.Error("the raw token must never be stored")
	}
	if _, stored := st.resets[auth.HashToken(token)]; !stored {
		t.Error("the token hash must be stored")
	}
}

func TestRequest_UnknownEmailIsSilent(t *testing.T) {
	t.Parallel()
	s, _, m, _, _ := setup(t)
	if err := s.Request(context.Background(), "nobody@x.io"); err != nil {
		t.Fatalf("unknown e-mail = %v, want nil", err)
	}
	if len(m.sent) != 0 {
		t.Error("no e-mail for an unknown address")
	}
}

// TestRequest_DeliveryNeverShapesTheAnswer: Request returns before the
// e-mail goes out, and a failed send is not an error, so an existing account
// answers like an unknown address (#422).
func TestRequest_DeliveryNeverShapesTheAnswer(t *testing.T) {
	t.Parallel()
	s, _, m, _, u := setup(t)
	m.release = make(chan struct{})
	m.err = errors.New("smtp: connection refused")
	if err := s.Request(context.Background(), u.Email); err != nil {
		t.Fatalf("Request = %v, want nil while the send is pending", err)
	}
	close(m.release)
	drain(t, s)
	if len(m.sent) != 1 {
		t.Fatalf("sent = %d, want the delivery to run in the background", len(m.sent))
	}
}

func TestConfirm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, st, m, ss, u := setup(t)
	if err := s.Request(ctx, u.Email); err != nil {
		t.Fatal(err)
	}
	drain(t, s)
	token := linkToken(t, m.sent[0])

	id, err := s.Confirm(ctx, token, "new-password-123")
	if err != nil || id != u.ID {
		t.Fatalf("Confirm = %v, %v", id, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(st.passwords[u.ID.Bytes]), []byte("new-password-123")) != nil {
		t.Error("the new password must be stored hashed")
	}
	if len(ss.revoked) != 1 || ss.revoked[0] != u.ID {
		t.Errorf("revoked = %v, want every session of the account ended", ss.revoked)
	}

	if _, err := s.Confirm(ctx, token, "another-password"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("second use = %v, want ErrInvalidToken", err)
	}
}

type brokenStore struct{ *fakeStore }

var errDown = errors.New("connection refused")

func (brokenStore) ConsumePasswordResetToken(context.Context, string) (db.PasswordResetToken, error) {
	return db.PasswordResetToken{}, errDown
}

// TestConfirm_StoreFailureIsNotAnInvalidLink: a database outage is an error,
// never an invalid link.
func TestConfirm_StoreFailureIsNotAnInvalidLink(t *testing.T) {
	t.Parallel()
	_, st, m, ss, _ := setup(t)
	s := New(brokenStore{st}, m, ss, Config{PasswordCost: bcrypt.MinCost})
	if _, err := s.Confirm(context.Background(), "any", "new-password"); !errors.Is(err, errDown) || errors.Is(err, ErrInvalidToken) {
		t.Errorf("Confirm = %v, want the store error", err)
	}
}

// TestRequest_OnlyLocalActiveAccounts: an SSO or suspended account gets no
// link, and the answer is the same as for an unknown address.
func TestRequest_OnlyLocalActiveAccounts(t *testing.T) {
	t.Parallel()
	s, st, m, _, _ := setup(t)
	st.users["sso@x.io"] = db.User{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Email: "sso@x.io", AuthProvider: "oidc", Status: "active"}
	st.users["off@x.io"] = db.User{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Email: "off@x.io", AuthProvider: "local", Status: "suspended"}
	for _, email := range []string{"sso@x.io", "off@x.io"} {
		if err := s.Request(context.Background(), email); err != nil {
			t.Errorf("Request(%s) = %v, want nil", email, err)
		}
	}
	drain(t, s)
	if len(m.sent) != 0 || len(st.resets) != 0 {
		t.Errorf("sent %d e-mails and stored %d links, want none", len(m.sent), len(st.resets))
	}
}

func (brokenStore) GetUserByEmail(context.Context, string) (db.User, error) {
	return db.User{}, errDown
}

// TestRequest_StoreFailureIsAnError: a database outage is an error, and no
// e-mail goes out.
func TestRequest_StoreFailureIsAnError(t *testing.T) {
	t.Parallel()
	_, st, m, ss, _ := setup(t)
	s := New(brokenStore{st}, m, ss, Config{PasswordCost: bcrypt.MinCost})
	if err := s.Request(context.Background(), "ana@x.io"); !errors.Is(err, errDown) {
		t.Errorf("Request = %v, want the store error", err)
	}
	if len(m.sent) != 0 {
		t.Error("no e-mail on a store failure")
	}
}

// drain waits for the service's background deliveries.
func drain(t *testing.T, s *Service) {
	t.Helper()
	if err := s.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestDrain_StopsAtTheDeadline: Drain returns once the deliveries end, or
// with the context's error while one is still running.
func TestDrain_StopsAtTheDeadline(t *testing.T) {
	t.Parallel()
	s, _, m, _, u := setup(t)
	m.release = make(chan struct{})
	if err := s.Request(context.Background(), u.Email); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Drain(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Drain with a delivery running = %v, want context.Canceled", err)
	}
	close(m.release)
	drain(t, s)
	if len(m.sent) != 1 {
		t.Errorf("sent = %d, want the delivery to finish", len(m.sent))
	}
}
