package repo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
)

// TestLongFreeTextIsStored: a query's SQL and a semantic description are free
// text the application accepts at length (a description up to 20000 characters),
// so no backend may cap them. On MySQL and MariaDB they were varchar(128), and
// anything longer failed with "Data too long".
func TestLongFreeTextIsStored(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	ctx := context.Background()

	for _, n := range []int{200, 3000, 20000} {
		sql := "select " + strings.Repeat("x", n)
		exec := model.QueryExecution{
			ID: model.UUID(NewUUID()), WorkspaceID: f.ws, UserID: f.owner,
			Status: "finished", Origin: "editor", SQL: sql, StartedAt: time.Now().UTC(),
		}
		if err := f.gdb.Create(&exec).Error; err != nil {
			t.Fatalf("a %d character SQL in the history: %v", len(sql), err)
		}
		var back model.QueryExecution
		if err := f.gdb.First(&back, "id = ?", string(exec.ID)).Error; err != nil || back.SQL != sql {
			t.Fatalf("history SQL of %d characters came back as %d characters (%v)", len(sql), len(back.SQL), err)
		}
	}

	description := strings.Repeat("d", 20000) // the documented maximum
	if _, err := f.r.UpsertSemanticDoc(ctx, db.UpsertSemanticDocParams{
		WorkspaceID: f.wsPG(), CatalogID: f.catalogPG(), Namespace: []string{"sales"}, TableName: "long_docs",
		Description: description, UpdatedBy: f.ownerPG(),
	}); err != nil {
		t.Fatalf("a %d character description: %v", len(description), err)
	}
	var doc model.SemanticDoc
	if err := f.gdb.First(&doc, "table_name = ?", "long_docs").Error; err != nil || doc.Description != description {
		t.Fatalf("description came back as %d characters (%v)", len(doc.Description), err)
	}
}
