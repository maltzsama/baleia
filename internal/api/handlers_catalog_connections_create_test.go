package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/db"
)

type recordingConnQuerier struct {
	db.Querier
	created []db.CreateCatalogConnectionParams
}

func (f *recordingConnQuerier) CreateCatalogConnection(_ context.Context, p db.CreateCatalogConnectionParams) (db.CatalogConnection, error) {
	f.created = append(f.created, p)
	return db.CatalogConnection{ID: p.ID, Name: p.Name, Type: p.Type, Url: p.Url, CredentialsEnc: p.CredentialsEnc, CreatedBy: p.CreatedBy}, nil
}

func createReq(typ CreateCatalogConnectionRequestType) CreateCatalogConnectionRequestObject {
	creds := map[string]interface{}{"username": "u", "password": "p-s3cr3t", "client_id": "u", "client_secret": "p-s3cr3t"}
	return CreateCatalogConnectionRequestObject{Body: &CreateCatalogConnectionRequest{
		Name: "conn-" + string(typ), Type: typ, Url: "http://x", Credentials: &creds,
	}}
}

var allConnectionTypes = []CreateCatalogConnectionRequestType{"polaris", "nessie", "glue", "hive", "iceberg", "postgresql", "mysql"}

// TestCreateConnection_AllTypesUseGuard: every connection type goes through the
// single encryption path. Without a key nothing is stored, for any type.
func TestCreateConnection_AllTypesUseGuard(t *testing.T) {
	for _, typ := range allConnectionTypes {
		t.Run(string(typ), func(t *testing.T) {
			q := &recordingConnQuerier{}
			h := &Handlers{queries: q} // keyring == nil
			resp, err := h.CreateCatalogConnection(testContext(), createReq(typ))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(q.created) != 0 {
				t.Fatalf("a row was stored without an encryption key: %+v", q.created)
			}
			rec := httptest.NewRecorder()
			if err := resp.VisitCreateCatalogConnectionResponse(rec); err != nil {
				t.Fatal(err)
			}
			if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"encryption_failed"`) {
				t.Fatalf("response = %d %s, want 500 encryption_failed", rec.Code, rec.Body)
			}
		})
	}
}

// TestCreateConnection_StoresEnvelopeBoundToItsID: with a key, every type stores
// a v1 envelope that opens only with the generated row id.
func TestCreateConnection_StoresEnvelopeBoundToItsID(t *testing.T) {
	kr := testKeyring(t)
	for _, typ := range allConnectionTypes {
		t.Run(string(typ), func(t *testing.T) {
			q := &recordingConnQuerier{}
			h := &Handlers{queries: q, keyring: kr, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)}
			if _, err := h.CreateCatalogConnection(testContext(), createReq(typ)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(q.created) != 1 {
				t.Fatalf("want 1 insert, got %d", len(q.created))
			}
			p := q.created[0]
			if !p.ID.Valid {
				t.Fatal("the id must be generated before the insert")
			}
			if p.CredentialsEnc == nil || !strings.HasPrefix(*p.CredentialsEnc, "v1:") || strings.Contains(*p.CredentialsEnc, "p-s3cr3t") {
				t.Fatalf("stored credentials = %v", p.CredentialsEnc)
			}
			if _, err := kr.Open(*p.CredentialsEnc, catalogs.CredentialsAAD(p.ID)); err != nil {
				t.Fatalf("envelope does not open with its own row id: %v", err)
			}
		})
	}
}

// TestUpdateConnection_EncryptionFailureIs500: a credentials PATCH with no key
// answers a typed 500 and writes nothing.
func TestUpdateConnection_EncryptionFailureIs500(t *testing.T) {
	owner := ownerUUID(t, testUserID)
	q := &fakeConnQuerier{conn: testConnection(t, true, owner)}
	h := &Handlers{queries: q}
	creds := map[string]interface{}{"password": "p-s3cr3t"}
	resp, err := h.UpdateCatalogConnection(testContext(), UpdateCatalogConnectionRequestObject{Id: [16]byte{0x01}, Body: &UpdateCatalogConnectionRequest{Credentials: &creds}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitUpdateCatalogConnectionResponse(rec); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"encryption_failed"`) {
		t.Fatalf("response = %d %s, want 500 encryption_failed", rec.Code, rec.Body)
	}
}
