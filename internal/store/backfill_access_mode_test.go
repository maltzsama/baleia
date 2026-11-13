package store

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// openBackfillTestDB creates a throwaway migrated database for the access-mode
// backfill test.
func openBackfillTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	adminURL := postgresAdminURL(t)
	ctx := context.Background()
	admin, err := Open(ctx, adminURL, Postgres, Options{MaxOpenConns: 1, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close(admin) })

	name := "baleia_backfill_" + strings.ToLower(rand.Text()[:12])
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
	db, err := Open(ctx, u.String(), Postgres, Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = Close(db) })
	if err := AutoMigrate(db, Models()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestBackfillAccessMode pins the single-path rule: a direct engine without
// source catalogs is direct, source catalogs without a direct engine are federated,
// and a connection with both (or neither) stays NULL for the split.
func TestBackfillAccessMode(t *testing.T) {
	t.Parallel()
	db := openBackfillTestDB(t)
	now := time.Now().UTC()

	cluster := model.TrinoCluster{ID: model.UUID(uuid.NewString()), Name: "c", CoordinatorURL: "http://trino:8080", CreatedAt: now}
	if err := db.Create(&cluster).Error; err != nil {
		t.Fatal(err)
	}

	newConn := func(name, typ string) model.CatalogConnection {
		c := model.CatalogConnection{
			ID: model.UUID(uuid.NewString()), Name: model.Text(name), Type: typ,
			URL: "jdbc:postgresql://db", Config: model.JSON(`{}`), CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(&c).Error; err != nil {
			t.Fatalf("create connection %s: %v", name, err)
		}
		return c
	}
	newEngine := func(conn model.CatalogConnection, class string) {
		e := model.Engine{
			ID: model.UUID(uuid.NewString()), Name: model.Text(string(conn.Name) + "-" + class),
			Kind: "postgresql", Class: class, ConnectionID: conn.ID, IdentityMode: "connection",
			Enabled: true, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(&e).Error; err != nil {
			t.Fatalf("create engine: %v", err)
		}
	}
	newSourceCatalog := func(conn model.CatalogConnection) {
		trinoName := model.Text(string(conn.Name))
		wh := "warehouse"
		connector := model.Text("postgresql")
		c := model.Catalog{
			ID: model.UUID(uuid.NewString()), ConnectionID: conn.ID, Name: model.Text("cat-" + string(conn.Name)),
			TrinoCatalogName: &trinoName, ClusterID: &cluster.ID, Warehouse: &wh, Connector: &connector,
			Origin: "source", Enabled: true, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(&c).Error; err != nil {
			t.Fatalf("create catalog: %v", err)
		}
	}

	directOnly := newConn("direct_only", "postgresql")
	newEngine(directOnly, "direct")

	federatedOnly := newConn("federated_only", "postgresql")
	newSourceCatalog(federatedOnly)

	dual := newConn("dual", "postgresql")
	newEngine(dual, "direct")
	newSourceCatalog(dual)

	unused := newConn("unused", "trino")

	// A database that reaches migration 8 from an older version still has the
	// check that allows only direct and trino; the backfill must replace it.
	if err := db.Migrator().DropConstraint(&model.CatalogConnection{}, accessModeCheck); err != nil {
		t.Fatalf("drop check: %v", err)
	}
	if err := db.Exec("ALTER TABLE catalog_connections ADD CONSTRAINT " + accessModeCheck +
		" CHECK (access_mode IS NULL OR access_mode IN ('direct', 'trino'))").Error; err != nil {
		t.Fatalf("restore the old check: %v", err)
	}

	if err := backfillAccessMode(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	modeOf := func(c model.CatalogConnection) *string {
		var row model.CatalogConnection
		if err := db.Where("id = ?", c.ID).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row.AccessMode
	}
	if m := modeOf(directOnly); m == nil || *m != "direct" {
		t.Errorf("direct-only mode = %v, want direct", m)
	}
	if m := modeOf(federatedOnly); m == nil || *m != "federated" {
		t.Errorf("federated-only mode = %v, want federated", m)
	}
	if m := modeOf(dual); m != nil {
		t.Errorf("dual-path mode = %v, want NULL (split handles it)", m)
	}
	if m := modeOf(unused); m != nil {
		t.Errorf("unused mode = %v, want NULL", m)
	}
}
