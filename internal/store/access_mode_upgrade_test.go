package store_test

import (
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestRenameTrinoAccessModeUpgrades takes the access-mode check back to the one
// that allowed "trino", stores rows of every mode, applies the migration and
// checks the rewrite, the new check and that a rerun changes nothing.
func TestRenameTrinoAccessModeUpgrades(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)

	const name = "catalog_connections_access_mode_check"
	if err := db.Migrator().DropConstraint(&model.CatalogConnection{}, name); err != nil {
		t.Fatalf("drop check: %v", err)
	}
	if err := db.Exec("ALTER TABLE catalog_connections ADD CONSTRAINT " + name +
		" CHECK (access_mode IS NULL OR access_mode IN ('direct', 'trino'))").Error; err != nil {
		t.Fatalf("restore the old check: %v", err)
	}

	now := time.Now().UTC()
	conn := func(n int, mode *string) model.CatalogConnection {
		return model.CatalogConnection{
			ID: model.UUID(fixtureUUID(100 + n)), Name: model.Text(fixtureUUID(100 + n)), Type: "postgresql",
			AccessMode: mode, CreatedAt: now, UpdatedAt: now,
		}
	}
	rows := map[string]model.CatalogConnection{
		"direct": conn(1, ptr("direct")), "trino": conn(2, ptr("trino")), "none": conn(3, nil),
	}
	for _, r := range rows {
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("create %s: %v", r.Name, err)
		}
	}

	for range 2 { // the second run proves it is idempotent
		if err := store.RenameTrinoAccessMode(db); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	want := map[string]*string{"direct": ptr("direct"), "trino": ptr("federated"), "none": nil}
	for key, r := range rows {
		var got model.CatalogConnection
		if err := db.Where("id = ?", r.ID).First(&got).Error; err != nil {
			t.Fatal(err)
		}
		switch {
		case want[key] == nil && got.AccessMode != nil:
			t.Errorf("%s: access_mode = %q, want NULL", key, *got.AccessMode)
		case want[key] != nil && (got.AccessMode == nil || *got.AccessMode != *want[key]):
			t.Errorf("%s: access_mode = %v, want %q", key, got.AccessMode, *want[key])
		}
	}

	// The expanded check accepts a legacy write from an older binary during a
	// rolling deployment. Only a rerun of the rename rewrites it; the API reports
	// such a row as federated meanwhile.
	legacy := conn(4, ptr("trino"))
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("legacy trino write was rejected: %v", err)
	}
	if err := store.RenameTrinoAccessMode(db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&legacy, "id = ?", legacy.ID).Error; err != nil || legacy.AccessMode == nil || *legacy.AccessMode != "federated" {
		t.Fatalf("legacy access mode = %v, err = %v", legacy.AccessMode, err)
	}
}
