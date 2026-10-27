package trail

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeStore returns n events per call, or a full page forever when endless.
type fakeStore struct {
	n       int
	endless bool
	calls   int
	last    db.ListAuditEventsParams
	// shared are the users the caller shares a workspace with.
	shared map[pgtype.UUID]bool
}

func (f *fakeStore) ListAuditEvents(ctx context.Context, p db.ListAuditEventsParams) ([]db.ListAuditEventsRow, error) {
	f.calls++
	f.last = p
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := f.n
	if f.endless {
		n = int(p.Lim)
	}
	rows := make([]db.ListAuditEventsRow, n)
	for i := range rows {
		rows[i] = db.ListAuditEventsRow{ID: int64(n - i), OccurredAt: pgtype.Timestamptz{Time: time.Unix(int64(1000-i), 0), Valid: true}}
	}
	return rows, nil
}

func (f *fakeStore) GetUserByID(_ context.Context, id pgtype.UUID) (db.User, error) {
	return db.User{ID: id, Email: "someone@x.io"}, nil
}

func (f *fakeStore) SharesWorkspace(_ context.Context, arg db.SharesWorkspaceParams) (bool, error) {
	return f.shared[arg.UserID], nil
}

func (f *fakeStore) GetCatalogByID(context.Context, pgtype.UUID) (db.Catalog, error) {
	return db.Catalog{}, nil
}

func TestPage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &fakeStore{n: 3}
	s := New(st)
	from := time.Now().Add(-time.Hour)
	two := 2
	user := pgtype.UUID{Bytes: uuid.New(), Valid: true}

	page, err := s.Page(ctx, Viewer{UserID: user}, Filter{From: &from}, &two, "")
	if err != nil || len(page.Events) != 2 || page.NextCursor == nil {
		t.Fatalf("Page = %+v, %v; want 2 events and a cursor", page, err)
	}
	if st.last.ScopeActor == nil || *st.last.ScopeActor != user.String() {
		t.Error("a non-admin is scoped to their own events")
	}
	if _, err := s.Page(ctx, Viewer{Admin: true}, Filter{From: &from}, &two, *page.NextCursor); err != nil || st.last.CursorID == nil || st.last.ScopeActor != nil {
		t.Errorf("next page = %v, cursor %v, scope %v; want the cursor applied and no scope for an admin", err, st.last.CursorID, st.last.ScopeActor)
	}

	var inv *InvalidError
	zero := 0
	if _, err := s.Page(ctx, Viewer{Admin: true}, Filter{From: &from}, &zero, ""); !errors.As(err, &inv) {
		t.Errorf("bad limit = %v", err)
	}
	if _, err := s.Page(ctx, Viewer{Admin: true}, Filter{From: &from}, nil, "%%"); !errors.As(err, &inv) {
		t.Errorf("bad cursor = %v", err)
	}
	var win *WindowError
	if _, err := s.Page(ctx, Viewer{Admin: true}, Filter{}, nil, ""); !errors.As(err, &win) {
		t.Errorf("no window = %v", err)
	}
	to := from.Add(MaxWindow + time.Hour)
	if _, err := s.Page(ctx, Viewer{Admin: true}, Filter{From: &from, To: &to}, nil, ""); !errors.As(err, &win) {
		t.Errorf("wide window = %v", err)
	}
	actor := "someone"
	if err := CheckWindow(Filter{ActorID: &actor}); err != nil {
		t.Errorf("an actor filter needs no window: %v", err)
	}
}

// TestExport_StopsWhenTheClientGoes: once the reader is closed or the
// request is cancelled, the export stops querying (#371).
func TestExport_StopsWhenTheClientGoes(t *testing.T) {
	t.Parallel()
	st := &fakeStore{endless: true}
	reader, writer := io.Pipe()
	_ = reader.Close()
	if err := New(st).Export(context.Background(), writer, Filter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("closed reader: err = %v, want io.ErrClosedPipe", err)
	}
	if st.calls != 1 {
		t.Fatalf("closed reader: %d pages queried, want 1", st.calls)
	}

	st = &fakeStore{endless: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := New(st).Export(ctx, io.Discard, Filter{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request: err = %v, want context.Canceled", err)
	}
	if st.calls != 0 {
		t.Fatalf("cancelled request: %d pages queried, want 0", st.calls)
	}
}

// oneEvent returns a single event carrying client-controlled text.
type oneEvent struct{ fakeStore }

func (oneEvent) ListAuditEvents(context.Context, db.ListAuditEventsParams) ([]db.ListAuditEventsRow, error) {
	ua, req := `=HYPERLINK("http://evil","x")`, "+1"
	return []db.ListAuditEventsRow{{ActorKind: "user", Action: "login", UserAgent: &ua, RequestID: &req}}, nil
}

// TestExport_NeutralizesFormulas: a value a spreadsheet would run as a
// formula is exported with a leading quote.
func TestExport_NeutralizesFormulas(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	if err := New(&oneEvent{}).Export(context.Background(), &out, Filter{}); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("csv = %q, %v", out.String(), err)
	}
	row := rows[1]
	if ua := row[len(row)-1]; ua != `'=HYPERLINK("http://evil","x")` {
		t.Errorf("user agent cell = %q; want a leading quote", ua)
	}
	if req := row[10]; req != "'+1" {
		t.Errorf("request id cell = %q; want a leading quote", req)
	}
	if row[1] != "user" || row[4] != "login" {
		t.Errorf("plain cells changed: %q", row)
	}
}

// TestUser_OnlyVisibleUsers: a non-admin resolves themselves and the users
// they share a workspace with, never anyone else's e-mail (#432).
func TestUser_OnlyVisibleUsers(t *testing.T) {
	ctx := context.Background()
	caller, peer, stranger := uid(1), uid(2), uid(3)
	s := New(&fakeStore{shared: map[pgtype.UUID]bool{peer: true}})
	for name, id := range map[string]pgtype.UUID{"themselves": caller, "a workspace peer": peer} {
		if u, err := s.User(ctx, caller, false, id); err != nil || u.ID != id {
			t.Errorf("%s = %+v, %v; want the user", name, u, err)
		}
	}
	if u, err := s.User(ctx, caller, false, stranger); !errors.Is(err, ErrUserHidden) || u.Email != "" {
		t.Errorf("stranger = %+v, %v; want ErrUserHidden and no e-mail", u, err)
	}
	if _, err := s.User(ctx, caller, true, stranger); err != nil {
		t.Errorf("admin = %v; want every user", err)
	}
}

func uid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }
