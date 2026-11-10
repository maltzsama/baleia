package events

import (
	"testing"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
)

func TestNotifyTxRollsBack(t *testing.T) {
	t.Parallel()
	gdb := testdb.New(t)
	outbox := NewOutbox(gdb)

	tx := gdb.Begin()
	if err := outbox.NotifyTx(tx, "execution", "abc"); err != nil {
		t.Fatalf("notify tx: %v", err)
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int64
	if err := gdb.Model(&model.MonitoringEvent{}).Where("channel = ?", "execution").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("a rolled-back write left %d notification rows", count)
	}
}
