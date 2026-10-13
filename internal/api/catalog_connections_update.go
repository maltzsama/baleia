package api

import (
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// buildConnectionUpdateParams maps a PATCH body to partial-update params.
//
// Only fields present in the request are set; omitted fields stay nil so the
// SQL COALESCE leaves the stored column untouched. This is what stops two
// concurrent PATCH requests (e.g. one renaming, one rotating credentials) from
// clobbering each other with a stale full-row write.
//
// Credentials are encrypted before they leave this function.
func buildConnectionUpdateParams(id pgtype.UUID, body UpdateCatalogConnectionRequest, kr *crypto.Keyring) (db.UpdateCatalogConnectionParams, error) {
	params := db.UpdateCatalogConnectionParams{ID: id}

	if body.Name != nil {
		params.Name = body.Name
	}
	if body.Url != nil {
		params.Url = body.Url
	}
	if body.DefaultReadonly != nil {
		params.DefaultReadonly = body.DefaultReadonly
	}
	if body.Credentials != nil {
		enc, err := encodeCredentials(kr, *body.Credentials, id)
		if err != nil {
			return db.UpdateCatalogConnectionParams{}, err
		}
		params.CredentialsEnc = enc
	}

	return params, nil
}
