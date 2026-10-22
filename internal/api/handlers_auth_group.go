package api

import (
	"context"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/account"
	"github.com/galileostudio/baleia/internal/identity/login"
	"github.com/galileostudio/baleia/internal/identity/oidclogin"
	"github.com/galileostudio/baleia/internal/identity/passwordreset"
	"github.com/galileostudio/baleia/internal/identity/session"
	"github.com/galileostudio/baleia/internal/identity/setup"
	"github.com/galileostudio/baleia/internal/identity/users"
	"github.com/galileostudio/baleia/internal/mail"
)

// AuthHandlers serves sign-in, sessions, users, API keys and identity providers (#365).
type AuthHandlers struct {
	*core
	// loginDelay slows repeated login failures per account (#51).
	loginDelayStore *LoginDelay
	loginDelayOnce  sync.Once
	// mailer sends password-reset and invite e-mails (#51) localized by the
	// recipient's preference (#67). Tests inject a fake so the paths run
	// without an SMTP server.
	mailer MailSender
	// oidc runs the OIDC authorization-code login (#278).
	oidc *oidclogin.Service
	// logins verifies passwords and directory binds (#51, #255).
	logins *login.Service
	// sessions issues, rotates and ends sign-in sessions (#51).
	sessions *session.Service
	// resets runs the e-mailed password reset (#51).
	resets *passwordreset.Service
	// users administers accounts (#258).
	users *users.Service
	// accounts is the signed-in user's own account.
	accounts *account.Service
	// setup creates the first administrator.
	setup *setup.Service
	// prober probes a provider configuration without persisting anything
	// (#286); tests give a fake so the handler runs without a network.
	prober identityProviderProber
}

// identityProviderProber checks a provider configuration end to end.
type identityProviderProber interface {
	Probe(ctx context.Context, cfg providerConfig, testUsername string) IdentityProviderTestResult
}

// networkProber reaches the provider over the network.
type networkProber struct{}

func (networkProber) Probe(ctx context.Context, cfg providerConfig, testUsername string) IdentityProviderTestResult {
	return defaultIdentityProviderProbe(ctx, cfg, testUsername)
}

func newAuthHandlers(c *core) *AuthHandlers {
	a := &AuthHandlers{
		core:     c,
		prober:   networkProber{},
		sessions: newSessions(c),
		setup: setup.New(c.queries, func(ctx context.Context, fn func(setup.Store) error) error {
			return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
		}, c.settings.Auth.PasswordCost),
	}
	a.logins = newLogins(c, login.LDAP{})
	a.accounts = account.New(c.queries, a.sessions, account.Config{
		PasswordCost:      c.settings.Auth.PasswordCost,
		MinPasswordLength: minPasswordLength,
	})
	a.oidc = oidclogin.New(c.queries, c.queries, c.keyring, oidclogin.DiscoverIssuer, oidclogin.Config{
		PublicURL: c.settings.Mail.PublicURL,
		CacheTTL:  time.Hour,
	})
	a.users = users.New(c.queries, func(ctx context.Context, fn func(users.TxStore) error) error {
		return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
	}, groupMailer{a}, a.sessions, users.Config{
		PublicURL:     c.settings.Mail.PublicURL,
		DefaultLocale: c.settings.Mail.DefaultLocale,
		PasswordCost:  c.settings.Auth.PasswordCost,
	})
	a.resets = passwordreset.New(c.queries, groupMailer{a}, a.sessions, passwordreset.Config{
		PublicURL:     c.settings.Mail.PublicURL,
		DefaultLocale: c.settings.Mail.DefaultLocale,
		TokenTTL:      c.settings.Auth.ResetTTL,
		PasswordCost:  c.settings.Auth.PasswordCost,
	})
	return a
}

// newSessions builds the session service over the core's database and settings.
func newSessions(c *core) *session.Service {
	return session.New(c.queries, c.queries, session.Config{
		JWTSecret:  c.settings.Auth.JWTSecret,
		AccessTTL:  c.settings.Auth.AccessTTL,
		RefreshTTL: c.settings.Auth.RefreshTTL,
	})
}

// groupMailer sends through the group's mailer as it is when the mail goes
// out, so a mailer set after construction (tests) is the one used.
type groupMailer struct{ a *AuthHandlers }

func (m groupMailer) Send(ctx context.Context, to, locale string, template mail.Template, data map[string]any) error {
	return m.a.mailSender().Send(ctx, to, locale, template, data)
}

// newLogins builds the credential verifier over the core's database; tests
// pass a fake directory.
func newLogins(c *core, directory login.Directory) *login.Service {
	return login.New(c.queries, directory, c.keyring, c.settings.Auth.PasswordCost)
}
