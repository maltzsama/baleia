package datapolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

// memStore is an in-memory PublishStore.
type memStore struct {
	mu        sync.Mutex
	policies  map[uuid.UUID][]Policy
	revisions map[uuid.UUID]Revision
	pointer   uuid.UUID
	subjects  []SubjectSource
	// raceOnce publishes another revision before the next publish lands.
	raceOnce func()
}

func newMemStore() *memStore {
	return &memStore{policies: map[uuid.UUID][]Policy{}, revisions: map[uuid.UUID]Revision{}}
}

func (m *memStore) ListByCatalog(_ context.Context, id uuid.UUID) ([]Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policies[id], nil
}

func (m *memStore) PublishedRevision(context.Context) (Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pointer == uuid.Nil {
		return Revision{}, ErrNotFound
	}
	return m.revisions[m.pointer], nil
}

func (m *memStore) PublishRevision(_ context.Context, rev Revision, base uuid.UUID) (Revision, error) {
	if race := m.raceOnce; race != nil {
		m.raceOnce = nil
		race()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pointer != base {
		return Revision{}, ErrVersionConflict
	}
	rev.Number = int64(len(m.revisions) + 1)
	m.revisions[rev.ID] = rev
	m.pointer = rev.ID
	return rev, nil
}

func (m *memStore) GetRevision(_ context.Context, id uuid.UUID) (Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rev, ok := m.revisions[id]
	if !ok {
		return Revision{}, ErrNotFound
	}
	return rev, nil
}

func (m *memStore) ListSubjectSources(context.Context) ([]SubjectSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.subjects, nil
}

type memObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	fail    error
}

func (o *memObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail != nil {
		return o.fail
	}
	if o.objects == nil {
		o.objects = map[string][]byte{}
	}
	o.objects[key] = data
	return nil
}

func (o *memObjects) GetReader(_ context.Context, key string) (io.ReadCloser, string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	data, ok := o.objects[key]
	if !ok {
		return nil, "", errors.New("no such object")
	}
	return io.NopCloser(bytes.NewReader(data)), bundleContentType, nil
}

func newPublisher(t *testing.T, store *memStore, objects *memObjects, now *time.Time) *Publisher {
	t.Helper()
	return &Publisher{
		Store: store, Objects: objects, Key: testKey(t), Lease: 24 * time.Hour, IdentityMode: "uuid",
		Trusted: map[string]bool{"baleia_admin": true},
		Now:     func() time.Time { return *now },
	}
}

func contentOf(t *testing.T, rev Revision) Content {
	t.Helper()
	var c Content
	if err := json.Unmarshal(rev.Content, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPublisher_PublishCatalog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, objects := newMemStore(), &memObjects{}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	p := newPublisher(t, store, objects, &now)
	sales, hr := uuid.New(), uuid.New()
	store.policies[sales] = publishedPolicies(sales)
	store.policies[hr] = publishedPolicies(hr)
	ana := db.UUID{Bytes: uuid.New(), Valid: true}
	store.subjects = []SubjectSource{{UserID: ana, Email: "ana@x.io"}}
	by := uuid.New()

	first, err := p.PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, by)
	if err != nil {
		t.Fatalf("PublishCatalog: %v", err)
	}
	if first.Reason != ReasonPublish || !first.NotAfter.Equal(now.Add(24*time.Hour)) || first.CreatedBy == nil || *first.CreatedBy != by {
		t.Fatalf("first revision = %+v", first)
	}
	if _, ok := objects.objects[BundleKey(first.ID)]; !ok {
		t.Fatal("the bundle was not stored under the revision's key")
	}
	c := contentOf(t, first)
	if _, ok := c.Catalogs[sales.String()]; !ok || len(c.Subjects) != 1 {
		t.Fatalf("content = %+v", c)
	}

	// Publishing another catalog keeps the first as published.
	second, err := p.PublishCatalog(ctx, hr, []string{"hr"}, ViewInspection{}, by)
	if err != nil {
		t.Fatal(err)
	}
	if c := contentOf(t, second); len(c.Catalogs) != 2 {
		t.Fatalf("catalogs after the second publish = %v", c.Catalogs)
	}

	// A catalog whose policies are all gone leaves the bundle.
	store.policies[hr] = nil
	third, err := p.PublishCatalog(ctx, hr, []string{"hr"}, ViewInspection{}, by)
	if err != nil {
		t.Fatal(err)
	}
	if c := contentOf(t, third); len(c.Catalogs) != 1 {
		t.Fatalf("catalogs after emptying hr = %v", c.Catalogs)
	}
}

func TestPublisher_Refusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now()
	sales := uuid.New()
	refused := func(t *testing.T, err error, code string) {
		t.Helper()
		var r *RefusedError
		if !errors.As(err, &r) {
			t.Fatalf("err = %v, want a refusal", err)
		}
		for _, problem := range r.Problems {
			if problem.Code == code {
				return
			}
		}
		t.Fatalf("problems = %+v, want %s", r.Problems, code)
	}

	t.Run("invalid policy", func(t *testing.T) {
		t.Parallel()
		store := newMemStore()
		policies := publishedPolicies(sales)
		policies[0].Validation = ValidationInvalid
		store.policies[sales] = policies
		_, err := newPublisher(t, store, &memObjects{}, &now).PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, uuid.New())
		refused(t, err, ProblemInvalid)
	})

	t.Run("a name another catalog has", func(t *testing.T) {
		t.Parallel()
		store := newMemStore()
		other := uuid.New()
		store.policies[sales], store.policies[other] = publishedPolicies(sales), publishedPolicies(other)
		p := newPublisher(t, store, &memObjects{}, &now)
		if _, err := p.PublishCatalog(ctx, other, []string{"lake"}, ViewInspection{}, uuid.New()); err != nil {
			t.Fatal(err)
		}
		_, err := p.PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, uuid.New())
		refused(t, err, ProblemNameConflict)
	})

	t.Run("views that cannot be read", func(t *testing.T) {
		t.Parallel()
		store := newMemStore()
		store.policies[sales] = publishedPolicies(sales)
		_, err := newPublisher(t, store, &memObjects{}, &now).PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{Uninspectable: []string{"hive"}}, uuid.New())
		refused(t, err, ProblemViewsUninspected)
	})

	t.Run("a trusted-owned DEFINER view", func(t *testing.T) {
		t.Parallel()
		store := newMemStore()
		store.policies[sales] = publishedPolicies(sales)
		views := ViewInspection{Views: []CatalogView{{
			Catalogs: []string{"lake"},
			View:     connections.View{Namespace: connections.Namespace{"sales"}, Name: "v", Owner: "baleia_admin", SQL: "SELECT * FROM orders"},
			Reads:    []TableName{NewTableName("lake", "sales", "orders")},
		}}}
		_, err := newPublisher(t, store, &memObjects{}, &now).PublishCatalog(ctx, sales, []string{"lake"}, views, uuid.New())
		refused(t, err, ProblemTrustedDefiner)
	})

	t.Run("object storage down publishes nothing", func(t *testing.T) {
		t.Parallel()
		store := newMemStore()
		store.policies[sales] = publishedPolicies(sales)
		_, err := newPublisher(t, store, &memObjects{fail: errors.New("down")}, &now).PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, uuid.New())
		if err == nil || store.pointer != uuid.Nil {
			t.Fatalf("err = %v, pointer = %v; want an error and nothing published", err, store.pointer)
		}
	})
}

// TestPublisher_ConcurrentPublishKeepsBoth: a publish that loses the race for
// the pointer starts over from the revision that won, keeping its change.
func TestPublisher_ConcurrentPublishKeepsBoth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now()
	store := newMemStore()
	sales, hr := uuid.New(), uuid.New()
	store.policies[sales], store.policies[hr] = publishedPolicies(sales), publishedPolicies(hr)
	p := newPublisher(t, store, &memObjects{}, &now)
	store.raceOnce = func() {
		if _, err := p.PublishCatalog(ctx, hr, []string{"hr"}, ViewInspection{}, uuid.New()); err != nil {
			t.Errorf("racing publish: %v", err)
		}
	}
	rev, err := p.PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if c := contentOf(t, rev); len(c.Catalogs) != 2 {
		t.Fatalf("catalogs = %v, want both publishes", c.Catalogs)
	}
}

// TestPublisher_RetryReadsTheCatalogAgain: a publish that loses the race to
// a publish of the same catalog writes the catalog as it is now, never the
// snapshot it read before the race.
func TestPublisher_RetryReadsTheCatalogAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now()
	store := newMemStore()
	sales := uuid.New()
	store.policies[sales] = publishedPolicies(sales)
	p := newPublisher(t, store, &memObjects{}, &now)
	store.raceOnce = func() {
		store.policies[sales][0].Compiled = "region = 'US'"
		if _, err := p.PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, uuid.New()); err != nil {
			t.Errorf("racing publish: %v", err)
		}
	}
	rev, err := p.PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	rows := contentOf(t, rev).Catalogs[sales.String()].Tables["sales"]["orders"].RowPolicies
	if len(rows) != 1 || rows[0].Using != "region = 'US'" {
		t.Fatalf("row policies = %+v, want the edit the racing publish carried", rows)
	}
}

func TestPublisher_RollbackRetryResolvesNamesAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now()
	store := newMemStore()
	sales := uuid.New()
	store.policies[sales] = publishedPolicies(sales)
	p := newPublisher(t, store, &memObjects{}, &now)
	first, err := p.PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	name := "lake"
	names := func(context.Context, uuid.UUID) ([]string, error) { return []string{name}, nil }
	store.raceOnce = func() {
		name = "lake_v2"
		if _, err := p.PublishCatalog(ctx, sales, []string{name}, ViewInspection{}, uuid.New()); err != nil {
			t.Errorf("racing publish: %v", err)
		}
	}
	back, err := p.Rollback(ctx, first.ID, ViewInspection{}, uuid.New(), names)
	if err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, back).Catalogs[sales.String()].Names; !slices.Equal(got, []string{"lake_v2"}) {
		t.Fatalf("restored names = %v, want the name the binding took during the retry", got)
	}
}

func TestPublisher_ReissueAndRollback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, objects := newMemStore(), &memObjects{}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	p := newPublisher(t, store, objects, &now)

	if _, changed, err := p.Reissue(ctx); err != nil || changed {
		t.Fatalf("Reissue with nothing published = %v, %v", changed, err)
	}
	sales := uuid.New()
	store.policies[sales] = publishedPolicies(sales)
	first, err := p.PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, uuid.New())
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(11 * time.Hour)
	if _, changed, err := p.Reissue(ctx); err != nil || changed {
		t.Fatalf("Reissue with most of the lease left = %v, %v", changed, err)
	}

	store.subjects = []SubjectSource{{UserID: db.UUID{Bytes: uuid.New(), Valid: true}, Email: "new@x.io"}}
	bySubjects, changed, err := p.Reissue(ctx)
	if err != nil || !changed || bySubjects.Reason != ReasonSubjects || *bySubjects.SourceID != first.ID {
		t.Fatalf("Reissue after a subject change = %+v, %v, %v", bySubjects, changed, err)
	}

	now = now.Add(13 * time.Hour)
	byLease, changed, err := p.Reissue(ctx)
	if err != nil || !changed || byLease.Reason != ReasonReissue || !byLease.NotAfter.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("Reissue past half the lease = %+v, %v, %v", byLease, changed, err)
	}

	// Rollback re-issues the first revision's policies after they changed.
	store.policies[sales] = nil
	emptied, err := p.PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, uuid.New())
	if err != nil || len(contentOf(t, emptied).Catalogs) != 0 {
		t.Fatalf("emptying = %v", err)
	}
	renamed := func(context.Context, uuid.UUID) ([]string, error) { return []string{"lake_v2", "lake_v2"}, nil }
	back, err := p.Rollback(ctx, first.ID, ViewInspection{}, uuid.New(), renamed)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	c := contentOf(t, back)
	if back.Reason != ReasonRollback || *back.SourceID != first.ID || len(c.Catalogs) != 1 || len(c.Subjects) != 1 {
		t.Fatalf("rollback = %+v, content %+v", back, c)
	}
	if got := c.Catalogs[sales.String()].Names; !slices.Equal(got, []string{"lake_v2"}) {
		t.Fatalf("restored names = %v, want the catalog's names today", got)
	}
	if _, err := p.Rollback(ctx, uuid.New(), ViewInspection{}, uuid.New(), renamed); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rollback to an unknown revision: %v", err)
	}

	// A catalog that cannot carry policies today refuses the rollback; one
	// that is gone leaves the bundle.
	unenforced := errors.New("unenforced binding")
	refusing := func(context.Context, uuid.UUID) ([]string, error) { return nil, unenforced }
	if _, err := p.Rollback(ctx, first.ID, ViewInspection{}, uuid.New(), refusing); !errors.Is(err, unenforced) {
		t.Fatalf("Rollback over an unenforced binding: %v", err)
	}
	gone := func(context.Context, uuid.UUID) ([]string, error) { return nil, nil }
	dropped, err := p.Rollback(ctx, first.ID, ViewInspection{}, uuid.New(), gone)
	if err != nil || len(contentOf(t, dropped).Catalogs) != 0 {
		t.Fatalf("Rollback of a deleted catalog = %v, %v", contentOf(t, dropped).Catalogs, err)
	}
}

func TestPublisher_CatalogChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now()
	store := newMemStore()
	sales := uuid.New()
	store.policies[sales] = publishedPolicies(sales)
	p := newPublisher(t, store, &memObjects{}, &now)
	if changed, err := p.CatalogChanged(ctx, sales, []string{"lake"}); err != nil || !changed {
		t.Fatalf("before publishing = %v, %v", changed, err)
	}
	if _, err := p.PublishCatalog(ctx, sales, []string{"lake"}, ViewInspection{}, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if changed, err := p.CatalogChanged(ctx, sales, []string{"lake"}); err != nil || changed {
		t.Fatalf("after publishing = %v, %v", changed, err)
	}
	if changed, err := p.CatalogChanged(ctx, sales, []string{"lake_v2"}); err != nil || !changed {
		t.Fatalf("after a binding rename = %v, %v", changed, err)
	}
	store.policies[sales][0].Compiled = "region = 'US'"
	if changed, err := p.CatalogChanged(ctx, sales, []string{"lake"}); err != nil || !changed {
		t.Fatalf("after an edit = %v, %v", changed, err)
	}
}
