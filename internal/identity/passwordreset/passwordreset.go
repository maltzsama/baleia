// Package passwordreset runs the e-mailed password reset (#51): a single-use
// link whose token is stored only as a hash, and a confirmation that ends
// every session of the account.
package passwordreset

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/mail"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store persists reset tokens and passwords; *db.Queries implements it.
type Store interface {
	GetUserByEmail(ctx context.Context, email string) (db.User, error)
	CreatePasswordResetToken(ctx context.Context, arg db.CreatePasswordResetTokenParams) error
	ConsumePasswordResetToken(ctx context.Context, tokenHash string) (db.PasswordResetToken, error)
	UpdateUserPassword(ctx context.Context, arg db.UpdateUserPasswordParams) error
}

// Mailer sends the reset e-mail in the recipient's locale.
type Mailer interface {
	Send(ctx context.Context, to, locale string, template mail.Template, data map[string]any) error
}

// Sessions ends every session of an account; *session.Service implements it.
type Sessions interface {
	RevokeAll(ctx context.Context, userID pgtype.UUID)
}

// Config are the reset settings, resolved by the caller.
type Config struct {
	// PublicURL is where the e-mailed link points.
	PublicURL     string
	DefaultLocale string
	TokenTTL      time.Duration
	PasswordCost  int
}

// ErrInvalidToken: the link is unknown, expired or already used.
var ErrInvalidToken = errors.New("passwordreset: invalid or expired token")

// deliveryTimeout bounds the background token insert and e-mail send.
const deliveryTimeout = time.Minute

// Service requests and confirms password resets.
type Service struct {
	store    Store
	mailer   Mailer
	sessions Sessions
	cfg      Config
	// pending tracks the deliveries running in the background.
	pending sync.WaitGroup
}

// New builds the service.
func New(store Store, mailer Mailer, sessions Sessions, cfg Config) *Service {
	return &Service{store: store, mailer: mailer, sessions: sessions, cfg: cfg}
}

// Request e-mails a reset link to the account with this address. Only a
// local, active account gets one: an SSO account cannot sign in with a
// password and a suspended one cannot sign in at all. Any other address is not
// an error, so the answer never reveals whether it exists.
//
// Request returns right after the lookup: the token insert and the e-mail
// send run in the background, so neither their time nor their failure
// reveals the account (#422). A failed delivery is only logged.
func (s *Service) Request(ctx context.Context, email string) error {
	user, err := s.store.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up account: %w", err)
	}
	if user.AuthProvider != "local" || auth.RequireActive(user.Status) != nil {
		return nil
	}

	s.pending.Go(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliveryTimeout)
		defer cancel()
		// The error never carries the link; log the account, not the address.
		if err := s.deliver(ctx, user); err != nil {
			slog.Error("password reset delivery failed", "user_id", user.ID.String(), "err", err)
		}
	})
	return nil
}

// Wait blocks until every background delivery has finished.
func (s *Service) Wait() { s.pending.Wait() }

// deliver stores a new token for user and e-mails its link.
func (s *Service) deliver(ctx context.Context, user db.User) error {
	token := newToken()
	// Only the hash is stored; the raw token travels in the e-mail link (#51).
	if err := s.store.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		TokenHash: auth.HashToken(token),
		UserID:    user.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(s.cfg.TokenTTL), Valid: true},
	}); err != nil {
		return fmt.Errorf("create password reset token: %w", err)
	}

	locale := mail.LocaleOrDefault(user.Locale, s.cfg.DefaultLocale)
	if err := s.mailer.Send(ctx, user.Email, locale, mail.TemplatePasswordReset, map[string]any{
		"Name":     user.Name,
		"ResetURL": s.cfg.PublicURL + "/reset-password?token=" + token,
	}); err != nil {
		return fmt.Errorf("send password reset e-mail: %w", err)
	}
	return nil
}

// Confirm sets a new password through a reset link and ends every session of
// the account. The caller validates the password first: a rejected password
// must not burn the link.
func (s *Service) Confirm(ctx context.Context, token, newPassword string) (pgtype.UUID, error) {
	// Consuming the hash makes the link single use even before it expires (#51).
	row, err := s.store.ConsumePasswordResetToken(ctx, auth.HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, ErrInvalidToken
	}
	if err != nil {
		return pgtype.UUID{}, err
	}

	hash, err := auth.HashPasswordWithCost(newPassword, s.cfg.PasswordCost)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if err := s.store.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{ID: row.UserID, PasswordHash: &hash}); err != nil {
		return pgtype.UUID{}, err
	}

	s.sessions.RevokeAll(ctx, row.UserID)
	return row.UserID, nil
}

// newToken is 128 random bits, URL-safe and unpadded so it survives being
// embedded raw in the link.
func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
