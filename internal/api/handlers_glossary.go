package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	textsearch "github.com/galileostudio/baleia/internal/search"
	"github.com/galileostudio/baleia/internal/semantic"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"gorm.io/gorm"
)

// Limits of a glossary entry.
const (
	glossaryMaxWords    = 64
	glossaryMaxSynonyms = 20
)

// glossaryActions is what the caller may do with the workspace's glossary:
// view it, and edit it with the editor role or above.
func (h *SemanticHandlers) glossaryActions(ctx context.Context, ws db.UUID) []string {
	actions := []string{"view"}
	if p, err := authz.PrincipalFromContext(ctx); err == nil {
		if role, err := h.authz().WorkspaceRole(ctx, p, ws); err == nil && role >= authz.WorkspaceEditor {
			actions = append(actions, "edit")
		}
	}
	return actions
}

// glossaryEntry is the public form of a stored entry.
func glossaryEntry(row model.SearchSynonym) SearchGlossaryEntry {
	return SearchGlossaryEntry{
		Id:        openapi_types.UUID(repo.PGUUID(string(row.ID)).Bytes),
		Term:      string(row.Term),
		Synonyms:  nonNilStrings([]string(row.Synonyms)),
		CreatedAt: row.CreatedAt,
	}
}

// ListSearchGlossary — GET /workspaces/{id}/search/glossary (#943).
func (h *SemanticHandlers) ListSearchGlossary(ctx context.Context, request ListSearchGlossaryRequestObject) (ListSearchGlossaryResponseObject, error) {
	ws, notFound, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if notFound {
		return ListSearchGlossary404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if h.glossary == nil {
		return nil, errors.New("search glossary is not configured")
	}
	rows, err := h.glossary.List(ctx, repo.UUIDString(ws))
	if err != nil {
		return nil, err
	}
	out := SearchGlossary{Entries: make([]SearchGlossaryEntry, 0, len(rows)), AllowedActions: h.glossaryActions(ctx, ws)}
	for _, row := range rows {
		out.Entries = append(out.Entries, glossaryEntry(row))
	}
	return ListSearchGlossary200JSONResponse(out), nil
}

// SearchGlossaryInsights reports visible semantic resources matching glossary
// members and proposes vocabulary already present in model labels/descriptions.
func (h *SemanticHandlers) SearchGlossaryInsights(ctx context.Context, request SearchGlossaryInsightsRequestObject) (SearchGlossaryInsightsResponseObject, error) {
	ws, notFound, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if notFound {
		return SearchGlossaryInsights404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if h.glossary == nil {
		return nil, errors.New("search glossary is not configured")
	}
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SearchGlossaryInsights401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	entries, err := h.glossary.List(ctx, repo.UUIDString(ws))
	if err != nil {
		return nil, err
	}
	models, err := h.queries.ListVisibleSemanticModels(ctx, db.ListVisibleSemanticModelsParams{WorkspaceID: ws, UserID: p.UserID})
	if err != nil {
		return nil, err
	}
	metrics, err := h.queries.ListVisibleSemanticMetrics(ctx, db.ListVisibleSemanticMetricsParams{WorkspaceID: ws, UserID: p.UserID})
	if err != nil {
		return nil, err
	}
	resources := make([]glossaryResource, 0, len(models)+len(metrics))
	for _, model := range models {
		resources = append(resources, glossaryResource{Kind: "semantic_model", ID: repo.UUIDString(model.ID), Label: model.Label, Description: model.Description})
		var definition semantic.Model
		if json.Unmarshal(model.Definition, &definition) == nil {
			for _, field := range definition.Dimensions {
				if !field.Hidden {
					resources = append(resources, glossaryResource{Kind: "semantic_field", ID: repo.UUIDString(model.ID), Label: model.Label + " · " + field.Name + " " + field.Label, Description: field.Description, SearchText: field.Name + " " + field.Label + " " + field.Description})
				}
			}
			for _, field := range definition.Metrics {
				resources = append(resources, glossaryResource{Kind: "semantic_field", ID: repo.UUIDString(model.ID), Label: model.Label + " · " + field.Name + " " + field.Label, SearchText: field.Name + " " + field.Label})
			}
			for _, field := range definition.CalculatedFields {
				resources = append(resources, glossaryResource{Kind: "semantic_field", ID: repo.UUIDString(model.ID), Label: model.Label + " · " + field.Name + " " + field.Label, SearchText: field.Name + " " + field.Label})
			}
		}
	}
	for _, metric := range metrics {
		resources = append(resources, glossaryResource{Kind: "semantic_metric", ID: repo.UUIDString(metric.ID), Label: metric.Label, Description: metric.Description})
	}
	out := SearchGlossaryInsights{Entries: make([]SearchGlossaryInsight, 0, len(entries))}
	for _, entry := range entries {
		insight := SearchGlossaryInsight{EntryId: openapi_types.UUID(repo.PGUUID(string(entry.ID)).Bytes), Term: string(entry.Term), Members: make([]SearchGlossaryMemberInsight, 0, len(entry.Synonyms)+1)}
		members := append([]string{string(entry.Term)}, []string(entry.Synonyms)...)
		for _, member := range members {
			mi := SearchGlossaryMemberInsight{Value: member, Matches: []SearchGlossaryResourceMatch{}, Suggestions: []string{}}
			seenSuggestions := map[string]bool{}
			for _, resource := range resources {
				searchText := resource.SearchText
				if searchText == "" {
					searchText = resource.Label + " " + resource.Description
				}
				if glossaryTextMatches(member, searchText) {
					kind := SearchGlossaryResourceMatchKind(resource.Kind)
					mi.Matches = append(mi.Matches, SearchGlossaryResourceMatch{Kind: kind, Id: openapi_types.UUID(repo.PGUUID(resource.ID).Bytes), Label: resource.Label})
					for _, token := range textsearch.Tokens(searchText) {
						if len(token) >= 3 && !slices.Contains(members, token) && !glossaryMemberContains(member, token) && !seenSuggestions[token] {
							mi.Suggestions = append(mi.Suggestions, token)
							seenSuggestions[token] = true
						}
					}
				}
			}
			if len(mi.Suggestions) > 5 {
				mi.Suggestions = mi.Suggestions[:5]
			}
			insight.Members = append(insight.Members, mi)
		}
		out.Entries = append(out.Entries, insight)
	}
	return SearchGlossaryInsights200JSONResponse(out), nil
}

type glossaryResource struct{ Kind, ID, Label, Description, SearchText string }

func glossaryMemberContains(member, token string) bool {
	return strings.Contains(textsearch.Fold(member), textsearch.Fold(token))
}
func glossaryTextMatches(member, text string) bool {
	needle := textsearch.Tokens(member)
	haystack := textsearch.Tokens(text)
	if len(needle) == 0 {
		return false
	}
	for _, n := range needle {
		found := false
		for _, h := range haystack {
			if h == n || strings.HasPrefix(h, n) || strings.HasPrefix(n, h) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// CreateSearchGlossaryEntry — POST /workspaces/{id}/search/glossary (#943).
func (h *SemanticHandlers) CreateSearchGlossaryEntry(ctx context.Context, request CreateSearchGlossaryEntryRequestObject) (CreateSearchGlossaryEntryResponseObject, error) {
	ws, notFound, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if notFound {
		return CreateSearchGlossaryEntry404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if err := h.requireWorkspaceRole(ctx, request.Id, authz.WorkspaceEditor); err != nil {
		if errors.Is(err, authz.ErrForbidden) {
			return CreateSearchGlossaryEntry403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "the glossary needs the editor role"}}, nil
		}
		return nil, err
	}
	if request.Body == nil {
		return CreateSearchGlossaryEntry422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "body is required")}, nil
	}
	term, synonyms, problem := validateGlossaryEntry(*request.Body)
	if problem != "" {
		return CreateSearchGlossaryEntry422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, problem)}, nil
	}
	if h.glossary == nil {
		return nil, errors.New("search glossary is not configured")
	}
	createdBy, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	row, err := h.glossary.Create(ctx, model.SearchSynonym{
		ID:          model.UUID(repo.NewUUID()),
		WorkspaceID: model.UUID(repo.UUIDString(ws)),
		Term:        model.Text(term),
		Synonyms:    model.StringArray(synonyms),
		CreatedBy:   model.UUID(repo.UUIDString(createdBy)),
		CreatedAt:   time.Now().UTC(),
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return CreateSearchGlossaryEntry409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: "this term already has an entry"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return CreateSearchGlossaryEntry201JSONResponse(glossaryEntry(row)), nil
}

// DeleteSearchGlossaryEntry — DELETE /workspaces/{id}/search/glossary/{entry_id}
// (#943).
func (h *SemanticHandlers) DeleteSearchGlossaryEntry(ctx context.Context, request DeleteSearchGlossaryEntryRequestObject) (DeleteSearchGlossaryEntryResponseObject, error) {
	ws, notFound, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if notFound {
		return DeleteSearchGlossaryEntry404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if err := h.requireWorkspaceRole(ctx, request.Id, authz.WorkspaceEditor); err != nil {
		if errors.Is(err, authz.ErrForbidden) {
			return DeleteSearchGlossaryEntry403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "the glossary needs the editor role"}}, nil
		}
		return nil, err
	}
	if h.glossary == nil {
		return nil, errors.New("search glossary is not configured")
	}
	removed, err := h.glossary.Delete(ctx, repo.UUIDString(ws), request.EntryId.String())
	if err != nil {
		return nil, err
	}
	if !removed {
		return DeleteSearchGlossaryEntry404JSONResponse{NotFoundJSONResponse: metadataNotFound("entry not found")}, nil
	}
	return DeleteSearchGlossaryEntry204Response{}, nil
}

// validateGlossaryEntry folds an entry's words and says what is wrong with
// it: a word with no letter or digit (the search ignores punctuation, so it
// could never match), one too long, a repeated synonym, a synonym equal to the
// term, or too many synonyms.
func validateGlossaryEntry(in SearchGlossaryEntryCreate) (term string, synonyms []string, problem string) {
	term = textsearch.Fold(in.Term)
	if !hasWord(in.Term) || utf8.RuneCountInString(in.Term) > glossaryMaxWords {
		return "", nil, "term must be 1 to 64 characters with a letter or a digit"
	}
	if len(in.Synonyms) < 1 || len(in.Synonyms) > glossaryMaxSynonyms {
		return "", nil, "synonyms must be 1 to 20 words"
	}
	synonyms = make([]string, 0, len(in.Synonyms))
	for _, s := range in.Synonyms {
		folded := textsearch.Fold(s)
		switch {
		case !hasWord(s) || utf8.RuneCountInString(s) > glossaryMaxWords:
			return "", nil, "each synonym must be 1 to 64 characters with a letter or a digit"
		case folded == term:
			return "", nil, "a synonym cannot be the term itself"
		case slices.Contains(synonyms, folded):
			return "", nil, "a synonym is repeated"
		}
		synonyms = append(synonyms, folded)
	}
	return term, synonyms, ""
}

// hasWord reports whether s has at least one word: a letter or a digit.
func hasWord(s string) bool { return len(textsearch.Tokens(s)) > 0 }

// glossaryFor is the workspace's glossary, empty when none is configured.
func (h *SemanticHandlers) glossaryFor(ctx context.Context, ws db.UUID) (textsearch.Glossary, error) {
	if h.glossary == nil {
		return textsearch.NewGlossary(nil), nil
	}
	rows, err := h.glossary.List(ctx, repo.UUIDString(ws))
	if err != nil {
		return textsearch.Glossary{}, err
	}
	entries := make([][]string, len(rows))
	for i, row := range rows {
		entries[i] = append([]string{string(row.Term)}, []string(row.Synonyms)...)
	}
	return textsearch.NewGlossary(entries), nil
}
