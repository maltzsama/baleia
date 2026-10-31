package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func testRegistry(t *testing.T) *connections.Registry {
	t.Helper()
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := providers.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

type recordingConnQuerier struct {
	db.Querier
	created []db.CreateCatalogConnectionParams
}

func (f *recordingConnQuerier) CreateCatalogConnection(_ context.Context, p db.CreateCatalogConnectionParams) (db.CatalogConnection, error) {
	f.created = append(f.created, p)
	return db.CatalogConnection{ID: p.ID, Name: p.Name, Type: p.Type, Url: p.Url, Config: p.Config, CredentialsEnc: p.CredentialsEnc, CreatedBy: p.CreatedBy}, nil
}

func (f *recordingConnQuerier) GetCatalogConnectionByID(_ context.Context, id pgtype.UUID) (db.CatalogConnection, error) {
	for _, p := range f.created {
		if p.ID == id {
			return db.CatalogConnection{ID: p.ID, Name: p.Name, Type: p.Type, Url: p.Url, Config: p.Config, CredentialsEnc: p.CredentialsEnc, CreatedBy: p.CreatedBy}, nil
		}
	}
	return db.CatalogConnection{ID: id}, nil
}

func strMap(kv ...string) *map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return &m
}

func anyMap(kv ...string) *map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return &m
}

// validCreateRequests has one valid body per provider, mixing the config shape
// and the older url + credentials shape the UI still sends.
func validCreateRequests() map[string]*CreateCatalogConnectionRequest {
	url := func(s string) *string { return &s }
	return map[string]*CreateCatalogConnectionRequest{
		"polaris": {Name: "p", Type: "polaris", Config: strMap("url", "http://polaris:8181", "client_id", "cid"), Credentials: anyMap("client_secret", "p-s3cr3t")},
		// The setup wizard sends client_id/client_secret for every Iceberg kind,
		// empty when the user leaves them blank.
		"nessie":  {Name: "n", Type: "nessie", Url: url("http://nessie:19120"), Credentials: anyMap("token", "p-s3cr3t", "client_id", "", "client_secret", "")},
		"iceberg": {Name: "i", Type: "iceberg", Config: strMap("url", "http://rest:8181/catalog"), Credentials: anyMap("token", "p-s3cr3t")},
		"postgresql": {Name: "pg", Type: "postgresql", Url: url("jdbc:postgresql://db:5432/sales"),
			PostgresqlCredentials: &SqlCredentials{Username: url("u"), Password: url("p-s3cr3t")}},
		"mysql": {Name: "my", Type: "mysql", Config: strMap("host", "db", "username", "u"), Credentials: anyMap("password", "p-s3cr3t")},
	}
}

func createHandlers(t *testing.T, q db.Querier, kr *crypto.Keyring) *API {
	t.Helper()
	return testAPI(handlerFields{queries: q, tx: directTx{q: q}, keyring: kr, connections: testRegistry(t), settings: SettingsFromConfig(&config.Config{}), catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})
}

func visitCreate(t *testing.T, resp CreateCatalogConnectionResponseObject) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := resp.VisitCreateCatalogConnectionResponse(rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestCreateConnection_AllTypesUseGuard: every provider goes through the single
// encryption path. Without a key nothing is stored.
func TestCreateConnection_AllTypesUseGuard(t *testing.T) {
	t.Parallel()
	for kind, body := range validCreateRequests() {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			q := &recordingConnQuerier{}
			resp, err := createHandlers(t, q, nil).CreateCatalogConnection(testContext(), CreateCatalogConnectionRequestObject{Body: body})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(q.created) != 0 {
				t.Fatalf("a row was stored without an encryption key: %+v", q.created)
			}
			if rec := visitCreate(t, resp); rec.Code != 500 || !strings.Contains(rec.Body.String(), `"encryption_failed"`) {
				t.Fatalf("response = %d %s, want 500 encryption_failed", rec.Code, rec.Body)
			}
		})
	}
}

// TestCreateConnection_SplitsConfigAndSecrets: secrets are sealed with the row
// id and nothing else; non-secret values go to config; the url column keeps
// what the Trino projection reads.
func TestCreateConnection_SplitsConfigAndSecrets(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	wantURL := map[string]string{
		"polaris":    "http://polaris:8181",
		"nessie":     "http://nessie:19120",
		"iceberg":    "http://rest:8181/catalog",
		"postgresql": "jdbc:postgresql://db:5432/sales",
		"mysql":      "jdbc:mysql://db:3306",
	}
	for kind, body := range validCreateRequests() {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			q := &recordingConnQuerier{}
			resp, err := createHandlers(t, q, kr).CreateCatalogConnection(testContext(), CreateCatalogConnectionRequestObject{Body: body})
			if err != nil {
				t.Fatal(err)
			}
			if rec := visitCreate(t, resp); rec.Code != 201 {
				t.Fatalf("response = %d %s", rec.Code, rec.Body)
			}
			p := q.created[0]
			if !p.ID.Valid || p.Type != kind || p.Url != wantURL[kind] {
				t.Fatalf("row = id %v type %q url %q", p.ID.Valid, p.Type, p.Url)
			}
			if strings.Contains(string(p.Config), "p-s3cr3t") {
				t.Fatalf("config holds a secret: %s", p.Config)
			}
			if p.CredentialsEnc == nil || !strings.HasPrefix(*p.CredentialsEnc, "v1:") || strings.Contains(*p.CredentialsEnc, "p-s3cr3t") {
				t.Fatalf("stored credentials = %v", p.CredentialsEnc)
			}
			plain, err := kr.Open(*p.CredentialsEnc, catalogs.CredentialsAAD(p.ID))
			if err != nil {
				t.Fatalf("envelope does not open with its own row id: %v", err)
			}
			var secrets map[string]string
			if err := json.Unmarshal(plain, &secrets); err != nil || len(secrets) != 1 {
				t.Fatalf("sealed = %s, want exactly the one secret", plain)
			}
		})
	}
}

// TestCreateConnection_LegacyShapeMovesValuesToConfig: the UI sends client_id
// inside credentials; it is stored as config, and the projection still sees it.
func TestCreateConnection_LegacyShapeMovesValuesToConfig(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	q := &recordingConnQuerier{}
	url := "http://polaris:8181/"
	body := &CreateCatalogConnectionRequest{Name: "p", Type: "polaris", Url: &url, Credentials: anyMap("client_id", "cid", "client_secret", "sec")}
	if _, err := createHandlers(t, q, kr).CreateCatalogConnection(testContext(), CreateCatalogConnectionRequestObject{Body: body}); err != nil {
		t.Fatal(err)
	}
	p := q.created[0]
	var cfg map[string]string
	_ = json.Unmarshal(p.Config, &cfg)
	if cfg["client_id"] != "cid" || cfg["url"] != "http://polaris:8181" || cfg["realm"] != "POLARIS" {
		t.Fatalf("config = %v", cfg)
	}
	creds, err := catalogs.DecodeCredentials(db.CatalogConnection{ID: p.ID, Config: p.Config, CredentialsEnc: p.CredentialsEnc}, kr)
	if err != nil || creds.ClientID != "cid" || creds.ClientSecret != "sec" || creds.Realm != "POLARIS" {
		t.Fatalf("projection credentials = %+v, %v", creds, err)
	}
}

func TestCreateConnection_Rejects(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		body  *CreateCatalogConnectionRequest
		code  string
		field string
	}{
		"unknown key": {&CreateCatalogConnectionRequest{Name: "x", Type: "polaris",
			Config: strMap("url", "http://p", "client_id", "c", "bogus", "1"), Credentials: anyMap("client_secret", "s")}, "validation_error", "bogus"},
		"unknown secret key": {&CreateCatalogConnectionRequest{Name: "x", Type: "polaris",
			Config: strMap("url", "http://p", "client_id", "c"), Credentials: anyMap("client_secret", "s", "password", "s")}, "validation_error", "password"},
		"missing required": {&CreateCatalogConnectionRequest{Name: "x", Type: "polaris", Config: strMap("url", "http://p")}, "validation_error", "client_id"},
		"secret in config": {&CreateCatalogConnectionRequest{Name: "x", Type: "mysql",
			Config: strMap("host", "db", "username", "u", "password", "s")}, "validation_error", "password"},
		"bad port":     {&CreateCatalogConnectionRequest{Name: "x", Type: "postgresql", Config: strMap("host", "db", "port", "99999", "username", "u")}, "validation_error", "port"},
		"bad url":      {&CreateCatalogConnectionRequest{Name: "x", Type: "nessie", Config: strMap("url", "ftp://n")}, "validation_error", "url"},
		"bad jdbc":     {&CreateCatalogConnectionRequest{Name: "x", Type: "postgresql", Url: func() *string { s := "not a url"; return &s }()}, "validation_error", "url"},
		"unknown kind": {&CreateCatalogConnectionRequest{Name: "x", Type: "duckdb"}, "provider_unsupported", "type"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			q := &recordingConnQuerier{}
			resp, err := createHandlers(t, q, testKeyring(t)).CreateCatalogConnection(testContext(), CreateCatalogConnectionRequestObject{Body: c.body})
			if err != nil {
				t.Fatal(err)
			}
			rec := visitCreate(t, resp)
			if rec.Code != 422 || !strings.Contains(rec.Body.String(), `"code":"`+c.code+`"`) || !strings.Contains(rec.Body.String(), `"field":"`+c.field+`"`) {
				t.Fatalf("response = %d %s, want 422 %s on %s", rec.Code, rec.Body, c.code, c.field)
			}
			if len(q.created) != 0 {
				t.Fatal("an invalid connection was stored")
			}
		})
	}
}

// ── update ───────────────────────────────────────────────────────────────────

type updateConnQuerier struct {
	db.Querier
	conn      db.CatalogConnection
	updates   []db.UpdateCatalogConnectionParams
	updateErr error
}

func (f *updateConnQuerier) GetCatalogConnectionByID(context.Context, pgtype.UUID) (db.CatalogConnection, error) {
	return f.conn, nil
}

func (f *updateConnQuerier) UpdateCatalogConnection(_ context.Context, p db.UpdateCatalogConnectionParams) (db.CatalogConnection, error) {
	f.updates = append(f.updates, p)
	c := f.conn
	if p.Config != nil {
		c.Config = p.Config
	}
	return c, f.updateErr
}

// storedPolaris is a polaris row as written by the registry: client_id in
// config, the secret sealed.
func storedPolaris(t *testing.T, kr *crypto.Keyring) db.CatalogConnection {
	t.Helper()
	c := testConnection(t, false, ownerUUID(t, testUserID))
	c.Config = []byte(`{"url":"http://polaris:8181","client_id":"cid","realm":"POLARIS"}`)
	enc, err := kr.Seal([]byte(`{"client_secret":"old-s3cr3t"}`), catalogs.CredentialsAAD(c.ID))
	if err != nil {
		t.Fatal(err)
	}
	c.CredentialsEnc = &enc
	return c
}

func runUpdate(t *testing.T, h *API, body UpdateCatalogConnectionRequest) *httptest.ResponseRecorder {
	t.Helper()
	resp, err := h.UpdateCatalogConnection(testContext(), UpdateCatalogConnectionRequestObject{Id: [16]byte{0x01}, Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitUpdateCatalogConnectionResponse(rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestUpdateConnection_NameOnlyTouchesNothingElse(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	q := &updateConnQuerier{conn: storedPolaris(t, kr)}
	name := "renamed"
	if rec := runUpdate(t, createHandlers(t, q, kr), UpdateCatalogConnectionRequest{Name: &name}); rec.Code != 200 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	p := q.updates[0]
	if *p.Name != "renamed" || p.Config != nil || p.Url != nil || p.CredentialsEnc != nil {
		t.Fatalf("params = %+v", p)
	}
}

// TestUpdateConnection_NameTakenIsConflict: renaming to a name another
// connection has is a 409 with the conflict code, not a 500.
func TestUpdateConnection_NameTakenIsConflict(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	q := &updateConnQuerier{conn: storedPolaris(t, kr), updateErr: &pgconn.PgError{Code: "23505"}}
	name := "taken"
	rec := runUpdate(t, createHandlers(t, q, kr), UpdateCatalogConnectionRequest{Name: &name})
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("response = %d %s, want 409 conflict", rec.Code, rec.Body)
	}
}

func TestUpdateConnection_MergesConfigAndKeepsSecret(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	q := &updateConnQuerier{conn: storedPolaris(t, kr)}
	rec := runUpdate(t, createHandlers(t, q, kr), UpdateCatalogConnectionRequest{Config: strMap("realm", "OTHER")})
	if rec.Code != 200 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") || !strings.Contains(rec.Body.String(), `"realm":"OTHER"`) {
		t.Fatalf("response body = %s", rec.Body)
	}
	p := q.updates[0]
	var cfg map[string]string
	_ = json.Unmarshal(p.Config, &cfg)
	if cfg["realm"] != "OTHER" || cfg["client_id"] != "cid" || cfg["url"] != "http://polaris:8181" || *p.Url != "http://polaris:8181" {
		t.Fatalf("config = %v url %v", cfg, p.Url)
	}
	plain, err := kr.Open(*p.CredentialsEnc, catalogs.CredentialsAAD(q.conn.ID))
	if err != nil || !strings.Contains(string(plain), "old-s3cr3t") {
		t.Fatalf("stored secret = %s, %v; want it kept", plain, err)
	}
}

func TestUpdateConnection_ReplacesNamedSecret(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	q := &updateConnQuerier{conn: storedPolaris(t, kr)}
	if rec := runUpdate(t, createHandlers(t, q, kr), UpdateCatalogConnectionRequest{Credentials: anyMap("client_id", "new-id", "client_secret", "new-s3cr3t")}); rec.Code != 200 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	p := q.updates[0]
	plain, _ := kr.Open(*p.CredentialsEnc, catalogs.CredentialsAAD(q.conn.ID))
	if !strings.Contains(string(plain), "new-s3cr3t") || strings.Contains(string(plain), "new-id") {
		t.Fatalf("sealed = %s", plain)
	}
	if !strings.Contains(string(p.Config), `"client_id":"new-id"`) {
		t.Fatalf("config = %s", p.Config)
	}
}

// TestUpdateConnection_ConfigWinsOverOldBlob: a row written before the config
// column keeps client_id in its blob; after a config update the projection
// must read the new value.
func TestUpdateConnection_ConfigWinsOverOldBlob(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	c := testConnection(t, false, ownerUUID(t, testUserID))
	c.Config = []byte(`{"url":"http://polaris:8181"}`) // backfilled by the migration
	enc, _ := kr.Seal([]byte(`{"client_id":"old-id","client_secret":"sec"}`), catalogs.CredentialsAAD(c.ID))
	c.CredentialsEnc = &enc
	q := &updateConnQuerier{conn: c}
	if rec := runUpdate(t, createHandlers(t, q, kr), UpdateCatalogConnectionRequest{Config: strMap("client_id", "new-id")}); rec.Code != 200 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	p := q.updates[0]
	creds, err := catalogs.DecodeCredentials(db.CatalogConnection{ID: c.ID, Config: p.Config, CredentialsEnc: p.CredentialsEnc}, kr)
	if err != nil || creds.ClientID != "new-id" || creds.ClientSecret != "sec" {
		t.Fatalf("projection credentials = %+v, %v", creds, err)
	}
}

func TestUpdateConnection_RejectsInvalid(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	q := &updateConnQuerier{conn: storedPolaris(t, kr)}
	rec := runUpdate(t, createHandlers(t, q, kr), UpdateCatalogConnectionRequest{Config: strMap("bogus", "1")})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), `"field":"bogus"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	if len(q.updates) != 0 {
		t.Fatal("an invalid update was written")
	}
}

// TestUpdateConnection_EncryptionFailureIs500: a credentials PATCH with no key
// answers a typed 500 and writes nothing.
func TestUpdateConnection_EncryptionFailureIs500(t *testing.T) {
	t.Parallel()
	owner := ownerUUID(t, testUserID)
	q := &updateConnQuerier{conn: testConnection(t, true, owner)}
	h := createHandlers(t, q, nil)
	rec := runUpdate(t, h, UpdateCatalogConnectionRequest{Credentials: anyMap("client_secret", "p-s3cr3t")})
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"encryption_failed"`) {
		t.Fatalf("response = %d %s, want 500 encryption_failed", rec.Code, rec.Body)
	}
	if len(q.updates) != 0 {
		t.Fatal("an update was written without a key")
	}
}

func TestCreateConnection_CredentialsReadOnly(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	q := &recordingConnQuerier{}
	readOnly := true
	body := &CreateCatalogConnectionRequest{
		Name: "pg", Type: "postgresql",
		Config:              strMap("host", "db", "username", "u"),
		Credentials:         anyMap("password", "p"),
		CredentialsReadOnly: &readOnly,
	}
	resp, err := createHandlers(t, q, kr).CreateCatalogConnection(testContext(), CreateCatalogConnectionRequestObject{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if rec := visitCreate(t, resp); rec.Code != 201 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	if !q.created[0].CredentialsReadOnly {
		t.Fatal("credentials_read_only was not stored")
	}
}

func TestUpdateConnection_CredentialsReadOnly(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	q := &updateConnQuerier{conn: storedPolaris(t, kr)}
	readOnly := true
	if rec := runUpdate(t, createHandlers(t, q, kr), UpdateCatalogConnectionRequest{CredentialsReadOnly: &readOnly}); rec.Code != 200 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	got := q.updates[0].CredentialsReadOnly
	if got == nil || !*got {
		t.Fatalf("update params = %+v", q.updates[0])
	}
}

func TestListConnectionProviders(t *testing.T) {
	t.Parallel()
	h := createHandlers(t, nil, nil)
	resp, err := h.ListConnectionProviders(testContext(), ListConnectionProvidersRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	list := resp.(ListConnectionProviders200JSONResponse)
	kinds := map[string]ConnectionProvider{}
	for _, p := range list {
		kinds[p.Kind] = p
	}
	for _, k := range []string{"iceberg", "mysql", "nessie", "polaris", "postgresql"} {
		if _, ok := kinds[k]; !ok {
			t.Errorf("provider %q missing", k)
		}
	}
	pg := kinds["postgresql"]
	if pg.EngineKind == nil || *pg.EngineKind != "postgresql" || pg.Family != "database" {
		t.Errorf("postgresql = %+v", pg)
	}
	secret := map[string]bool{}
	for _, f := range pg.ConfigSchema {
		secret[f.Key] = f.Secret
	}
	if !secret["password"] || secret["username"] {
		t.Errorf("secret flags = %v", secret)
	}
}

// ── who may manage connections (#512) ────────────────────────────────────────

func adminsOnlyHandlers(t *testing.T, q db.Querier, kr *crypto.Keyring) *API {
	t.Helper()
	return testAPI(handlerFields{queries: q, tx: directTx{q: q}, keyring: kr, connections: testRegistry(t), settings: SettingsFromConfig(&config.Config{ConnectionsCreate: "admins"}), catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})
}

func roleContext(role string) context.Context {
	return context.WithValue(testContext(), auth.ContextKeyRole, role)
}

// TestMayManageConnections: everyone by default; with
// BALEIA_CONNECTIONS_CREATE=admins only admins, which is what both handlers and
// the create_connection global action use.
func TestMayManageConnections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		create   string
		role     string
		expected bool
	}{
		{name: "member by default", role: "member", expected: true},
		{name: "member with everyone", create: "everyone", role: "member", expected: true},
		{name: "member with admins", create: "admins", role: "member", expected: false},
		{name: "admin with admins", create: "admins", role: authz.RoleAdmin, expected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := testAPI(handlerFields{settings: SettingsFromConfig(&config.Config{ConnectionsCreate: tt.create})})
			if got := h.mayManageConnections(authz.Principal{Role: tt.role}); got != tt.expected {
				t.Errorf("mayManageConnections = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestCreateConnection_AdminsOnly: a member is refused before anything is
// validated or stored; an admin still creates.
func TestCreateConnection_AdminsOnly(t *testing.T) {
	t.Parallel()
	body := validCreateRequests()["polaris"]

	q := &recordingConnQuerier{}
	resp, err := adminsOnlyHandlers(t, q, testKeyring(t)).CreateCatalogConnection(roleContext("member"), CreateCatalogConnectionRequestObject{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if rec := visitCreate(t, resp); rec.Code != 403 || !strings.Contains(rec.Body.String(), `"forbidden"`) {
		t.Fatalf("member: %d %s", rec.Code, rec.Body)
	}
	if len(q.created) != 0 {
		t.Fatalf("member stored %d connections", len(q.created))
	}

	q = &recordingConnQuerier{}
	resp, err = adminsOnlyHandlers(t, q, testKeyring(t)).CreateCatalogConnection(roleContext(authz.RoleAdmin), CreateCatalogConnectionRequestObject{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if rec := visitCreate(t, resp); rec.Code != 201 || len(q.created) != 1 {
		t.Fatalf("admin: %d %s, stored %d", rec.Code, rec.Body, len(q.created))
	}
}

// TestUpdateConnection_AdminsOnly: owning a connection is not enough to
// re-point it when only admins manage connections.
func TestUpdateConnection_AdminsOnly(t *testing.T) {
	t.Parallel()
	kr := testKeyring(t)
	name := "renamed"
	for _, c := range []struct {
		role     string
		expected int
		updates  int
	}{
		{role: "member", expected: 403},
		{role: authz.RoleAdmin, expected: 200, updates: 1},
	} {
		q := &updateConnQuerier{conn: storedPolaris(t, kr)}
		resp, err := adminsOnlyHandlers(t, q, kr).UpdateCatalogConnection(roleContext(c.role), UpdateCatalogConnectionRequestObject{Id: [16]byte{0x01}, Body: &UpdateCatalogConnectionRequest{Name: &name}})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		if err := resp.VisitUpdateCatalogConnectionResponse(rec); err != nil {
			t.Fatal(err)
		}
		if rec.Code != c.expected || len(q.updates) != c.updates {
			t.Errorf("%s: %d %s, %d updates", c.role, rec.Code, rec.Body, len(q.updates))
		}
	}
}
