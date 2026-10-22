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
	// ldapAuthenticate verifies directory credentials (#255). Tests inject a
	// fake so the handler branch runs without a directory server.
	ldapAuthenticate func(ctx context.Context, cfg identityldap.Config, username, password string) (*identityldap.User, error)
	// idpProbe probes a provider configuration without persisting anything
	// (#286). Tests inject a fake so the handler runs without a network.
	idpProbe func(ctx context.Context, cfg providerConfig, testUsername string) IdentityProviderTestResult
}
