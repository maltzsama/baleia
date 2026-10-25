package docs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestNormalizeTags(t *testing.T) {
	tags := []string{"Sales", " sales ", "revenue-2026"}
	got, err := NormalizeTags(&tags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"sales", "revenue-2026"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("NormalizeTags = %v, want %v", got, want)
	}
	var inv *InvalidTagsError
	bad := []string{"Sales!"}
	if _, err := NormalizeTags(&bad); !errors.As(err, &inv) {
		t.Fatal("invalid tag was accepted")
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = "tag" + string(rune('a'+i%26))
	}
	if _, err := NormalizeTags(&many); !errors.As(err, &inv) {
		t.Fatal("21 tags were accepted")
	}
	if got, err := NormalizeTags(nil); err != nil || got == nil || len(got) != 0 {
		t.Errorf("nil tags = %v, %v; want an empty list", got, err)
	}
}

// fakeStore keeps one doc per table and column.
type fakeStore struct{ docs map[string]db.SemanticDoc }

func key(table string, column *string) string {
	if column == nil {
		return table
	}
	return table + "." + *column
}

func (f *fakeStore) GetSemanticDoc(_ context.Context, arg db.GetSemanticDocParams) (db.SemanticDoc, error) {
	if d, ok := f.docs[key(arg.TableName, arg.ColumnName)]; ok {
		return d, nil
	}
	return db.SemanticDoc{}, pgx.ErrNoRows
}

func (f *fakeStore) ListSemanticDocsForTable(context.Context, db.ListSemanticDocsForTableParams) ([]db.SemanticDoc, error) {
	return nil, nil
}

func (f *fakeStore) SearchSemanticDocs(context.Context, db.SearchSemanticDocsParams) ([]db.SearchSemanticDocsRow, error) {
	return nil, nil
}

func (f *fakeStore) ListSemanticTags(context.Context, db.ListSemanticTagsParams) ([]db.ListSemanticTagsRow, error) {
	return nil, nil
}

func (f *fakeStore) UpsertSemanticDoc(_ context.Context, arg db.UpsertSemanticDocParams) (db.SemanticDoc, error) {
	d := db.SemanticDoc{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TableName: arg.TableName, ColumnName: arg.ColumnName, Description: arg.Description, Tags: arg.Tags, Certified: arg.Certified, UpdatedBy: arg.UpdatedBy, UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}
	f.docs[key(arg.TableName, arg.ColumnName)] = d
	return d, nil
}

func (f *fakeStore) DeleteSemanticDoc(_ context.Context, arg db.DeleteSemanticDocParams) (int64, error) {
	k := key(arg.TableName, arg.ColumnName)
	if _, ok := f.docs[k]; !ok {
		return 0, nil
	}
	delete(f.docs, k)
	return 1, nil
}

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func TestUpsert_Certification(t *testing.T) {
	ctx := context.Background()
	s := New(&fakeStore{docs: map[string]db.SemanticDoc{}})
	k := Key{WorkspaceID: newID(), CatalogID: newID(), Table: "orders"}
	yes := true

	if _, _, err := s.Upsert(ctx, k, Edit{Description: "a", Certified: &yes}, newID(), false); !errors.Is(err, ErrCertifyForbidden) {
		t.Fatalf("certify without the right = %v, want ErrCertifyForbidden", err)
	}
	d, changed, err := s.Upsert(ctx, k, Edit{Description: "a", Certified: &yes}, newID(), true)
	if err != nil || !d.Certified || !changed {
		t.Fatalf("certify = %+v, %v, %v", d, changed, err)
	}
	// An editor who may not certify keeps the certification while the
	// description is unchanged, and loses it when they change it.
	if d, changed, _ = s.Upsert(ctx, k, Edit{Description: "a"}, newID(), false); !d.Certified || changed {
		t.Errorf("same description = certified %v, changed %v", d.Certified, changed)
	}
	if d, changed, _ = s.Upsert(ctx, k, Edit{Description: "b"}, newID(), false); d.Certified || !changed {
		t.Errorf("new description = certified %v, changed %v; want uncertified", d.Certified, changed)
	}
}

func TestUpsert_Validation(t *testing.T) {
	ctx := context.Background()
	s := New(&fakeStore{docs: map[string]db.SemanticDoc{}})
	k := Key{WorkspaceID: newID(), CatalogID: newID(), Table: "orders"}

	if _, _, err := s.Upsert(ctx, k, Edit{Description: strings.Repeat("x", MaxDescription+1)}, newID(), true); !errors.Is(err, ErrDescriptionTooLong) {
		t.Errorf("long description = %v", err)
	}
	bad := []string{"Bad Tag"}
	var inv *InvalidTagsError
	if _, _, err := s.Upsert(ctx, k, Edit{Description: "a", Tags: &bad}, newID(), true); !errors.As(err, &inv) {
		t.Errorf("bad tag = %v", err)
	}

	d, _, _ := s.Upsert(ctx, k, Edit{Description: "a"}, newID(), true)
	stale := d.UpdatedAt.Time.Add(-time.Minute)
	var conflict *ConflictError
	if _, _, err := s.Upsert(ctx, k, Edit{Description: "b", ExpectedUpdatedAt: &stale}, newID(), true); !errors.As(err, &conflict) || conflict.UpdatedBy != d.UpdatedBy {
		t.Errorf("stale write = %v, want a ConflictError naming the last editor", err)
	}
}

func TestColumnKeyAndDelete(t *testing.T) {
	ctx := context.Background()
	s := New(&fakeStore{docs: map[string]db.SemanticDoc{}})
	blank, col := " ", "amount"
	table := Key{WorkspaceID: newID(), CatalogID: newID(), Table: "orders", Column: &blank}
	if _, _, err := s.Upsert(ctx, table, Edit{Description: "t"}, newID(), true); err != nil {
		t.Fatal(err)
	}
	if d, err := s.Get(ctx, Key{Table: "orders"}); err != nil || d.Description != "t" {
		t.Fatalf("a blank column names the table: %+v, %v", d, err)
	}
	if _, err := s.Get(ctx, Key{Table: "orders", Column: &col}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing column doc = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, table); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, table); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}
