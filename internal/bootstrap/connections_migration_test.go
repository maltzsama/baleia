package bootstrap

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// openIsolatedStore creates a throwaway database and migrates it. The split
// test needs the real dialect: the credentials use the connection id as AAD
// and the tables carry the constraints the migration relies on.
func openIsolatedStore(t *testing.T) *gorm.DB {
	t.Helper()
	adminURL := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := store.Open(ctx, adminURL, store.Postgres, store.Options{MaxOpenConns: 1, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(admin) })

	name := "baleia_split_" + strings.ToLower(rand.Text()[:12])
	if err := admin.WithContext(ctx).Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)").Error
	})

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	isolated, err := store.Open(ctx, u.String(), store.Postgres, store.Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close(isolated) })
	if err := store.AutoMigrate(isolated, store.Models()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return isolated
}

// TestSplitDualPathConnections splits a connection that has both a direct
// engine and Trino source catalogs: the original keeps its id for the Trino
// path, a new connection takes the direct engine with its credentials re-sealed
// under the new identity, and the catalog stays on the original.
func TestSplitDualPathConnections(t *testing.T) {
	t.Parallel()
	store := openIsolatedStore(t)
	ctx := context.Background()
	kr, err := crypto.NewKeyring("key-a-0123456789abcdef0123456789abcdef", "")
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}

	origID := model.UUID(uuid.NewString())
	origDB, err := aadFor(origID)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := kr.Seal([]byte(`{"password":"s3cret"}`), origDB)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	orig := model.CatalogConnection{
		ID: origID, Name: "analytics", Type: "postgresql",
		URL: "jdbc:postgresql://db/analytics", Config: model.JSON(`{"host":"db","database":"analytics"}`),
		CredentialsEnc: &sealed, CredentialsReadOnly: true,
		CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}
	if err := store.Create(&orig).Error; err != nil {
		t.Fatalf("create connection: %v", err)
	}
	engine := model.Engine{
		ID: model.UUID(uuid.NewString()), Name: "analytics-direct", Kind: "postgresql",
		Class: "direct", ConnectionID: origID, IdentityMode: "connection", Enabled: true,
		CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}
	if err := store.Create(&engine).Error; err != nil {
		t.Fatalf("create engine: %v", err)
	}
	cluster := model.TrinoCluster{
		ID: model.UUID(uuid.NewString()), Name: "test", CoordinatorURL: "http://trino:8080",
		CreatedAt: nowUTC(),
	}
	if err := store.Create(&cluster).Error; err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	trinoName := model.Text("analytics")
	wh := "warehouse"
	connector := model.Text("postgresql")
	catalog := model.Catalog{
		ID: model.UUID(uuid.NewString()), ConnectionID: origID, Name: "analytics",
		TrinoCatalogName: &trinoName, ClusterID: &cluster.ID, Warehouse: &wh, Connector: &connector,
		Origin: "source", Enabled: true, CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}
	if err := store.Create(&catalog).Error; err != nil {
		t.Fatalf("create catalog: %v", err)
	}
	native := model.Catalog{
		ID: model.UUID(uuid.NewString()), ConnectionID: origID, Name: "native_lake",
		Origin: "native", Enabled: true, CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}
	if err := store.Create(&native).Error; err != nil {
		t.Fatalf("create native catalog: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	if err := SplitDualPathConnections(ctx, store, kr, logger); err != nil {
		t.Fatalf("split: %v", err)
	}

	var updated model.CatalogConnection
	if err := store.Where("id = ?", origID).First(&updated).Error; err != nil {
		t.Fatal(err)
	}
	if updated.AccessMode == nil || *updated.AccessMode != "trino" {
		t.Fatalf("original access_mode = %v, want trino", updated.AccessMode)
	}

	var direct model.CatalogConnection
	if err := store.Where("type = ? AND access_mode = ? AND id <> ?", "postgresql", "direct", origID).
		First(&direct).Error; err != nil {
		t.Fatalf("direct connection not created: %v", err)
	}
	if direct.Name == orig.Name {
		t.Errorf("direct connection reused the original name %q", direct.Name)
	}
	if string(direct.Config) != string(orig.Config) {
		t.Errorf("config = %s, want %s", direct.Config, orig.Config)
	}
	if !direct.CredentialsReadOnly {
		t.Error("credentials_read_only not preserved")
	}
	if direct.CredentialsEnc == nil {
		t.Fatal("direct connection has no credentials")
	}
	newDB, err := aadFor(direct.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := kr.Open(*direct.CredentialsEnc, newDB)
	if err != nil {
		t.Fatalf("re-sealed credentials do not open under the new id: %v", err)
	}
	if string(plain) != `{"password":"s3cret"}` {
		t.Fatalf("credentials = %s", plain)
	}

	var moved model.Engine
	if err := store.Where("id = ?", engine.ID).First(&moved).Error; err != nil {
		t.Fatal(err)
	}
	if moved.ConnectionID != direct.ID {
		t.Errorf("engine connection_id = %s, want %s", moved.ConnectionID, direct.ID)
	}

	var kept model.Catalog
	if err := store.Where("id = ?", catalog.ID).First(&kept).Error; err != nil {
		t.Fatal(err)
	}
	if kept.ConnectionID != origID {
		t.Errorf("catalog connection_id = %s, want original %s", kept.ConnectionID, origID)
	}

	var movedNative model.Catalog
	if err := store.Where("id = ?", native.ID).First(&movedNative).Error; err != nil {
		t.Fatal(err)
	}
	if movedNative.ConnectionID != direct.ID {
		t.Errorf("native catalog connection_id = %s, want direct %s", movedNative.ConnectionID, direct.ID)
	}

	// Idempotent: a second pass changes nothing.
	if err := SplitDualPathConnections(ctx, store, kr, logger); err != nil {
		t.Fatalf("second split: %v", err)
	}
	var count int64
	store.Model(&model.CatalogConnection{}).Where("access_mode = ?", "direct").Count(&count)
	if count != 1 {
		t.Fatalf("direct connections after second pass = %d, want 1", count)
	}
}

// TestSplitDualPathConnectionsLeavesUnreadableCredentials confirms a connection
// whose credentials cannot be re-sealed is left intact, never dropped.
func TestSplitDualPathConnectionsLeavesUnreadableCredentials(t *testing.T) {
	t.Parallel()
	store := openIsolatedStore(t)
	ctx := context.Background()
	kr, err := crypto.NewKeyring("key-a-0123456789abcdef0123456789abcdef", "")
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	origID := model.UUID(uuid.NewString())
	foreign, err := crypto.NewKeyring("key-b-0123456789abcdef0123456789abcdef", "")
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	origDB, _ := aadFor(origID)
	sealed, err := foreign.Seal([]byte(`{"password":"x"}`), origDB)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	orig := model.CatalogConnection{
		ID: origID, Name: "legacy", Type: "postgresql", URL: "jdbc:postgresql://db",
		Config: model.JSON(`{}`), CredentialsEnc: &sealed, CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}
	if err := store.Create(&orig).Error; err != nil {
		t.Fatal(err)
	}
	engine := model.Engine{
		ID: model.UUID(uuid.NewString()), Name: "legacy-direct", Kind: "postgresql",
		Class: "direct", ConnectionID: origID, IdentityMode: "connection", Enabled: true,
		CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}
	if err := store.Create(&engine).Error; err != nil {
		t.Fatal(err)
	}
	cluster := model.TrinoCluster{
		ID: model.UUID(uuid.NewString()), Name: "test", CoordinatorURL: "http://trino:8080",
		CreatedAt: nowUTC(),
	}
	if err := store.Create(&cluster).Error; err != nil {
		t.Fatal(err)
	}
	trinoName := model.Text("legacy")
	wh := "warehouse"
	connector := model.Text("postgresql")
	catalog := model.Catalog{
		ID: model.UUID(uuid.NewString()), ConnectionID: origID, Name: "legacy",
		TrinoCatalogName: &trinoName, ClusterID: &cluster.ID, Warehouse: &wh, Connector: &connector,
		Origin: "source", Enabled: true, CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}
	if err := store.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}

	if err := SplitDualPathConnections(ctx, store, kr, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("split: %v", err)
	}

	var kept model.CatalogConnection
	if err := store.Where("id = ?", origID).First(&kept).Error; err != nil {
		t.Fatal(err)
	}
	if kept.AccessMode != nil {
		t.Fatalf("unsplit connection access_mode = %v, want NULL", kept.AccessMode)
	}
	var direct int64
	store.Model(&model.CatalogConnection{}).Where("access_mode = ?", "direct").Count(&direct)
	if direct != 0 {
		t.Fatalf("created %d direct connections, want 0", direct)
	}
}

func nowUTC() time.Time { return time.Now().UTC() }
