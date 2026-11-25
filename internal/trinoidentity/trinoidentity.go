// Package trinoidentity maps a Baleia user to the Trino identity their queries
// run under. It is shared by the API (query editor, Lens) and the worker
// (retriever refreshes) so every path agrees.
package trinoidentity

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
)

// UserStore is the slice of the database the mapping reads.
type UserStore interface {
	GetUserByID(ctx context.Context, id db.UUID) (db.User, error)
}

// For returns the Trino identity of user uid. mode is
// BALEIA_TRINO_USER_IDENTITY: "email" uses the user's e-mail, anything else
// (the default "uuid") u_<uuid>. It never returns an admin or service identity.
// The user must exist: an error (including no rows) means the user is gone.
func For(ctx context.Context, q UserStore, mode string, uid db.UUID) (string, error) {
	u, err := q.GetUserByID(ctx, uid)
	if err != nil {
		return "", err
	}
	return Of(mode, uid, u.Email), nil
}

// Of is the mapping For applies, for callers that already hold the user's id
// and e-mail, such as the data policy subject map (ADR 0028): both paths
// produce the same identity.
func Of(mode string, uid db.UUID, email string) string {
	if mode == "email" {
		return email
	}
	return "u_" + uid.String()
}
