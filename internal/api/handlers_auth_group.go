package api

import (
	"context"
	"sync"

	"github.com/galileostudio/baleia/internal/db"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/galileostudio/baleia/internal/identity/passwordreset"
	"github.com/galileostudio/baleia/internal/identity/session"
	"github.com/galileostudio/baleia/internal/identity/setup"
	"github.com/galileostudio/baleia/internal/mail"
)

// AuthHandlers serves sign-in, sessions, users, API keys and identity providers (#365).
type AuthHandlers struct {
	*core
	// dummyHash is compared against on the unknown-account and SSO-only paths
	// so timing does not reveal whether an account exists (#51).
	dummyHashOnce sync.Once
	dummyHash     string
	// loginDelay slows repeated login failures per account (#51).
	loginDelayStore *LoginDelay
	loginDelayOnce  sync.Once
	// mailer sends password-reset and invite e-mails (#51) localized by the
	// recipient's preference (#67). Tests inject a fake so the paths run
	// without an SMTP server.
	mailer MailSender
	// oidcCache holds discovered providers keyed by id + config version (#280).
	oidcCache sync.Map
	// directory verifies LDAP credentials (#255); tests give a fake so the
	// handler branch runs without a directory server.
	directory directoryAuthenticator
	// sessions issues, rotates and ends sign-in sessions (#51).
	sessions *session.Service
	// resets runs the e-mailed password reset (#51).
	resets *passwordreset.Service
	// setup creates the first administrator.
	setup *setup.Service
	// prober probes a provider configuration without persisting anything
	// (#286); tests give a fake so the handler runs without a network.
	prober identityProviderProber
}

// directoryAuthenticator verifies a user's credentials against a directory.
type directoryAuthenticator interface {
	Authenticate(ctx context.Context, cfg identityldap.Config, username, password string) (*identityldap.User, error)
}

// ldapDirectory binds to the configured LDAP server.
type ldapDirectory struct{}

func (ldapDirectory) Authenticate(ctx context.Context, cfg identityldap.Config, username, password string) (*identityldap.User, error) {
	return identityldap.Authenticate(ctx, cfg, username, password)
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
		core:      c,
		directory: ldapDirectory{},
		prober:    networkProber{},
		sessions:  newSessions(c),
		setup: setup.New(c.queries, func(ctx context.Context, fn func(setup.Store) error) error {
			return c.inTx(ctx, func(q db.Querier) error { return fn(q) })
		}, c.settings.Auth.PasswordCost),
	}
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
