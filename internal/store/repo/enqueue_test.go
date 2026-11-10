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

func TestExecutionEnqueueIsTransactional(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	store := New(gdb)
	ctx := context.Background()
	now := time.Now().UTC()

	user := createAPIKeyUser(t, gdb, "enqueue@example.test")
	ws := model.Workspace{
		ID: model.UUID(NewUUID()), Name: "Enqueue", Type: "personal",
		PolarisCatalogName: model.Text("polaris_enqueue"), OwnerUserID: &user.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&ws).Error; err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	exec, err := store.CreateQueryExecution(ctx, db.CreateQueryExecutionParams{
		WorkspaceID: PGUUID(string(ws.ID)),
		UserID:      PGUUID(string(user.ID)),
		Status:      "running",
		Sql:         "SELECT 1",
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}

	var events []string
	store.SetEnqueue(func(tx *gorm.DB, channel, payload string) error {
		events = append(events, channel+":"+payload)
		return tx.Create(&model.MonitoringEvent{Channel: model.Text(channel), Payload: payload}).Error
	})
	if _, err := store.UpdateQueryExecution(ctx, db.UpdateQueryExecutionParams{
		ID: exec.ID, Status: "finished", FinishedAt: PGTime(now),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	want := "execution:" + UUIDString(exec.ID)
	if len(events) != 1 || events[0] != want {
		t.Fatalf("enqueued %v, want [%s]", events, want)
	}
	var rows []model.MonitoringEvent
	if err := gdb.Where("channel = ?", "execution").Find(&rows).Error; err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if len(rows) != 1 || rows[0].Payload != UUIDString(exec.ID) {
		t.Fatalf("outbox rows = %+v", rows)
	}

	// A no-op update (the row is already terminal) must not enqueue.
	if _, err := store.UpdateQueryExecution(ctx, db.UpdateQueryExecutionParams{
		ID: exec.ID, Status: "failed", FinishedAt: PGTime(now),
	}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("second update err = %v, want ErrNoRows", err)
	}
	if len(events) != 1 {
		t.Fatalf("a no-op enqueued: %v", events)
	}
}
