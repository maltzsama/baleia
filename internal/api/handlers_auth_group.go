package api

import (
	"context"
	"sync"

	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
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
	return &AuthHandlers{core: c, directory: ldapDirectory{}, prober: networkProber{}}
}
