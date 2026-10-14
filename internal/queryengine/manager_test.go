package queryengine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/fake"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type store struct {
	row *db.GetEngineWithConnectionRow
}

func (s *store) GetEngineWithConnection(context.Context, pgtype.UUID) (db.GetEngineWithConnectionRow, error) {
	if s.row == nil {
		return db.GetEngineWithConnectionRow{}, pgx.ErrNoRows
	}
	return *s.row, nil
}

var engineID = pgtype.UUID{Bytes: [16]byte{0xE1}, Valid: true}

func engineRow(kind string, updated time.Time, readOnlyCreds bool) *db.GetEngineWithConnectionRow {
	return &db.GetEngineWithConnectionRow{
		Engine: db.Engine{ID: engineID, Name: "e", Kind: kind, Class: "direct", IdentityMode: "connection", Enabled: true,
			UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}},
		CatalogConnection: db.CatalogConnection{ID: pgtype.UUID{Bytes: [16]byte{0xC1}, Valid: true}, Type: kind,
			Config: []byte(`{"url":"fake://"}`), CredentialsReadOnly: readOnlyCreds},
	}
}

func newManager(t *testing.T, s *store) (*queryengine.Manager, *connections.Dialer) {
	t.Helper()
	d, err := connections.NewDialer(connections.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return queryengine.NewManager(s, nil, nil, d), d
}

// unwrap reaches the fake behind the manager's wrapper.
func unwrap(t *testing.T, e queryengine.Engine) *fake.Engine {
	t.Helper()
	type inner interface{ Unwrap() queryengine.Engine }
	if w, ok := e.(inner); ok {
		e = w.Unwrap()
	}
	f, ok := e.(*fake.Engine)
	if !ok {
		t.Fatalf("engine is %T", e)
	}
	return f
}

func TestManager_UsesDialer(t *testing.T) {
	m, d := newManager(t, &store{row: engineRow(fake.Kind, time.Unix(1, 0), false)})
	e, err := m.Get(context.Background(), engineID)
	if err != nil {
		t.Fatal(err)
	}
	if unwrap(t, e).Dialer() != d {
		t.Fatal("the factory did not receive the shared dialer")
	}
}

func TestManager_RebuildsOnUpdate(t *testing.T) {
	ctx := context.Background()
	s := &store{row: engineRow(fake.Kind, time.Unix(1, 0), false)}
	m, _ := newManager(t, s)

	first, err := m.Get(ctx, engineID)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := m.Get(ctx, engineID)
	if unwrap(t, again) != unwrap(t, first) {
		t.Fatal("an unchanged row built a new instance")
	}

	sess, err := first.OpenSession(ctx, queryengine.SessionSpec{})
	if err != nil {
		t.Fatal(err)
	}
	s.row = engineRow(fake.Kind, time.Unix(2, 0), false)
	second, err := m.Get(ctx, engineID)
	if err != nil {
		t.Fatal(err)
	}
	if unwrap(t, second) == unwrap(t, first) {
		t.Fatal("a changed row reused the old instance")
	}
	if unwrap(t, first).Closed() {
		t.Fatal("the old instance closed while a session was open")
	}
	if _, err := first.OpenSession(ctx, queryengine.SessionSpec{}); queryengine.CodeOf(err) != queryengine.CodeUnavailable {
		t.Fatalf("new session on a replaced instance: %v", err)
	}
	sess.Close()
	if !unwrap(t, first).Closed() {
		t.Fatal("the old instance stayed open after its last session closed")
	}
	if unwrap(t, second).Closed() {
		t.Fatal("the new instance was closed")
	}
}

func TestReadOnlyUnsupportedRejected(t *testing.T) {
	ctx := context.Background()
	m, _ := newManager(t, &store{row: engineRow(readOnlyNoneKind, time.Unix(1, 0), false)})
	e, err := m.Get(ctx, engineID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.OpenSession(ctx, queryengine.SessionSpec{ReadOnly: true}); queryengine.CodeOf(err) != queryengine.CodeReadOnly {
		t.Fatalf("err = %v, want read_only_unsupported", err)
	}
	s, err := e.OpenSession(ctx, queryengine.SessionSpec{})
	if err != nil {
		t.Fatalf("a read-write session must still open: %v", err)
	}
	s.Close()
}

func TestReadOnlyUnsupportedAllowedWithAssertion(t *testing.T) {
	ctx := context.Background()
	m, _ := newManager(t, &store{row: engineRow(readOnlyNoneKind, time.Unix(1, 0), true)})
	e, err := m.Get(ctx, engineID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.OpenSession(ctx, queryengine.SessionSpec{ReadOnly: true})
	if err != nil {
		t.Fatalf("read-only credentials asserted: %v", err)
	}
	s.Close()
}

func TestManager_Refuses(t *testing.T) {
	ctx := context.Background()
	disabled := engineRow(fake.Kind, time.Unix(1, 0), false)
	disabled.Engine.Enabled = false
	cases := map[string]struct {
		row  *db.GetEngineWithConnectionRow
		code queryengine.ErrorCode
		err  error
	}{
		"missing engine":  {nil, queryengine.CodeNotFound, nil},
		"disabled engine": {disabled, queryengine.CodeUnavailable, nil},
		"unknown kind":    {engineRow("nope", time.Unix(1, 0), false), "", queryengine.ErrKindNotRegistered},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := newManager(t, &store{row: c.row})
			_, err := m.Get(ctx, engineID)
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Fatalf("err = %v, want %v", err, c.err)
				}
				return
			}
			if queryengine.CodeOf(err) != c.code {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
		})
	}
}

const readOnlyNoneKind = "fake-readonly-none"

func init() {
	queryengine.RegisterKind(readOnlyNoneKind, queryengine.ClassDirect, func(_ context.Context, row queryengine.EngineRow, _ connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
		return fake.New(fake.Options{ID: row.ID, Dialer: d, ReadOnly: queryengine.ReadOnlyNone}), nil
	})
}

type multiStore map[[16]byte]*db.GetEngineWithConnectionRow

func (s multiStore) GetEngineWithConnection(_ context.Context, id pgtype.UUID) (db.GetEngineWithConnectionRow, error) {
	r, ok := s[id.Bytes]
	if !ok {
		return db.GetEngineWithConnectionRow{}, pgx.ErrNoRows
	}
	return *r, nil
}

// TestManager_SeveralInstancesAndKinds: two engines of one kind and one of
// another run side by side through the same manager.
func TestManager_SeveralInstancesAndKinds(t *testing.T) {
	ctx := context.Background()
	s := multiStore{}
	ids := []pgtype.UUID{}
	for i, kind := range []string{fake.Kind, fake.Kind, readOnlyNoneKind} {
		id := pgtype.UUID{Bytes: [16]byte{0xE0, byte(i)}, Valid: true}
		r := engineRow(kind, time.Unix(1, 0), false)
		r.Engine.ID = id
		s[id.Bytes] = r
		ids = append(ids, id)
	}
	d, _ := connections.NewDialer(connections.DialerOptions{})
	m := queryengine.NewManager(s, nil, nil, d)
	seen := map[*fake.Engine]bool{}
	for _, id := range ids {
		e, err := m.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		tbl, err := queryengine.Collect(ctx, e, queryengine.SessionSpec{}, "SELECT COUNT", 10)
		if err != nil {
			t.Fatal(err)
		}
		tbl.Release()
		seen[unwrap(t, e)] = true
	}
	if len(seen) != 3 {
		t.Fatalf("%d distinct instances, want 3", len(seen))
	}
}
