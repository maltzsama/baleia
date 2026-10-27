package history

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

// fakeStore has live entities by id and the latest version of each history.
type fakeStore struct {
	queries map[[16]byte]db.SavedQuery
	latest  map[[16]byte]db.ContentVersion
	down    bool
}

func (f *fakeStore) GetSavedQueryByID(_ context.Context, arg db.GetSavedQueryByIDParams) (db.SavedQuery, error) {
	if f.down {
		return db.SavedQuery{}, errors.New("connection refused")
	}
	if q, ok := f.queries[arg.ID.Bytes]; ok {
		return q, nil
	}
	return db.SavedQuery{}, pgx.ErrNoRows
}

func (f *fakeStore) GetSemanticMetric(context.Context, db.GetSemanticMetricParams) (db.SemanticMetric, error) {
	return db.SemanticMetric{}, pgx.ErrNoRows
}

func (f *fakeStore) GetSemanticModel(context.Context, db.GetSemanticModelParams) (db.SemanticModel, error) {
	return db.SemanticModel{}, pgx.ErrNoRows
}

func (f *fakeStore) LatestContentVersion(_ context.Context, arg db.LatestContentVersionParams) (db.ContentVersion, error) {
	if v, ok := f.latest[arg.EntityID.Bytes]; ok {
		return v, nil
	}
	return db.ContentVersion{}, pgx.ErrNoRows
}

func (f *fakeStore) ListContentVersions(context.Context, db.ListContentVersionsParams) ([]db.ListContentVersionsRow, error) {
	return nil, nil
}

func (f *fakeStore) GetContentVersion(context.Context, db.GetContentVersionParams) (db.GetContentVersionRow, error) {
	return db.GetContentVersionRow{}, pgx.ErrNoRows
}

func (f *fakeStore) ListDeletedContent(context.Context, db.ListDeletedContentParams) ([]db.ListDeletedContentRow, error) {
	return nil, nil
}

func TestSpaceOf(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	live, deleted, gone := newID(), newID(), newID()
	liveSpace, oldSpace := newID(), newID()
	st := &fakeStore{
		queries: map[[16]byte]db.SavedQuery{live.Bytes: {ID: live, SpaceID: liveSpace}},
		latest:  map[[16]byte]db.ContentVersion{deleted.Bytes: {SpaceID: oldSpace, Version: 3}},
	}
	s := New(st)

	if got, err := s.SpaceOf(ctx, newID(), content.EntitySavedQuery, live); err != nil || got != liveSpace {
		t.Errorf("live entity = %v, %v; want its own space", got, err)
	}
	if got, err := s.SpaceOf(ctx, newID(), content.EntitySavedQuery, deleted); err != nil || got != oldSpace {
		t.Errorf("deleted entity = %v, %v; want its last version's space", got, err)
	}
	if _, err := s.SpaceOf(ctx, newID(), content.EntitySavedQuery, gone); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown entity = %v, want ErrNotFound", err)
	}
	if v := s.Latest(ctx, newID(), content.EntitySavedQuery, deleted); v != 3 {
		t.Errorf("Latest = %d, want 3", v)
	}
	if v := s.Latest(ctx, newID(), content.EntitySavedQuery, gone); v != 0 {
		t.Errorf("Latest without versions = %d, want 0", v)
	}

	st.down = true
	if _, err := s.SpaceOf(ctx, newID(), content.EntitySavedQuery, live); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("store failure = %v, want the store error", err)
	}
	if _, err := s.Get(ctx, newID(), content.EntitySavedQuery, live, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing version = %v, want ErrNotFound", err)
	}
}
