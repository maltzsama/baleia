// Package trinoidentity maps a Baleia user to the Trino identity their queries
// run under. It is shared by the API (query editor, Lens) and the worker
// (retriever refreshes) so every path agrees.
package trinoidentity

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// UserStore is the slice of the database the mapping reads.
type UserStore interface {
	GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error)
}

// For returns the Trino identity of user uid. mode is
// BALEIA_TRINO_USER_IDENTITY: "email" uses the user's e-mail, anything else
// (the default "uuid") u_<uuid>. It never returns an admin or service identity.
// The user must exist: an error (including no rows) means the user is gone.
func For(ctx context.Context, q UserStore, mode string, uid pgtype.UUID) (string, error) {
	u, err := q.GetUserByID(ctx, uid)
	if err != nil {
		return "", err
	}
	if mode == "email" {
		return u.Email, nil
	}
	return "u_" + uid.String(), nil
}
