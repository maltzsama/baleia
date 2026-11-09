package migrate

import (
	"context"
	"database/sql"
)

// NewSQLCredentialStore adapts a transaction to CredentialStore. The caller
// opens and commits the transaction: the rows are read with FOR UPDATE, so the
// store is only valid inside it.
func NewSQLCredentialStore(tx *sql.Tx) CredentialStore {
	return sqlCredentialStore{tx: tx}
}

type sqlCredentialStore struct{ tx *sql.Tx }

func (s sqlCredentialStore) ListCredentials(ctx context.Context) ([]CredentialRow, error) {
	rows, err := s.tx.QueryContext(ctx, "SELECT id::text, credentials_enc FROM catalog_connections WHERE credentials_enc IS NOT NULL AND credentials_enc <> '' FOR UPDATE")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CredentialRow
	for rows.Next() {
		var r CredentialRow
		if err := rows.Scan(&r.ID, &r.Enc); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s sqlCredentialStore) UpdateCredentials(ctx context.Context, id, enc string) error {
	_, err := s.tx.ExecContext(ctx, "UPDATE catalog_connections SET credentials_enc = $1 WHERE id = $2::uuid", enc, id)
	return err
}

// NewSQLProviderStore adapts a transaction to ProviderStore. The caller opens
// and commits the transaction: the rows are read with FOR UPDATE, so the store
// is only valid inside it.
func NewSQLProviderStore(tx *sql.Tx) ProviderStore {
	return sqlProviderStore{tx: tx}
}

type sqlProviderStore struct{ tx *sql.Tx }

func (s sqlProviderStore) ListProviders(ctx context.Context) ([]ProviderRow, error) {
	rows, err := s.tx.QueryContext(ctx, "SELECT id::text, config_enc FROM auth_providers FOR UPDATE")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProviderRow
	for rows.Next() {
		var r ProviderRow
		if err := rows.Scan(&r.ID, &r.Enc); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s sqlProviderStore) UpdateProvider(ctx context.Context, id, enc string) error {
	_, err := s.tx.ExecContext(ctx, "UPDATE auth_providers SET config_enc = $1, updated_at = now() WHERE id = $2::uuid", enc, id)
	return err
}
