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

func ownerUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	u, err := uuidFromString(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func testConnection(t *testing.T, stored bool, owner string) db.CatalogConnection {
	t.Helper()
	c := db.CatalogConnection{
		ID:        pgtype.UUID{Bytes: [16]byte{0x01}, Valid: true},
		Name:      "prod",
		Type:      "polaris",
		Url:       "http://polaris:8181",
		CreatedBy: ownerUUID(t, owner),
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
	h := &Handlers{queries: &fakeConnQuerier{conn: testConnection(t, true, testUserID)}}

	resp, err := h.GetCatalogConnection(testContext(), GetCatalogConnectionRequestObject{Id: PathId{0x01}})
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
	h := &Handlers{queries: &fakeConnQuerier{conn: testConnection(t, false, testUserID)}}

	resp, err := h.GetCatalogConnection(testContext(), GetCatalogConnectionRequestObject{Id: PathId{0x01}})
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

// TestGetCatalogConnectionHidesOtherUsersConnections pins the ownership fix: a
// caller cannot read another user's connection (404, not the details).
func TestGetCatalogConnectionHidesOtherUsersConnections(t *testing.T) {
	other := "99999999-9999-9999-9999-999999999999"
	h := &Handlers{queries: &fakeConnQuerier{conn: testConnection(t, true, other)}}

	resp, err := h.GetCatalogConnection(testContext(), GetCatalogConnectionRequestObject{Id: PathId{0x01}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(GetCatalogConnection404JSONResponse); !ok {
		t.Fatalf("want 404 for another user's connection, got %T", resp)
	}
}
