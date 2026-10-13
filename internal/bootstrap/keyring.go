package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/crypto"
)

// CredentialCounter reports how many connections already hold stored
// credentials. It is a function so callers can adapt any database handle.
type CredentialCounter func(ctx context.Context) (int64, error)

// ErrEncryptionKeyRequired is returned when stored credentials exist but no
// BALEIA_ENCRYPTION_KEY is configured: serving would make every one of them
// unreadable.
var ErrEncryptionKeyRequired = errors.New("BALEIA_ENCRYPTION_KEY is required: the database holds stored credentials")

// LoadKeyring builds the credential keyring from configuration.
//
// With no key configured, a fresh install (no stored credentials) starts with a
// nil keyring and a warning, so first boot is not blocked; saving a credential
// then fails with a clear error until a key is set. If credentials already
// exist, startup is refused.
func LoadKeyring(ctx context.Context, cfg *config.Config, count CredentialCounter) (*crypto.Keyring, error) {
	if cfg.EncryptionKey == "" {
		n, err := count(ctx)
		if err != nil {
			return nil, fmt.Errorf("count stored credentials: %w", err)
		}
		if n > 0 {
			return nil, ErrEncryptionKeyRequired
		}
		slog.Warn("BALEIA_ENCRYPTION_KEY is not set: connection credentials cannot be saved until it is")
		return nil, nil
	}
	return crypto.NewKeyring(cfg.EncryptionKey, cfg.EncryptionKeyPrevious)
}
