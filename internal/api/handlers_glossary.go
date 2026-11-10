package api

import (
	"context"
	"errors"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	textsearch "github.com/galileostudio/baleia/internal/search"
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
