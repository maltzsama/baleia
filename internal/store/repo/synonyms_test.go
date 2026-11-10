package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

func TestSearchSynonyms(t *testing.T) {
	t.Parallel()
	f := semanticFixture(t)
	r := NewSearchSynonyms(f.gdb)
	ctx := context.Background()
	entry := func(term string, synonyms ...string) model.SearchSynonym {
		return model.SearchSynonym{
			ID: model.UUID(NewUUID()), WorkspaceID: f.ws, Term: model.Text(term),
			Synonyms: model.StringArray(synonyms), CreatedBy: f.owner, CreatedAt: time.Now().UTC(),
		}
	}
	first, err := r.Create(ctx, entry("receita", "faturamento", "revenue"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create(ctx, entry("churn", "cancelamento")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create(ctx, entry("receita", "x1")); !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("a second entry for a term: %v, want gorm.ErrDuplicatedKey", err)
	}

	rows, err := r.List(ctx, string(f.ws))
	if err != nil || len(rows) != 2 {
		t.Fatalf("List = %v, %v", rows, err)
	}
	if len(rows[0].Synonyms) == 0 {
		t.Errorf("synonyms were not stored: %+v", rows[0])
	}
	if other, err := r.List(ctx, string(NewUUID())); err != nil || len(other) != 0 {
		t.Fatalf("another workspace's glossary = %v, %v", other, err)
	}

	if removed, err := r.Delete(ctx, string(NewUUID()), string(first.ID)); err != nil || removed {
		t.Errorf("deleting through another workspace = %v, %v", removed, err)
	}
	if removed, err := r.Delete(ctx, string(f.ws), string(first.ID)); err != nil || !removed {
		t.Errorf("Delete = %v, %v", removed, err)
	}
	if removed, _ := r.Delete(ctx, string(f.ws), string(first.ID)); removed {
		t.Error("deleting twice removes nothing")
	}
}
