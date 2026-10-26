package topology

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeStore struct {
	Store
	found   bool
	columns int
}

func (f *fakeStore) GetMetadataTableByName(context.Context, db.GetMetadataTableByNameParams) (pgtype.UUID, error) {
	if !f.found {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	return pgtype.UUID{Valid: true}, nil
}

func (f *fakeStore) ListMetadataColumns(context.Context, pgtype.UUID) ([]db.ListMetadataColumnsRow, error) {
	return make([]db.ListMetadataColumnsRow, f.columns), nil
}

// TestColumns: an unknown table returns the store's no-rows error, which
// callers treat as "no columns"; a known one its columns.
func TestColumns(t *testing.T) {
	ctx := context.Background()
	st := &fakeStore{columns: 2}
	s := New(st)
	if _, err := s.Columns(ctx, db.GetMetadataTableByNameParams{}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown table = %v, want pgx.ErrNoRows", err)
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
