// Package docs documents tables and columns of a workspace's catalogs (#116):
// a description, tags, an owner and a certification. The caller checks that
// the workspace may use the catalog and says whether the actor may certify.
package docs

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reader reads docs and tags; *db.Queries implements it.
type Reader interface {
	GetSemanticDoc(ctx context.Context, arg db.GetSemanticDocParams) (db.SemanticDoc, error)
	ListSemanticDocsForTable(ctx context.Context, arg db.ListSemanticDocsForTableParams) ([]db.SemanticDoc, error)
	SearchSemanticDocs(ctx context.Context, arg db.SearchSemanticDocsParams) ([]db.SearchSemanticDocsRow, error)
	ListSemanticTags(ctx context.Context, arg db.ListSemanticTagsParams) ([]db.ListSemanticTagsRow, error)
}

// Writer writes docs; *db.Queries implements it.
type Writer interface {
	UpsertSemanticDoc(ctx context.Context, arg db.UpsertSemanticDocParams) (db.SemanticDoc, error)
	DeleteSemanticDoc(ctx context.Context, arg db.DeleteSemanticDocParams) (int64, error)
}

// Store is Reader and Writer.
type Store interface {
	Reader
	Writer
}

// MaxDescription is the longest description, in characters.
const MaxDescription = 20000

// MaxTags is the most tags a doc may carry.
const MaxTags = 20

var tagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

var (
	// ErrNotFound: no doc documents this table or column.
	ErrNotFound = errors.New("docs: not found")
	// ErrDescriptionTooLong: the description is over MaxDescription.
	ErrDescriptionTooLong = errors.New("description must be at most 20000 characters")
	// ErrCertifyForbidden: only a workspace owner or an admin may certify.
	ErrCertifyForbidden = errors.New("only a workspace owner or admin may certify docs")
)

// InvalidTagsError says why a tag list was refused.
type InvalidTagsError struct{ Msg string }

func (e *InvalidTagsError) Error() string { return e.Msg }

// ConflictError: the doc changed since the client loaded it.
type ConflictError struct {
	UpdatedBy pgtype.UUID
	UpdatedAt time.Time
}

func (e *ConflictError) Error() string { return "docs: the doc changed since you loaded it" }

// Key names a documented table, or one of its columns when Column is set.
type Key struct {
	WorkspaceID pgtype.UUID
	CatalogID   pgtype.UUID
	Namespace   []string
	Table       string
	// Column is nil for a table-level doc; an empty name means nil.
	Column *string
}

func (k Key) column() *string {
	if k.Column == nil || strings.TrimSpace(*k.Column) == "" {
		return nil
	}
	return k.Column
}

// Service manages docs.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// Get returns the doc of a column, or of the table when k has no column.
func (s *Service) Get(ctx context.Context, k Key) (db.SemanticDoc, error) {
	d, err := s.store.GetSemanticDoc(ctx, db.GetSemanticDocParams{
		WorkspaceID: k.WorkspaceID, CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table, ColumnName: k.column(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.SemanticDoc{}, ErrNotFound
	}
	return d, err
}

// ListForTable returns the docs of a table and its columns.
func (s *Service) ListForTable(ctx context.Context, k Key) ([]db.SemanticDoc, error) {
	return s.store.ListSemanticDocsForTable(ctx, db.ListSemanticDocsForTableParams{
		WorkspaceID: k.WorkspaceID, CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table, ColumnName: nil,
	})
}

// Edit is a doc write. Certified nil keeps the certification, which an
// uncertifying editor loses when they change the description.
type Edit struct {
	Description string
	Tags        *[]string
	Owner       pgtype.UUID
	Certified   *bool
	// ExpectedUpdatedAt rejects the write when the doc changed after it.
	ExpectedUpdatedAt *time.Time
}

// Upsert writes the doc at k as actor. canCertify says whether the actor may
// certify. certificationChanged reports a certify or uncertify, for the audit
// trail.
func (s *Service) Upsert(ctx context.Context, k Key, e Edit, actor pgtype.UUID, canCertify bool) (doc db.SemanticDoc, certificationChanged bool, err error) {
	if utf8.RuneCountInString(e.Description) > MaxDescription {
		return db.SemanticDoc{}, false, ErrDescriptionTooLong
	}
	tags, err := NormalizeTags(e.Tags)
	if err != nil {
		return db.SemanticDoc{}, false, err
	}
	current, err := s.Get(ctx, k)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return db.SemanticDoc{}, false, err
	}
	if exists && e.ExpectedUpdatedAt != nil && current.UpdatedAt.Valid && !e.ExpectedUpdatedAt.Equal(current.UpdatedAt.Time) {
		return db.SemanticDoc{}, false, &ConflictError{UpdatedBy: current.UpdatedBy, UpdatedAt: current.UpdatedAt.Time}
	}

	wasCertified := exists && current.Certified
	certified := wasCertified
	if e.Certified != nil {
		if *e.Certified && !canCertify {
			return db.SemanticDoc{}, false, ErrCertifyForbidden
		}
		certified = *e.Certified
	} else if wasCertified && current.Description != e.Description && !canCertify {
		certified = false
	}

	doc, err = s.store.UpsertSemanticDoc(ctx, db.UpsertSemanticDocParams{
		WorkspaceID: k.WorkspaceID,
		CatalogID:   k.CatalogID,
		Namespace:   k.Namespace,
		TableName:   k.Table,
		ColumnName:  k.column(),
		Description: e.Description,
		Tags:        tags,
		OwnerUserID: e.Owner,
		UpdatedBy:   actor,
		Certified:   certified,
	})
	if err != nil {
		return db.SemanticDoc{}, false, err
	}
	return doc, certified != wasCertified, nil
}

// Delete removes the doc at k.
func (s *Service) Delete(ctx context.Context, k Key) error {
	n, err := s.store.DeleteSemanticDoc(ctx, db.DeleteSemanticDocParams{
		WorkspaceID: k.WorkspaceID, CatalogID: k.CatalogID, Namespace: k.Namespace, TableName: k.Table, ColumnName: k.column(),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Search finds docs of the workspace; a blank tag is no filter.
func (s *Service) Search(ctx context.Context, q db.SearchSemanticDocsParams) ([]db.SearchSemanticDocsRow, error) {
	if q.Tag != nil && strings.TrimSpace(*q.Tag) == "" {
		q.Tag = nil
	}
	return s.store.SearchSemanticDocs(ctx, q)
}

// Tags lists the workspace's tags matching the prefix q, with their counts.
func (s *Service) Tags(ctx context.Context, ws pgtype.UUID, q string, limit int32) ([]db.ListSemanticTagsRow, error) {
	return s.store.ListSemanticTags(ctx, db.ListSemanticTagsParams{WorkspaceID: ws, Q: strings.ToLower(strings.TrimSpace(q)), Lim: limit})
}

// NormalizeTags lowercases, dedupes and validates a tag list (#116). A nil
// list is empty.
func NormalizeTags(tags *[]string) ([]string, error) {
	if tags == nil {
		return []string{}, nil
	}
	if len(*tags) > MaxTags {
		return nil, &InvalidTagsError{Msg: "at most 20 tags are allowed"}
	}
	out := make([]string, 0, len(*tags))
	seen := map[string]bool{}
	for _, tag := range *tags {
		t := strings.ToLower(strings.TrimSpace(tag))
		if !tagRe.MatchString(t) {
			return nil, &InvalidTagsError{Msg: "tags must match " + tagRe.String()}
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, nil
}
