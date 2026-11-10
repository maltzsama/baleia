package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
)

func TestWebhookCRUD(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	store := New(gdb)
	ctx := context.Background()
	now := time.Now().UTC()

	user := createAPIKeyUser(t, gdb, "webhook@example.test")
	ws := model.Workspace{
		ID: model.UUID(NewUUID()), Name: "Webhooks", Type: "personal",
		PolarisCatalogName: model.Text("polaris_webhooks"), OwnerUserID: &user.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&ws).Error; err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	w, err := store.Create(ctx, model.Webhook{
		ID:          model.UUID(NewUUID()),
		WorkspaceID: ws.ID,
		URL:         "https://example.test/hook",
		SecretEnc:   "sealed",
		Events:      model.JSON(`["execution.finished"]`),
		Enabled:     true,
		CreatedBy:   user.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := store.List(ctx, string(ws.ID))
	if err != nil || len(list) != 1 || list[0].URL != w.URL {
		t.Fatalf("list = %+v, %v", list, err)
	}
	match, err := store.Match(ctx, string(ws.ID), "execution.finished")
	if err != nil || len(match) != 1 {
		t.Fatalf("match execution.finished = %+v, %v", match, err)
	}
	if other, _ := store.Match(ctx, string(ws.ID), "audit"); len(other) != 0 {
		t.Fatalf("audit matched a webhook that does not subscribe: %+v", other)
	}

	ok, err := store.Delete(ctx, string(ws.ID), string(w.ID))
	if err != nil || !ok {
		t.Fatalf("delete = %v, %v", ok, err)
	}
	if list, _ := store.List(ctx, string(ws.ID)); len(list) != 0 {
		t.Fatalf("webhook survived delete: %+v", list)
	}
}
