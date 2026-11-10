package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"gorm.io/gorm"
)

func TestAPIKeysRoundTrip(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)

	ctx := context.Background()
	store := NewAPIKeys(gdb)
	first := createAPIKeyUser(t, gdb, "first@example.test")
	second := createAPIKeyUser(t, gdb, "second@example.test")
	firstID := PGUUID(string(first.ID))
	secondID := PGUUID(string(second.ID))

	firstKey, err := store.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID:  firstID,
		Name:    "first",
		KeyHash: "hash-first",
		Prefix:  "bal_",
		Scopes:  []string{"*"},
	})
	if err != nil {
		t.Fatalf("CreateAPIKey(first): %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID:  secondID,
		Name:    "second",
		KeyHash: "hash-second",
		Prefix:  "bal_",
		Scopes:  []string{"*"},
	}); err != nil {
		t.Fatalf("CreateAPIKey(second): %v", err)
	}

	// Each listing sees only its owner's key.
	firstKeys, err := store.ListAPIKeysByUser(ctx, firstID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser(first): %v", err)
	}
	if len(firstKeys) != 1 || firstKeys[0].Name != "first" {
		t.Fatalf("first keys = %+v, want only first", firstKeys)
	}
	secondKeys, err := store.ListAPIKeysByUser(ctx, secondID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser(second): %v", err)
	}
	if len(secondKeys) != 1 || secondKeys[0].Name != "second" {
		t.Fatalf("second keys = %+v, want only second", secondKeys)
	}

	// The wrong owner cannot delete the key.
	if err := store.DeleteAPIKey(ctx, db.DeleteAPIKeyParams{ID: firstKey.ID, UserID: secondID}); err != nil {
		t.Fatalf("DeleteAPIKey(wrong owner): %v", err)
	}
	firstKeys, err = store.ListAPIKeysByUser(ctx, firstID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser after wrong delete: %v", err)
	}
	if len(firstKeys) != 1 {
		t.Fatalf("first keys after wrong-owner delete = %+v, want the key", firstKeys)
	}

	if err := store.DeleteAPIKey(ctx, db.DeleteAPIKeyParams{ID: firstKey.ID, UserID: firstID}); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	firstKeys, err = store.ListAPIKeysByUser(ctx, firstID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser after delete: %v", err)
	}
	if len(firstKeys) != 0 {
		t.Fatalf("first keys = %+v, want none", firstKeys)
	}
}

func createAPIKeyUser(t *testing.T, gdb *gorm.DB, email string) model.User {
	t.Helper()
	hash := "x"
	user := model.User{
		ID:           model.UUID(NewUUID()),
		Name:         email,
		Email:        model.Text(email),
		PasswordHash: &hash,
		Role:         "member",
		AuthProvider: "local",
		Status:       "active",
		Kind:         "human",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := gdb.Create(&user).Error; err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return user
}

// TestBIKeysStore covers the workspace-bound key kind (#708): a BI key is
// listed by workspace, never by the personal key listing, and deleting a
// personal key cannot remove it.
func TestBIKeysStore(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	ctx := context.Background()
	store := NewAPIKeys(gdb)
	user := createAPIKeyUser(t, gdb, "bi@example.test")
	userID := PGUUID(string(user.ID))

	now := time.Now().UTC()
	conn := model.CatalogConnection{ID: model.UUID(NewUUID()), Name: model.Text("bi-" + NewUUID()[:8]), Type: "trino", URL: "http://trino.test", CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&conn).Error; err != nil {
		t.Fatal(err)
	}
	engine := model.Engine{ID: model.UUID(NewUUID()), Name: model.Text("bi-" + NewUUID()[:8]), Kind: "trino", Class: "federated", ConnectionID: conn.ID, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&engine).Error; err != nil {
		t.Fatal(err)
	}
	ws := model.Workspace{ID: model.UUID(NewUUID()), Name: "BI", Type: "personal", PolarisCatalogName: model.Text("ws_bi_" + NewUUID()[:8]), OwnerUserID: &user.ID, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&ws).Error; err != nil {
		t.Fatal(err)
	}
	catalog := model.Catalog{ID: model.UUID(NewUUID()), ConnectionID: conn.ID, Name: model.Text("lake"), Origin: "native", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Create(&model.CatalogBinding{ID: model.UUID(NewUUID()), CatalogID: catalog.ID, EngineID: engine.ID, EngineCatalogName: "lake", Mode: "native", Enabled: true, LifecycleState: "active", CreatedAt: now, UpdatedAt: now, StateChangedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Create(&model.WorkspaceCatalogGrant{WorkspaceID: ws.ID, CatalogID: catalog.ID, GrantedBy: user.ID, GrantedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	personal, err := store.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: userID, Name: "personal", KeyHash: "hash-personal", Prefix: "bal_", Scopes: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	wsID := PGUUID(string(ws.ID))
	engineID := PGUUID(string(engine.ID))
	bi, err := store.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID: userID, Kind: "bi", WorkspaceID: wsID, EngineID: engineID,
		Name: "bi", KeyHash: "hash-bi", Prefix: "bal_", Scopes: []string{"bi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if bi.Kind != "bi" || !bi.WorkspaceID.Valid || !bi.EngineID.Valid || bi.WorkspaceID.Bytes != wsID.Bytes || bi.EngineID.Bytes != engineID.Bytes {
		t.Fatalf("bi key = %+v", bi)
	}
	got, err := store.GetAPIKeyByHash(ctx, "hash-bi")
	if err != nil || got.Kind != "bi" {
		t.Fatalf("GetAPIKeyByHash = %+v, %v", got, err)
	}

	personalKeys, err := store.ListAPIKeysByUser(ctx, userID)
	if err != nil || len(personalKeys) != 1 || personalKeys[0].ID.Bytes != personal.ID.Bytes {
		t.Fatalf("personal keys = %+v, %v", personalKeys, err)
	}
	biKeys, err := store.ListBIKeysByWorkspace(ctx, wsID)
	if err != nil || len(biKeys) != 1 || biKeys[0].ID.Bytes != bi.ID.Bytes {
		t.Fatalf("workspace keys = %+v, %v", biKeys, err)
	}
	ownKeys, err := store.ListBIKeysByWorkspaceOwner(ctx, wsID, userID)
	if err != nil || len(ownKeys) != 1 {
		t.Fatalf("owner keys = %+v, %v", ownKeys, err)
	}
	if _, err := store.GetBIKeyByID(ctx, bi.ID, PGUUID(NewUUID())); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("foreign workspace = %v", err)
	}

	// The personal delete path leaves the BI key alone.
	if err := store.DeleteAPIKey(ctx, db.DeleteAPIKeyParams{ID: bi.ID, UserID: userID}); err != nil {
		t.Fatal(err)
	}
	if biKeys, _ := store.ListBIKeysByWorkspace(ctx, wsID); len(biKeys) != 1 {
		t.Fatal("a personal delete removed a BI key")
	}
	if n, err := store.DeleteBIKey(ctx, bi.ID, wsID); err != nil || n != 1 {
		t.Fatalf("DeleteBIKey = %d, %v", n, err)
	}
	if biKeys, _ := store.ListBIKeysByWorkspace(ctx, wsID); len(biKeys) != 0 {
		t.Fatal("the BI key survived its delete")
	}
}
