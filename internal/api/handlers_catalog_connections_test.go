package api

import (
	"context"
	"encoding/json"
	"strings"
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

func testConnection(t *testing.T, stored bool, owner pgtype.UUID) db.CatalogConnection {
	t.Helper()
	c := db.CatalogConnection{
		ID:        pgtype.UUID{Bytes: [16]byte{0x01}, Valid: true},
		Name:      "prod",
		Type:      "polaris",
		Url:       "http://polaris:8181",
		CreatedBy: owner,
	}
	if stored {
		enc := "<stored-ciphertext>"
		c.CredentialsEnc = &enc
	}
	return c
}

// TestGetCatalogConnectionNeverReturnsSecrets pins the fix: the serialized
// response carries only has_credentials, never a credentials field or the secret.
func TestGetCatalogConnectionNeverReturnsSecrets(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{queries: &fakeConnQuerier{conn: testConnection(t, true, ownerUUID(t, testUserID))}})

	resp, err := h.GetCatalogConnection(testContext(), GetCatalogConnectionRequestObject{Id: PathId{0x01}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := resp.(GetCatalogConnection200JSONResponse)
	if !ok {
		t.Fatalf("want 200, got %T", resp)
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), `"credentials"`) {
		t.Fatalf("response exposes a credentials field: %s", body)
	}
	if strings.Contains(string(body), "<stored-ciphertext>") {
		t.Fatalf("response leaks the stored secret: %s", body)
	}
	if !got.HasCredentials {
		t.Fatal("has_credentials = false, want true (a secret is stored)")
	}
}

func TestGetCatalogConnectionReportsNoCredentials(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{queries: &fakeConnQuerier{conn: testConnection(t, false, ownerUUID(t, testUserID))}})

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

func TestGetCatalogConnectionHidesOtherUsersConnections(t *testing.T) {
	t.Parallel()
	other := ownerUUID(t, "99999999-9999-9999-9999-999999999999")
	h := testAPI(handlerFields{queries: &fakeConnQuerier{conn: testConnection(t, true, other)}})

	resp, err := h.GetCatalogConnection(testContext(), GetCatalogConnectionRequestObject{Id: PathId{0x01}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(GetCatalogConnection404JSONResponse); !ok {
		t.Fatalf("want 404 for another user's connection, got %T", resp)
	}
}

// TestGetCatalogConnectionHidesUnownedConnections covers a NULL created_by: an
// unowned connection must not be readable by any caller.
func TestGetCatalogConnectionHidesUnownedConnections(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{queries: &fakeConnQuerier{conn: testConnection(t, true, pgtype.UUID{})}})

	resp, err := h.GetCatalogConnection(testContext(), GetCatalogConnectionRequestObject{Id: PathId{0x01}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(GetCatalogConnection404JSONResponse); !ok {
		t.Fatalf("want 404 for an unowned connection, got %T", resp)
	}
}

func TestTestCatalogConnectionHidesOtherUsersConnections(t *testing.T) {
	t.Parallel()
	other := ownerUUID(t, "99999999-9999-9999-9999-999999999999")
	h := testAPI(handlerFields{queries: &fakeConnQuerier{conn: testConnection(t, true, other)}})

	resp, err := h.TestCatalogConnection(testContext(), TestCatalogConnectionRequestObject{Id: PathId{0x01}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(TestCatalogConnection404JSONResponse); !ok {
		t.Fatalf("want 404 testing another user's connection, got %T", resp)
	}
}

func TestTestCatalogConnectionHidesUnownedConnections(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{queries: &fakeConnQuerier{conn: testConnection(t, true, pgtype.UUID{})}})

	resp, err := h.TestCatalogConnection(testContext(), TestCatalogConnectionRequestObject{Id: PathId{0x01}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(TestCatalogConnection404JSONResponse); !ok {
		t.Fatalf("want 404 testing an unowned connection, got %T", resp)
	}
}
