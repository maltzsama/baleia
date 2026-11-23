package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/openlineage"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
)

func TestOpenLineageDestinationPreservesRotatedToken(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	user := createAPIKeyUser(t, gdb, "openlineage@example.test")
	ws := model.Workspace{ID: model.UUID(NewUUID()), Name: "Lineage", Type: "personal", PolarisCatalogName: model.Text("lineage"), OwnerUserID: &user.ID}
	if err := gdb.Create(&ws).Error; err != nil {
		t.Fatal(err)
	}
	store := NewOpenLineageDestinations(gdb)
	ctx := context.Background()
	row := model.OpenLineageDestination{WorkspaceID: ws.ID, Endpoint: "https://example.test/events", Namespace: "test"}
	created, err := store.Upsert(ctx, row, false)
	if err != nil || created.TokenEnc != "" {
		t.Fatalf("create without token: %+v, %v", created, err)
	}
	row.TokenEnc = "old"
	if _, err := store.Upsert(ctx, row, true); err != nil {
		t.Fatal(err)
	}
	stale, err := store.Get(ctx, string(ws.ID))
	if err != nil {
		t.Fatal(err)
	}
	row.TokenEnc = "rotated"
	if _, err := store.Upsert(ctx, row, true); err != nil {
		t.Fatal(err)
	}
	stale.Namespace = "changed"
	got, err := store.Upsert(ctx, stale, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenEnc != "rotated" || got.Namespace != "changed" {
		t.Fatalf("got %+v", got)
	}
	stale.Endpoint = "http://example.test/events"
	if _, err := store.Upsert(ctx, stale, false); !errors.Is(err, openlineage.ErrInsecureBearer) {
		t.Fatalf("insecure update: %v", err)
	}
	got, err = store.Get(ctx, string(ws.ID))
	if err != nil || got.Endpoint != "https://example.test/events" || got.TokenEnc != "rotated" {
		t.Fatalf("rollback: %+v, %v", got, err)
	}
	got.TokenEnc = ""
	got, err = store.Upsert(ctx, got, true)
	if err != nil || got.TokenEnc != "" {
		t.Fatalf("clear: %+v, %v", got, err)
	}
}
