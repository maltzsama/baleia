package api

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/content/search"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/metadata/browse"
	"github.com/galileostudio/baleia/internal/metrics"
	textsearch "github.com/galileostudio/baleia/internal/search"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Limits of the federated search.
const (
	federatedDefaultLimit = 20
	federatedMaxLimit     = 50
	subtitleRunes         = 120
)

// federatedKinds lists the kinds in the order facets are shown and ties break.
var federatedKinds = []WorkspaceSearchKind{
	WorkspaceSearchKindTable, WorkspaceSearchKindColumn, WorkspaceSearchKindDoc,
	WorkspaceSearchKindSavedQuery, WorkspaceSearchKindSemanticMetric, WorkspaceSearchKindSemanticModel,
	WorkspaceSearchKindFolder,
}

// SearchWorkspace — GET /workspaces/{id}/search (#943). One ranked list over
// the metadata, doc and content searches, each already limited to what the
// caller may see.
func (h *SemanticHandlers) SearchWorkspace(ctx context.Context, request SearchWorkspaceRequestObject) (SearchWorkspaceResponseObject, error) {
	ws, notFound, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if notFound {
		return SearchWorkspace404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SearchWorkspace401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	params := request.Params
	text := strings.TrimSpace(params.Q)
	if n := utf8.RuneCountInString(text); n < minSearchQuery || n > maxSearchQuery {
		return SearchWorkspace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "q must be 2 to 200 characters")}, nil
	}
	limit := federatedDefaultLimit
	if params.Limit != nil {
		if *params.Limit < 1 || *params.Limit > federatedMaxLimit {
			return SearchWorkspace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "limit must be between 1 and 50")}, nil
		}
		limit = *params.Limit
	}
	wanted, ok := wantedKinds(params.Kind)
	if !ok {
		return SearchWorkspace422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "kind is not a search kind")}, nil
	}

	glossary, err := h.glossaryFor(ctx, ws)
	if err != nil {
		return nil, err
	}
	// A variant must be a query the surfaces accept: a one-letter synonym or a
	// long query with a longer word is dropped, and not reported.
	variants, expanded := glossary.Variants(text, func(q string) bool {
		n := utf8.RuneCountInString(q)
		return n >= minSearchQuery && n <= maxSearchQuery
	})
	hits, err := h.federatedHitsFor(ctx, ws, p.UserID, append([]string{text}, variants...), wanted, params, limit)
	if err != nil {
		return nil, err
	}
	candidates := make([]textsearch.Candidate, len(hits))
	for i, hit := range hits {
		candidates[i] = textsearch.Candidate{Kind: string(hit.item.Kind), Key: hit.item.Id, Score: hit.item.Score}
	}
	kindOrder := make([]string, len(federatedKinds))
	for i, k := range federatedKinds {
		kindOrder[i] = string(k)
	}
	chosen, facets := textsearch.Rank(candidates, kindOrder, limit)
	out := WorkspaceSearchResponse{
		Items:        make([]WorkspaceSearchItem, 0, len(chosen)),
		Facets:       make([]WorkspaceSearchFacet, 0, len(facets)),
		ExpandedWith: expanded,
	}
	for _, i := range chosen {
		out.Items = append(out.Items, hits[i].item)
	}
	for _, f := range facets {
		out.Facets = append(out.Facets, WorkspaceSearchFacet{Kind: WorkspaceSearchKind(f.Kind), Count: f.Count})
	}
	h.metrics.search.Result(metrics.SearchFederated, len(out.Items))
	return SearchWorkspace200JSONResponse(out), nil
}

// variantDiscount scales the score of a result found through a glossary
// synonym. At 0.75 the best a synonym can score (an exact match, about 0.78)
// stays below a whole-word match of what the person typed (0.8), so the words
// typed rank first.
const variantDiscount = 0.75

// federatedHitsFor runs the search for the query and for each glossary variant
// of it and keeps, for each result, its best score.
func (h *SemanticHandlers) federatedHitsFor(ctx context.Context, ws, user db.UUID, queries []string, wanted map[WorkspaceSearchKind]bool, params SearchWorkspaceParams, limit int) ([]federatedHit, error) {
	var out []federatedHit
	at := map[string]int{}
	for i, query := range queries {
		hits, err := h.federatedHits(ctx, ws, user, query, wanted, params, limit)
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			if i > 0 {
				hit.item.Score *= variantDiscount
			}
			key := string(hit.item.Kind) + "\x00" + hit.item.Id
			if j, seen := at[key]; seen {
				if hit.item.Score > out[j].item.Score {
					out[j] = hit
				}
				continue
			}
			at[key] = len(out)
			out = append(out, hit)
		}
	}
	return out, nil
}

// federatedHit is one result of one surface, ready to rank.
type federatedHit struct{ item WorkspaceSearchItem }

// wantedKinds is the set of kinds a request asks for; every kind when it names
// none. ok is false for a kind that does not exist.
func wantedKinds(requested *[]WorkspaceSearchKind) (map[WorkspaceSearchKind]bool, bool) {
	wanted := map[WorkspaceSearchKind]bool{}
	if requested == nil || len(*requested) == 0 {
		for _, k := range federatedKinds {
			wanted[k] = true
		}
		return wanted, true
	}
	for _, k := range *requested {
		known := false
		for _, f := range federatedKinds {
			known = known || f == k
		}
		if !known {
			return nil, false
		}
		wanted[k] = true
	}
	return wanted, true
}

// federatedHits asks each surface the request wants for its best matches.
func (h *SemanticHandlers) federatedHits(ctx context.Context, ws, user db.UUID, text string, wanted map[WorkspaceSearchKind]bool, params SearchWorkspaceParams, limit int) ([]federatedHit, error) {
	var hits []federatedHit
	catalog := db.UUID{}
	if params.CatalogId != nil {
		catalog = db.UUID{Bytes: *params.CatalogId, Valid: true}
	}

	if wanted[WorkspaceSearchKindTable] || wanted[WorkspaceSearchKindColumn] {
		query := browse.SearchQuery{Text: text, Limit: &limit, CatalogID: catalog, Kind: browse.KindAll}
		switch {
		case !wanted[WorkspaceSearchKindColumn]:
			query.Kind = browse.KindTable
		case !wanted[WorkspaceSearchKindTable]:
			query.Kind = browse.KindColumn
		}
		rows, err := h.browse.Search(ctx, ws, query)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			hits = append(hits, federatedHit{item: metadataItem(r)})
		}
	}

	if wanted[WorkspaceSearchKindDoc] {
		rows, err := h.docs.Search(ctx, db.SearchSemanticDocsParams{Q: text, WorkspaceID: ws, CatalogID: catalog, Lim: int32(limit)}) //nolint:gosec // limit is 1..50, validated above
		if err != nil {
			return nil, err
		}
		actions := h.docActions(ctx, ws)
		for _, r := range rows {
			doc := dbSemanticSearchRowToPublic(r)
			doc.AllowedActions = &actions
			hits = append(hits, federatedHit{item: docItem(doc)})
		}
	}

	if kinds := wantedContentKinds(wanted); len(kinds) > 0 {
		query := search.Query{Text: text, Limit: limit}
		if params.SpaceId != nil {
			query.SpaceID = db.UUID{Bytes: *params.SpaceId, Valid: true}
		}
		// Every content kind is one search; a subset is one search per kind, so
		// the limit of the content search never cuts a wanted kind short for
		// the benefit of one that is not wanted.
		queries := []search.Query{query}
		if len(kinds) < len(contentKinds) {
			queries = queries[:0]
			for _, kind := range kinds {
				q := query
				q.Kind = kind
				queries = append(queries, q)
			}
		}
		for _, q := range queries {
			found, err := h.search.Search(ctx, ws, user, q)
			if err != nil {
				return nil, err
			}
			for _, hit := range found {
				hits = append(hits, federatedHit{item: contentItem(contentSearchItem(hit), hit.Score, hit.SpaceName)})
			}
		}
	}
	return hits, nil
}

// contentKinds are the kinds the content search serves.
var contentKinds = []string{search.KindFolder, search.KindSavedQuery, search.KindSemanticMetric, search.KindSemanticModel}

// wantedContentKinds are the content kinds a request asks for.
func wantedContentKinds(wanted map[WorkspaceSearchKind]bool) []string {
	var out []string
	for _, kind := range contentKinds {
		if wanted[WorkspaceSearchKind(kind)] {
			out = append(out, kind)
		}
	}
	return out
}

// metadataItem is a table or column hit as a federated result.
func metadataItem(r db.SearchMetadataRow) WorkspaceSearchItem {
	res := metadataSearchResult(r)
	item := WorkspaceSearchItem{
		Kind:     WorkspaceSearchKind(r.Kind),
		Id:       uuidText(res.TableId),
		Title:    res.Table,
		Score:    res.Score,
		Metadata: &res,
	}
	location := strings.Join(res.Namespace, ".")
	if res.Column != nil {
		item.Id += "/" + *res.Column
		item.Title = *res.Column
		location += "." + res.Table
	}
	item.Subtitle = &location
	return item
}

// docItem is a doc hit as a federated result.
func docItem(doc SemanticDocSearchResult) WorkspaceSearchItem {
	title := doc.Table
	if doc.Column != nil {
		title += "." + *doc.Column
	}
	subtitle := clipRunes(doc.Description, subtitleRunes)
	return WorkspaceSearchItem{
		Kind:     WorkspaceSearchKindDoc,
		Id:       uuidText(doc.Id),
		Title:    title,
		Subtitle: &subtitle,
		Score:    doc.Score,
		Doc:      &doc,
	}
}

// contentItem is a saved query, metric, model or folder hit as a federated
// result.
func contentItem(c ContentSearchItem, score float64, space string) WorkspaceSearchItem {
	return WorkspaceSearchItem{
		Kind:     WorkspaceSearchKind(c.Kind),
		Id:       uuidText(c.Id),
		Title:    c.Title,
		Subtitle: &space,
		Score:    score,
		Content:  &c,
	}
}

// uuidText renders an id.
func uuidText(id openapi_types.UUID) string { return id.String() }

// clipRunes keeps the first n runes of s.
func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
