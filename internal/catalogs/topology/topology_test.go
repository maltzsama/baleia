package topology

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
)

type fakeStore struct {
	Store
	found   bool
	columns int
}

func (f *fakeStore) GetMetadataTableByName(context.Context, db.GetMetadataTableByNameParams) (db.UUID, error) {
	if !f.found {
		return db.UUID{}, db.ErrNoRows
	}
	return db.UUID{Valid: true}, nil
}

func (f *fakeStore) ListMetadataColumns(context.Context, db.UUID) ([]db.ListMetadataColumnsRow, error) {
	return make([]db.ListMetadataColumnsRow, f.columns), nil
}

// TestColumns: an unknown table returns the store's no-rows error, which
// callers treat as "no columns"; a known one its columns.
func TestColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &fakeStore{columns: 2}
	s := New(st)
	if _, err := s.Columns(ctx, db.GetMetadataTableByNameParams{}); !errors.Is(err, db.ErrNoRows) {
		t.Errorf("unknown table = %v, want db.ErrNoRows", err)
	}
	st.found = true
	if cols, err := s.Columns(ctx, db.GetMetadataTableByNameParams{}); err != nil || len(cols) != 2 {
		t.Errorf("Columns = %d, %v", len(cols), err)
	}
	var nilStore Store
	if New(nilStore).Available() || !s.Available() {
		t.Error("Available reports whether there is a store")
	}
}
