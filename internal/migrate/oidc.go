package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/crypto"
)

// ProviderRow is one auth_providers row for the re-encryption pass.
type ProviderRow struct {
	ID  string
	Enc string
}

// ProviderStore is the database slice the OIDC re-encryption needs.
type ProviderStore interface {
	ListProviders(ctx context.Context) ([]ProviderRow, error)
	UpdateProvider(ctx context.Context, id, enc string) error
}

// ProvidersResult counts the re-encryption outcomes.
type ProvidersResult struct {
	Migrated int
	Resealed int
	Skipped  int
	Failed   []string
}

// MigrateOIDCProviders seals plaintext auth_providers.config_enc rows and
// re-seals rows encrypted under the previous key (#278). It never writes a row
// it cannot decrypt or parse, and never logs a secret.
func MigrateOIDCProviders(ctx context.Context, store ProviderStore, kr *crypto.Keyring) (ProvidersResult, error) {
	var res ProvidersResult
	rows, err := store.ListProviders(ctx)
	if err != nil {
		return res, fmt.Errorf("list oidc providers: %w", err)
	}
	for _, r := range rows {
		if r.Enc == "" || kr.IsCurrent(r.Enc) {
			res.Skipped++
			continue
		}
		var cfg auth.OIDCProviderConfig
		if strings.HasPrefix(r.Enc, "v1:") {
			plain, err := kr.Open(r.Enc, r.ID)
			if err != nil {
				res.Failed = append(res.Failed, r.ID)
				continue
			}
			if err := json.Unmarshal(plain, &cfg); err != nil {
				res.Failed = append(res.Failed, r.ID)
				continue
			}
			res.Resealed++
		} else {
			// Legacy plaintext JSON; accept only valid JSON.
			if err := json.Unmarshal([]byte(r.Enc), &cfg); err != nil {
				res.Failed = append(res.Failed, r.ID)
				continue
			}
			res.Migrated++
		}
		sealed, err := auth.SealOIDCConfig(kr, r.ID, cfg)
		if err != nil {
			return res, fmt.Errorf("seal %s: %w", r.ID, err)
		}
		if err := store.UpdateProvider(ctx, r.ID, sealed); err != nil {
			return res, fmt.Errorf("update %s: %w", r.ID, err)
		}
	}
	return res, nil
}
