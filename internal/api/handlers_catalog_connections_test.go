package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeConnQuerier struct {
	db.Querier
	conn db.CatalogConnection
}

func (f *fakeConnQuerier) GetCatalogConnectionByID(context.Context, pgtype.UUID) (db.CatalogConnection, error) {
	return f.conn, nil
}

func connectionWith(stored bool) db.CatalogConnection {
	c := db.CatalogConnection{
		ID:   pgtype.UUID{Bytes: [16]byte{0x01}, Valid: true},
		Name: "prod",
		Type: "polaris",
		Url:  "http://polaris:8181",
	}
	if stored {
		enc := "<stored-ciphertext>"
		c.CredentialsEnc = &enc
	}
	return c
}

// TestGetCatalogConnectionNeverReturnsSecrets pins the fix: the response carries
// only has_credentials, never the stored (decrypted) secret.
func TestGetCatalogConnectionNeverReturnsSecrets(t *testing.T) {
	h := &Handlers{queries: &fakeConnQuerier{conn: connectionWith(true)}}

	resp, err := h.GetCatalogConnection(context.Background(), GetCatalogConnectionRequestObject{Id: PathId{0x01}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := resp.(GetCatalogConnection200JSONResponse)
	if !ok {
		t.Fatalf("want 200, got %T", resp)
	}
	if !got.HasCredentials {
		t.Fatal("has_credentials = false, want true (a secret is stored)")
	}
}

func TestGetCatalogConnectionReportsNoCredentials(t *testing.T) {
	h := &Handlers{queries: &fakeConnQuerier{conn: connectionWith(false)}}

	resp, err := h.GetCatalogConnection(context.Background(), GetCatalogConnectionRequestObject{Id: PathId{0x01}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := resp.(GetCatalogConnection200JSONResponse)
	if !ok {
		t.Fatalf("want 200, got %T", resp)
	}
	if got.HasCredentials {
		t.Fatal("has_credentials = true, want false (no secret stored)")
	}
}
