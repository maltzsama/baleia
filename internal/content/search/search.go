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
	textsearch "github.com/galileostudio/baleia/internal/search"
)

// Reader reads the visible content and the space names; *repo.Repo
// implements it.
type Reader interface {
	ListSpacesForPrincipal(ctx context.Context, arg db.ListSpacesForPrincipalParams) ([]db.ListSpacesForPrincipalRow, error)
	ListAllVisibleQueryFolders(ctx context.Context, arg db.ListAllVisibleQueryFoldersParams) ([]db.QueryFolder, error)
	SearchVisibleSavedQueries(ctx context.Context, arg db.SearchVisibleSavedQueriesParams) ([]db.SavedQuery, error)
	ListVisibleSemanticMetrics(ctx context.Context, arg db.ListVisibleSemanticMetricsParams) ([]db.SemanticMetric, error)
	ListVisibleSemanticModels(ctx context.Context, arg db.ListVisibleSemanticModelsParams) ([]db.SemanticModel, error)
}

// Store is a Reader that also proposes, from the search index, the content
// that may match a query (ADR 0023); the Reader's lists read and filter it.
type Store interface {
	Reader
	ContentCandidates(ctx context.Context, ws db.UUID, p textsearch.Pattern) (Candidates, error)
}

// Candidates are the ids of content whose indexed words match a query. All
// means there is no index to ask: every visible row is a candidate.
type Candidates struct {
	All          bool
	SavedQueries []db.UUID
	Metrics      []db.UUID
	Models       []db.UUID
}

// Unindexed makes a Reader that has no search index a Store: every visible
// row is a candidate.
type Unindexed struct{ Reader }

// ContentCandidates proposes everything.
func (Unindexed) ContentCandidates(context.Context, db.UUID, textsearch.Pattern) (Candidates, error) {
	return Candidates{All: true}, nil
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
	SpaceID db.UUID
	// Limit outside 1..MaxLimit is DefaultLimit.
	Limit int
}

// Folder is one step of a hit's folder path.
type Folder struct {
	ID   db.UUID
	Name string
}

// Hit is one result.
type Hit struct {
	Kind      string
	ID        db.UUID
	Slug      string
	Title     string
	SpaceID   db.UUID
	SpaceName string
	// FolderPath runs from the root to the hit's folder; empty at the root
	// and for semantic content.
	FolderPath []Folder
	UpdatedAt  time.Time
	// Certified is set for semantic content.
	Certified *bool
	// Score ranks the hit: how well it matches, raised a little for
	// certified, own and recent content (#941).
	Score float64
}

// Ranking boosts: each multiplies the match score by 1 plus its value, at
// most 8% together. They mostly order hits within a match tier, but all of
// them together can lift a hit just above the best hits of the next tier up
// (a certified, own and fresh prefix match above a plain exact one).
const (
	certifiedBoost = 0.04
	ownBoost       = 0.02
	recentBoost    = 0.02 // decays to 0 over recentWindow
	recentWindow   = 90 * 24 * time.Hour
)

// Service searches content.
type Service struct {
	store Store
	now   func() time.Time
}

// New builds the service.
func New(store Store) *Service { return &Service{store: store, now: time.Now} }

// Search returns the hits for q visible to userID, best first: a hit's name
// or slug matches q (case, accents and word order folded), then the ranking
// boosts apply, and only then is the list cut to the limit. A query shorter
// than MinQuery has no hits.
func (s *Service) Search(ctx context.Context, ws, userID db.UUID, q Query) ([]Hit, error) {
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

	pattern := textsearch.Compile(text)
	candidates, err := s.store.ContentCandidates(ctx, ws, pattern)
	if err != nil {
		return nil, err
	}
	now := s.now()
	want := func(kind string) bool { return q.Kind == "" || q.Kind == kind }
	hits := make([]Hit, 0, limit)
	add := func(match float64, h Hit, owner db.UUID) {
		if match == 0 {
			return
		}
		boost := 1.0
		if h.Certified != nil && *h.Certified {
			boost += certifiedBoost
		}
		if owner.Valid && owner == userID {
			boost += ownBoost
		}
		if age := now.Sub(h.UpdatedAt); age >= 0 && age < recentWindow {
			boost += recentBoost * (1 - float64(age)/float64(recentWindow))
		}
		h.Score = match * boost
		h.SpaceName = spaceNames[h.SpaceID.Bytes]
		hits = append(hits, h)
	}
	nameAndSlug := func(name, slug string) float64 {
		return pattern.Best(textsearch.Field{Text: name, Weight: 1}, textsearch.Field{Text: slug, Weight: 1})
	}

	if want(KindFolder) {
		for _, f := range folders {
			add(nameAndSlug(f.Name, f.Slug), Hit{
				Kind: KindFolder, ID: f.ID, Slug: f.Slug, Title: f.Name, SpaceID: f.SpaceID,
				FolderPath: paths.to(f.ParentID), UpdatedAt: f.CreatedAt.Time,
			}, db.UUID{})
		}
	}
	if want(KindSavedQuery) && (candidates.All || len(candidates.SavedQueries) > 0) {
		queries, err := s.store.SearchVisibleSavedQueries(ctx, db.SearchVisibleSavedQueriesParams{WorkspaceID: ws, UserID: userID, SpaceID: q.SpaceID, IDs: candidates.SavedQueries})
		if err != nil {
			return nil, err
		}
		for _, sq := range queries {
			add(pattern.Best(
				textsearch.Field{Text: sq.Name, Weight: 1},
				textsearch.Field{Text: sq.Slug, Weight: 1},
				textsearch.Field{Text: clip(sq.Sql, maxSQLRunes), Weight: sqlWeight},
			), Hit{
				Kind: KindSavedQuery, ID: sq.ID, Slug: sq.Slug, Title: sq.Name, SpaceID: sq.SpaceID,
				FolderPath: paths.to(sq.FolderID), UpdatedAt: sq.UpdatedAt.Time,
			}, sq.CreatedBy)
		}
	}
	if want(KindSemanticMetric) && (candidates.All || len(candidates.Metrics) > 0) {
		metrics, err := s.store.ListVisibleSemanticMetrics(ctx, db.ListVisibleSemanticMetricsParams{WorkspaceID: ws, UserID: userID, SpaceID: q.SpaceID, IDs: candidates.Metrics})
		if err != nil {
			return nil, err
		}
		for _, m := range metrics {
			certified := m.Certified
			add(semanticScore(pattern, m.Label, m.Slug, m.Description, m.Definition), Hit{
				Kind: KindSemanticMetric, ID: m.ID, Slug: m.Slug, Title: m.Label, SpaceID: m.SpaceID,
				FolderPath: []Folder{}, UpdatedAt: m.UpdatedAt.Time, Certified: &certified,
			}, m.OwnerUserID)
		}
	}
	if want(KindSemanticModel) && (candidates.All || len(candidates.Models) > 0) {
		models, err := s.store.ListVisibleSemanticModels(ctx, db.ListVisibleSemanticModelsParams{WorkspaceID: ws, UserID: userID, SpaceID: q.SpaceID, IDs: candidates.Models})
		if err != nil {
			return nil, err
		}
		for _, m := range models {
			certified := m.Certified
			add(semanticScore(pattern, m.Label, m.Slug, m.Description, m.Definition), Hit{
				Kind: KindSemanticModel, ID: m.ID, Slug: m.Slug, Title: m.Label, SpaceID: m.SpaceID,
				FolderPath: []Folder{}, UpdatedAt: m.UpdatedAt.Time, Certified: &certified,
			}, m.OwnerUserID)
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return strings.ToLower(hits[i].Title) < strings.ToLower(hits[j].Title)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// Weights of the texts of content other than its name and slug.
const (
	descriptionWeight = 0.6
	fieldsWeight      = 0.5 // the names a metric or model definition mentions
	sqlWeight         = 0.3 // a saved query's SQL
	maxSQLRunes       = 4000
)

// semanticScore is how well a metric or model matches: its label and slug,
// its description and the names inside its definition.
func semanticScore(p textsearch.Pattern, label, slug, description string, definition []byte) float64 {
	return p.Best(
		textsearch.Field{Text: label, Weight: 1},
		textsearch.Field{Text: slug, Weight: 1},
		textsearch.Field{Text: description, Weight: descriptionWeight},
		textsearch.Field{Text: strings.Join(textsearch.DefinitionWords(definition), " "), Weight: fieldsWeight},
	)
}

// clip keeps the first n runes of s.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// paths resolves folder paths from the visible folders.
type paths map[[16]byte]db.QueryFolder

// newPaths indexes the visible folders by id.
func newPaths(folders []db.QueryFolder) paths {
	p := make(paths, len(folders))
	for _, f := range folders {
		p[f.ID.Bytes] = f
	}
	return p
}

// to returns the path from the root down to id; a folder the user cannot
// see, or a cycle, ends it.
func (p paths) to(id db.UUID) []Folder {
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
