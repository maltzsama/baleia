package migrate

import (
	"context"
	"fmt"
	"strings"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// CredentialRow is one stored connection credential.
type CredentialRow struct {
	ID  string
	Enc string
}

// CredentialStore is the storage the credential migration reads and writes. The
// caller runs the whole migration inside one transaction.
type CredentialStore interface {
	ListCredentials(ctx context.Context) ([]CredentialRow, error)
	UpdateCredentials(ctx context.Context, id, enc string) error
}

// CredentialsResult reports what MigrateCredentials did.
type CredentialsResult struct {
	Migrated int
	Skipped  int      // already v1 envelopes
	Failed   []string // ids that could not be decrypted; left untouched
}

// MigrateCredentials re-seals every legacy credential (AES-GCM under
// sha256(JWT_SECRET), no AAD) as a v1 envelope under the keyring, bound to its
// row id. It is idempotent: v1 rows are skipped. Rows that fail legacy
// decryption are reported and left untouched so the operator can re-enter them.
// This is the only place legacy values are ever decrypted.
func MigrateCredentials(ctx context.Context, store CredentialStore, kr *crypto.Keyring, legacySecret string) (CredentialsResult, error) {
	var res CredentialsResult
	rows, err := store.ListCredentials(ctx)
	if err != nil {
		return res, fmt.Errorf("list credentials: %w", err)
	}
	for _, r := range rows {
		if strings.HasPrefix(r.Enc, "v1:") {
			res.Skipped++
			continue
		}
		id, err := uuid.Parse(r.ID)
		if err != nil {
			res.Failed = append(res.Failed, r.ID)
			continue
		}
		plain, err := crypto.OpenLegacy(r.Enc, legacySecret)
		if err != nil {
			res.Failed = append(res.Failed, r.ID)
			continue
		}
		sealed, err := kr.Seal(plain, catalogs.CredentialsAAD(pgtype.UUID{Bytes: id, Valid: true}))
		if err != nil {
			return res, fmt.Errorf("seal %s: %w", r.ID, err)
		}
		if err := store.UpdateCredentials(ctx, r.ID, sealed); err != nil {
			return res, fmt.Errorf("update %s: %w", r.ID, err)
		}
		res.Migrated++
	}
	return res, nil
}
