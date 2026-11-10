package bikeys

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

var (
	wsID     = id()
	engineID = id()
	actorID  = id()
	otherID  = id()
	saID     = id()
)

func id() db.UUID { return db.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	users     map[[16]byte]db.User
	members   map[[16]byte]bool
	keys      []db.ApiKey
	targets   []db.ListWorkspaceTargetsRow
	createArg *db.CreateAPIKeyParams
	touched   bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users: map[[16]byte]db.User{
			actorID.Bytes: {ID: actorID, Email: "member@example.test", Status: "active", Kind: "human"},
			otherID.Bytes: {ID: otherID, Email: "other@example.test", Status: "active", Kind: "human"},
			saID.Bytes:    {ID: saID, Email: "sa@service-accounts.baleia.invalid", Status: "active", Kind: "service_account"},
		},
		members: map[[16]byte]bool{actorID.Bytes: true, otherID.Bytes: true, saID.Bytes: true},
		targets: []db.ListWorkspaceTargetsRow{{
			EngineID: engineID, EngineCatalogName: "lake", EngineKind: "trino",
			BindingEnabled: true, EngineEnabled: true, CatalogEnabled: true,
		}},
	}
}

func (f *fakeStore) CreateAPIKey(_ context.Context, arg db.CreateAPIKeyParams) (db.ApiKey, error) {
	f.createArg = &arg
	key := db.ApiKey{
		ID: id(), UserID: arg.UserID, Kind: arg.Kind, WorkspaceID: arg.WorkspaceID, EngineID: arg.EngineID,
		Name: arg.Name, KeyHash: arg.KeyHash, Prefix: arg.Prefix, Scopes: arg.Scopes, ExpiresAt: arg.ExpiresAt,
	}
	f.keys = append(f.keys, key)
	return key, nil
}

func (f *fakeStore) GetAPIKeyByHash(_ context.Context, hash string) (db.ApiKey, error) {
	for _, k := range f.keys {
		if k.KeyHash == hash {
			return k, nil
		}
	}
	return db.ApiKey{}, db.ErrNoRows
}

func (f *fakeStore) GetUserByID(_ context.Context, id db.UUID) (db.User, error) {
	u, ok := f.users[id.Bytes]
	if !ok {
		return db.User{}, db.ErrNoRows
	}
	return u, nil
}

func (f *fakeStore) CheckWorkspaceAccess(_ context.Context, arg db.CheckWorkspaceAccessParams) (bool, error) {
	return f.members[arg.UserID.Bytes], nil
}

func (f *fakeStore) ListWorkspaceTargets(context.Context, db.UUID) ([]db.ListWorkspaceTargetsRow, error) {
	return f.targets, nil
}

func (f *fakeStore) ListBIKeysByWorkspace(_ context.Context, workspaceID db.UUID) ([]db.ApiKey, error) {
	var out []db.ApiKey
	for _, k := range f.keys {
		if k.Kind == "bi" && k.WorkspaceID.Bytes == workspaceID.Bytes {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeStore) ListBIKeysByWorkspaceOwner(_ context.Context, workspaceID, userID db.UUID) ([]db.ApiKey, error) {
	var out []db.ApiKey
	for _, k := range f.keys {
		if k.Kind == "bi" && k.WorkspaceID.Bytes == workspaceID.Bytes && k.UserID.Bytes == userID.Bytes {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeStore) GetBIKeyByID(_ context.Context, id, workspaceID db.UUID) (db.ApiKey, error) {
	for _, k := range f.keys {
		if k.ID.Bytes == id.Bytes && k.Kind == "bi" && k.WorkspaceID.Bytes == workspaceID.Bytes {
			return k, nil
		}
	}
	return db.ApiKey{}, db.ErrNoRows
}

func (f *fakeStore) DeleteBIKey(_ context.Context, id, workspaceID db.UUID) (int64, error) {
	for i, k := range f.keys {
		if k.ID.Bytes == id.Bytes && k.Kind == "bi" && k.WorkspaceID.Bytes == workspaceID.Bytes {
			f.keys = append(f.keys[:i], f.keys[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeStore) TouchAPIKey(context.Context, db.UUID) error {
	f.touched = true
	return nil
}

func createParams() CreateParams {
	return CreateParams{
		WorkspaceID: wsID, EngineID: engineID, ActorID: actorID, Name: "Tableau",
	}
}

func TestCreateSelf(t *testing.T) {
	t.Parallel()
	st := newFakeStore()
	key, secret, err := New(st).Create(context.Background(), createParams())
	if err != nil {
		t.Fatal(err)
	}
	if key.Kind != "bi" || key.WorkspaceID.Bytes != wsID.Bytes || key.EngineID.Bytes != engineID.Bytes {
		t.Fatalf("key = %+v", key)
	}
	if key.UserID.Bytes != actorID.Bytes || key.Name != "Tableau" {
		t.Fatalf("key = %+v", key)
	}
	if len(key.Scopes) != 1 || key.Scopes[0] != "bi" {
		t.Fatalf("scopes = %v", key.Scopes)
	}
	if !secretPrefixed(secret) || key.KeyHash != auth.HashAPIKey(secret) || key.Prefix != secret[:8] {
		t.Fatalf("secret = %q, stored = %+v", secret, key)
	}
}

func secretPrefixed(s string) bool { return len(s) > 40 && s[:4] == "bal_" }

func TestCreateForServiceAccountNeedsWorkspaceAdmin(t *testing.T) {
	t.Parallel()
	st := newFakeStore()
	p := createParams()
	p.OwnerID = saID
	if _, _, err := New(st).Create(context.Background(), p); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member creating for a service account = %v, want ErrForbidden", err)
	}
	p.ActorIsWorkspaceAdmin = true
	key, _, err := New(st).Create(context.Background(), p)
	if err != nil {
		t.Fatalf("workspace admin creating for a service account: %v", err)
	}
	if key.UserID.Bytes != saID.Bytes {
		t.Fatalf("owner = %v, want the service account", key.UserID)
	}
}

func TestCreateForAnotherHumanRefused(t *testing.T) {
	t.Parallel()
	st := newFakeStore()
	p := createParams()
	p.OwnerID = otherID
	p.ActorIsWorkspaceAdmin = true
	if _, _, err := New(st).Create(context.Background(), p); !errors.Is(err, ErrForbidden) {
		t.Fatalf("creating for another person = %v, want ErrForbidden", err)
	}
}

func TestCreateRequiresMembershipAndTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newFakeStore()
	st.members[actorID.Bytes] = false
	if _, _, err := New(st).Create(ctx, createParams()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-member = %v, want ErrForbidden", err)
	}

	st = newFakeStore()
	st.targets[0].BindingEnabled = false
	if _, _, err := New(st).Create(ctx, createParams()); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("disabled binding = %v, want ErrTargetUnavailable", err)
	}

	st = newFakeStore()
	st.targets[0].EngineKind = "postgresql"
	if _, _, err := New(st).Create(ctx, createParams()); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("non-trino target = %v, want ErrTargetUnavailable", err)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	past := time.Now().Add(-time.Minute)
	cases := map[string]CreateParams{
		"no workspace": {EngineID: engineID, ActorID: actorID, Name: "x"},
		"no engine":    {WorkspaceID: wsID, ActorID: actorID, Name: "x"},
		"no name":      {WorkspaceID: wsID, EngineID: engineID, ActorID: actorID},
		"past expiry":  {WorkspaceID: wsID, EngineID: engineID, ActorID: actorID, Name: "x", ExpiresAt: &past},
	}
	for name, p := range cases {
		if _, _, err := New(newFakeStore()).Create(ctx, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s = %v, want ErrInvalid", name, err)
		}
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newFakeStore()
	s := New(st)
	_, secret, err := s.Create(ctx, createParams())
	if err != nil {
		t.Fatal(err)
	}
	cred, err := s.Authenticate(ctx, "member@example.test", secret)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if cred.User.ID.Bytes != actorID.Bytes || cred.Key.Kind != "bi" {
		t.Fatalf("credential = %+v", cred)
	}
	if !st.touched {
		t.Fatal("a valid key is touched")
	}

	if _, err := s.Authenticate(ctx, "other@example.test", secret); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong e-mail = %v, want ErrInvalid", err)
	}
	if _, err := s.Authenticate(ctx, "member@example.test", "bal_wrong"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong secret = %v, want ErrInvalid", err)
	}
}

func TestAuthenticateRechecksMembershipAndTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newFakeStore()
	s := New(st)
	_, secret, err := s.Create(ctx, createParams())
	if err != nil {
		t.Fatal(err)
	}
	st.members[actorID.Bytes] = false
	if _, err := s.Authenticate(ctx, "member@example.test", secret); !errors.Is(err, ErrInvalid) {
		t.Fatalf("removed member = %v, want ErrInvalid", err)
	}

	st = newFakeStore()
	s = New(st)
	_, secret, _ = s.Create(ctx, createParams())
	st.targets = nil
	if _, err := s.Authenticate(ctx, "member@example.test", secret); !errors.Is(err, ErrInvalid) {
		t.Fatalf("target removed = %v, want ErrInvalid", err)
	}
}

func TestAuthenticateRejectsPersonalKeys(t *testing.T) {
	t.Parallel()
	st := newFakeStore()
	key, err := st.CreateAPIKey(context.Background(), db.CreateAPIKeyParams{
		UserID: actorID, Kind: "user", Name: "personal", KeyHash: auth.HashAPIKey("bal_personal"), Scopes: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if key.Kind != "user" {
		t.Fatal("fixture")
	}
	if _, err := New(st).Authenticate(context.Background(), "member@example.test", "bal_personal"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("personal key = %v, want ErrInvalid", err)
	}
}

func TestListAndRevoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newFakeStore()
	s := New(st)
	own, _, _ := s.Create(ctx, createParams())
	other, _, err := s.Create(ctx, CreateParams{
		WorkspaceID: wsID, EngineID: engineID, ActorID: actorID, OwnerID: saID, ActorIsWorkspaceAdmin: true, Name: "sa",
	})
	if err != nil {
		t.Fatal(err)
	}

	member, err := s.List(ctx, wsID, actorID, false)
	if err != nil || len(member) != 1 || member[0].ID.Bytes != own.ID.Bytes {
		t.Fatalf("member list = %v, %v", member, err)
	}
	admin, err := s.List(ctx, wsID, actorID, true)
	if err != nil || len(admin) != 2 {
		t.Fatalf("admin list = %v, %v", admin, err)
	}

	if _, err := s.Revoke(ctx, wsID, other.ID, actorID, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member revoking another key = %v, want ErrForbidden", err)
	}
	if _, err := s.Revoke(ctx, wsID, own.ID, actorID, false); err != nil {
		t.Fatalf("owner revoking own key: %v", err)
	}
	if _, err := s.Revoke(ctx, wsID, other.ID, actorID, true); err != nil {
		t.Fatalf("admin revoking a service-account key: %v", err)
	}
	if _, err := s.Revoke(ctx, wsID, other.ID, actorID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking twice = %v, want ErrNotFound", err)
	}
}
