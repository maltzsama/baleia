// Package trail reads the audit trail (#43): pages of events with their
// filters and cursor, the CSV export, and the users and catalogs the
// resolver names. The caller checks who may read what; Page scopes a
// non-admin to their own events.
package trail

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store reads events and the entities they name; *db.Queries implements it.
type Store interface {
	ListAuditEvents(ctx context.Context, arg db.ListAuditEventsParams) ([]db.ListAuditEventsRow, error)
	GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error)
	GetCatalogByID(ctx context.Context, id pgtype.UUID) (db.Catalog, error)
}

// Limits on pages, windows and exports.
const (
	DefaultPage = 50
	MaxPage     = 200
	// MaxWindow is the widest time window.
	MaxWindow = 93 * 24 * time.Hour
	// ExportPage is the page size of an export, MaxExportRows its bound.
	ExportPage    = 1000
	MaxExportRows = 1_000_000
)

// WindowError: the time window is missing or too wide.
type WindowError struct{ Msg string }

func (e *WindowError) Error() string { return e.Msg }

// InvalidError: a page parameter was refused.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// Filter narrows events. From is required unless ResourceID or ActorID is
// set.
type Filter struct {
	ResourceType, ResourceID, ActorID, Action *string
	Outcome, RequestID, TraceID               *string
	From, To                                  *time.Time
}

// CheckWindow refuses a missing or too wide window.
func CheckWindow(f Filter) error {
	hasResource := f.ResourceID != nil && *f.ResourceID != ""
	hasActor := f.ActorID != nil && *f.ActorID != ""
	if f.From == nil && !hasResource && !hasActor {
		return &WindowError{Msg: "a time window (from) is required unless resource_id or actor_id is given"}
	}
	if f.From != nil && f.To != nil && f.To.Sub(*f.From) > MaxWindow {
		return &WindowError{Msg: "the window is larger than 93 days"}
	}
	return nil
}

// Service reads the audit trail.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// Viewer is who reads the trail: an admin reads every event, anyone else
// the events they did or that touch what they own.
type Viewer struct {
	UserID pgtype.UUID
	Admin  bool
}

// Page is a page of events, newest first.
type Page struct {
	Events []db.ListAuditEventsRow
	// NextCursor is set when more events follow.
	NextCursor *string
}

// Page returns a page of events matching f. A nil limit is DefaultPage;
// cursor is the NextCursor of the previous page.
func (s *Service) Page(ctx context.Context, v Viewer, f Filter, limit *int, cursor string) (Page, error) {
	lim := DefaultPage
	if limit != nil {
		if *limit < 1 || *limit > MaxPage {
			return Page{}, &InvalidError{Msg: "limit must be between 1 and 200"}
		}
		lim = *limit
	}
	if err := CheckWindow(f); err != nil {
		return Page{}, err
	}
	params := params(f, int32(lim+1))
	if !v.Admin {
		self := v.UserID.String()
		params.ScopeActor = &self
		params.ScopeOwner = v.UserID
	}
	if cursor != "" {
		at, id, ok := decodeCursor(cursor)
		if !ok {
			return Page{}, &InvalidError{Msg: "invalid cursor"}
		}
		params.CursorAt, params.CursorID = pgtype.Timestamptz{Time: at, Valid: true}, &id
	}
	rows, err := s.store.ListAuditEvents(ctx, params)
	if err != nil {
		return Page{}, err
	}
	out := Page{Events: rows}
	if len(rows) > lim {
		out.Events = rows[:lim]
		last := out.Events[lim-1]
		c := encodeCursor(last.OccurredAt.Time, last.ID)
		out.NextCursor = &c
	}
	return out, nil
}

// Export writes every event matching f as CSV, page by page, up to
// MaxExportRows. It stops as soon as ctx ends or w fails.
func (s *Service) Export(ctx context.Context, w io.Writer, f Filter) error {
	p := params(f, ExportPage)
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"occurred_at", "actor_kind", "actor_id", "auth_method", "action", "resource_type",
		"resource_id", "workspace_id", "outcome", "reason", "request_id", "trace_id",
		"client_ip", "user_agent",
	}); err != nil {
		return err
	}
	written := 0
	for written < MaxExportRows {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := s.store.ListAuditEvents(ctx, p)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			if err := cw.Write(cells(
				r.OccurredAt.Time.UTC().Format(time.RFC3339),
				r.ActorKind, str(r.ActorID), str(r.AuthMethod), r.Action,
				r.ResourceType, str(r.ResourceID), id(r.WorkspaceID), r.Outcome,
				str(r.Reason), str(r.RequestID), str(r.TraceID),
				ip(r.ClientIp), str(r.UserAgent),
			)); err != nil {
				return err
			}
			written++
			if written >= MaxExportRows {
				break
			}
		}
		// Flushing each page makes a closed reader fail the next write.
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
		if len(rows) < int(p.Lim) {
			break
		}
		last := rows[len(rows)-1]
		p.CursorAt = last.OccurredAt
		lastID := last.ID
		p.CursorID = &lastID
	}
	cw.Flush()
	return cw.Error()
}

// User returns a user an event names.
func (s *Service) User(ctx context.Context, id pgtype.UUID) (db.User, error) {
	return s.store.GetUserByID(ctx, id)
}

// Catalog returns a catalog an event names, before the caller checks
// access to its connection.
func (s *Service) Catalog(ctx context.Context, id pgtype.UUID) (db.Catalog, error) {
	return s.store.GetCatalogByID(ctx, id)
}

// cells neutralizes values a spreadsheet would run as a formula: a cell
// starting with =, +, -, @, a tab or a carriage return gets a leading
// quote. Several columns carry client-controlled text (user agent, request
// and trace ids, reasons).
func cells(values ...string) []string {
	for i, v := range values {
		if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
			values[i] = "'" + v
		}
	}
	return values
}

func params(f Filter, lim int32) db.ListAuditEventsParams {
	p := db.ListAuditEventsParams{
		ResourceType: f.ResourceType, ResourceID: f.ResourceID, ActorID: f.ActorID, Action: f.Action,
		Outcome: f.Outcome, RequestID: f.RequestID, TraceID: f.TraceID, Lim: lim,
	}
	if f.From != nil {
		p.FromTime = pgtype.Timestamptz{Time: *f.From, Valid: true}
	}
	if f.To != nil {
		p.ToTime = pgtype.Timestamptz{Time: *f.To, Valid: true}
	}
	return p
}

// The cursor is the (occurred_at, id) of the last event of a page.
func encodeCursor(at time.Time, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(id, 10)))
}

func decodeCursor(c string) (time.Time, int64, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, 0, false
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, 0, false
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, 0, false
	}
	n, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return time.Time{}, 0, false
	}
	return at, n, true
}

func str(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func ip(v *netip.Addr) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func id(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	return uuid.UUID(v.Bytes).String()
}
