// Package search finds a workspace's saved content by name or slug: folders,
// saved queries, semantic metrics and semantic models, among those the user
// can see (#131). The caller checks the user may read the workspace.
package search

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store reads the visible content and the space names; *db.Queries
// implements it.
type Store interface {
	ListSpacesForPrincipal(ctx context.Context, arg db.ListSpacesForPrincipalParams) ([]db.ListSpacesForPrincipalRow, error)
	ListAllVisibleQueryFolders(ctx context.Context, arg db.ListAllVisibleQueryFoldersParams) ([]db.QueryFolder, error)
	SearchVisibleSavedQueries(ctx context.Context, arg db.SearchVisibleSavedQueriesParams) ([]db.SavedQuery, error)
	ListVisibleSemanticMetrics(ctx context.Context, arg db.ListVisibleSemanticMetricsParams) ([]db.SemanticMetric, error)
	ListVisibleSemanticModels(ctx context.Context, arg db.ListVisibleSemanticModelsParams) ([]db.SemanticModel, error)
}

// Kinds of content a search returns.
const (
	KindFolder         = "folder"
	KindSavedQuery     = "saved_query"
	KindSemanticMetric = "semantic_metric"
	KindSemanticModel  = "semantic_model"
)

// Limits on a search.
const (
	MinQuery     = 2
	DefaultLimit = 20
	MaxLimit     = 50
)

// Query is a search. Kind narrows to one kind; a valid SpaceID to one space.
type Query struct {
	Text    string
	Kind    string
	SpaceID pgtype.UUID
	// Limit outside 1..MaxLimit is DefaultLimit.
	Limit int
}

// Folder is one step of a hit's folder path.
type Folder struct {
	ID   pgtype.UUID
	Name string
}

// Hit is one result.
type Hit struct {
	Kind      string
	ID        pgtype.UUID
	Slug      string
	Title     string
	SpaceID   pgtype.UUID
	SpaceName string
	// FolderPath runs from the root to the hit's folder; empty at the root
	// and for semantic content.
	FolderPath []Folder
	UpdatedAt  time.Time
	// Certified is set for semantic content.
	Certified *bool
}

// Service searches content.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// Search returns the hits for q visible to userID, ordered by title. A query
// shorter than MinQuery has no hits.
func (s *Service) Search(ctx context.Context, ws, userID pgtype.UUID, q Query) ([]Hit, error) {
	text := strings.TrimSpace(q.Text)
	if len([]rune(text)) < MinQuery {
		return []Hit{}, nil
	}
	limit := q.Limit
	if limit < 1 || limit > MaxLimit {
		limit = DefaultLimit
	}

	spaces, err := s.store.ListSpacesForPrincipal(ctx, db.ListSpacesForPrincipalParams{WorkspaceID: ws, UserID: userID})
	if err != nil {
		return nil, err
	}
	spaceNames := make(map[[16]byte]string, len(spaces))
	for _, sp := range spaces {
		spaceNames[sp.ID.Bytes] = sp.Name
	}
	folders, err := s.store.ListAllVisibleQueryFolders(ctx, db.ListAllVisibleQueryFoldersParams{WorkspaceID: ws, UserID: userID, SpaceID: q.SpaceID})
	if err != nil {
		return nil, err
	}
	paths := newPaths(folders)

	lower := strings.ToLower(text)
	matches := func(fields ...string) bool {
		for _, f := range fields {
			if strings.Contains(strings.ToLower(f), lower) {
				return true
			}
		}
		return false
	}
	want := func(kind string) bool { return q.Kind == "" || q.Kind == kind }
	hit := func(kind string, id pgtype.UUID, slug, title string, space pgtype.UUID, path []Folder, updated pgtype.Timestamptz, certified *bool) Hit {
		return Hit{Kind: kind, ID: id, Slug: slug, Title: title, SpaceID: space, SpaceName: spaceNames[space.Bytes], FolderPath: path, UpdatedAt: updated.Time, Certified: certified}
	}

	hits := make([]Hit, 0, limit)
	if want(KindFolder) {
		for _, f := range folders {
			if matches(f.Name, f.Slug) {
				hits = append(hits, hit(KindFolder, f.ID, f.Slug, f.Name, f.SpaceID, paths.to(f.ParentID), f.CreatedAt, nil))
			}
		}
	}
	if want(KindSavedQuery) {
		queries, err := s.store.SearchVisibleSavedQueries(ctx, db.SearchVisibleSavedQueriesParams{WorkspaceID: ws, UserID: userID, SpaceID: q.SpaceID, Q: &text})
		if err != nil {
			return nil, err
		}
		for _, sq := range queries {
			hits = append(hits, hit(KindSavedQuery, sq.ID, sq.Slug, sq.Name, sq.SpaceID, paths.to(sq.FolderID), sq.UpdatedAt, nil))
		}
	}
	if want(KindSemanticMetric) {
		metrics, err := s.store.ListVisibleSemanticMetrics(ctx, db.ListVisibleSemanticMetricsParams{WorkspaceID: ws, UserID: userID, SpaceID: q.SpaceID})
		if err != nil {
			return nil, err
		}
		for _, m := range metrics {
			if matches(m.Label, m.Slug) {
				certified := m.Certified
				hits = append(hits, hit(KindSemanticMetric, m.ID, m.Slug, m.Label, m.SpaceID, []Folder{}, m.UpdatedAt, &certified))
			}
		}
	}
	if want(KindSemanticModel) {
		models, err := s.store.ListVisibleSemanticModels(ctx, db.ListVisibleSemanticModelsParams{WorkspaceID: ws, UserID: userID, SpaceID: q.SpaceID})
		if err != nil {
			return nil, err
		}
		for _, m := range models {
			if matches(m.Label, m.Slug) {
				certified := m.Certified
				hits = append(hits, hit(KindSemanticModel, m.ID, m.Slug, m.Label, m.SpaceID, []Folder{}, m.UpdatedAt, &certified))
			}
		}
	}

	sort.SliceStable(hits, func(i, j int) bool { return strings.ToLower(hits[i].Title) < strings.ToLower(hits[j].Title) })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// paths resolves folder paths from the visible folders.
type paths map[[16]byte]db.QueryFolder

func newPaths(folders []db.QueryFolder) paths {
	p := make(paths, len(folders))
	for _, f := range folders {
		p[f.ID.Bytes] = f
	}
	return p
}

// to returns the path from the root down to id; a folder the user cannot
// see, or a cycle, ends it.
func (p paths) to(id pgtype.UUID) []Folder {
	out := []Folder{}
	seen := map[[16]byte]bool{}
	for id.Valid && !seen[id.Bytes] {
		seen[id.Bytes] = true
		f, ok := p[id.Bytes]
		if !ok {
			break
		}
		out = append([]Folder{{ID: f.ID, Name: f.Name}}, out...)
		id = f.ParentID
	}
	return out
}
