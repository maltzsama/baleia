package signals

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeStore records which engine list was read.
type fakeStore struct {
	Store
	all bool
}

func (f *fakeStore) ListEngines(context.Context) ([]db.Engine, error) {
	f.all = true
	return nil, nil
}

func (f *fakeStore) ListEnginesVisibleToUser(context.Context, pgtype.UUID) ([]db.Engine, error) {
	f.all = false
	return nil, nil
}

func TestAvailableAndEngines(t *testing.T) {
	t.Parallel()
	var nilQuerier db.Querier
	if New(nilQuerier).Available() {
		t.Error("a service without a database is unavailable")
	}
	st := &fakeStore{}
	s := New(st)
	if !s.Available() {
		t.Fatal("a service with a store is available")
	}
	_, _ = s.Engines(context.Background(), pgtype.UUID{}, true)
	if !st.all {
		t.Error("an admin reads every engine")
	}
	_, _ = s.Engines(context.Background(), pgtype.UUID{}, false)
	if st.all {
		t.Error("a member reads the engines visible to them")
	}
}
