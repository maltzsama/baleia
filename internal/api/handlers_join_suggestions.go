package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// joinSuggestionLimit bounds one suggestion list. The editor only needs the
// first few; the rest is noise.
const joinSuggestionLimit = 50

// ListJoinSuggestions — GET /workspaces/{id}/metadata/join-suggestions
// The equi-join conditions a workspace used between two tables in the last 90
// days, ranked by a 30-day half-life decay, plus the foreign keys between them
// (B1, #554). Both tables are resolved grant-scoped: a table of a catalog the
// workspace may not use is a 404, never an empty list.
func (h *CatalogHandlers) ListJoinSuggestions(ctx context.Context, request ListJoinSuggestionsRequestObject) (ListJoinSuggestionsResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListJoinSuggestions404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	left, notFound, err := h.grantedTable(ctx, ws, request.Params.LeftTable)
	if err != nil {
		return nil, err
	}
	if notFound {
		return ListJoinSuggestions404JSONResponse{NotFoundJSONResponse: metadataNotFound("left table not found")}, nil
	}
	right, notFound, err := h.grantedTable(ctx, ws, request.Params.RightTable)
	if err != nil {
		return nil, err
	}
	if notFound {
		return ListJoinSuggestions404JSONResponse{NotFoundJSONResponse: metadataNotFound("right table not found")}, nil
	}

	history, err := h.workbenchReader().ListJoinSuggestions(ctx, db.ListJoinSuggestionsParams{
		Lim:         joinSuggestionLimit,
		WorkspaceID: ws,
		CatalogID:   left.CatalogID,
		LeftNs:      lastNamespace(left.Namespace),
		LeftTable:   left.Name,
		RightNs:     lastNamespace(right.Namespace),
		RightTable:  right.Name,
	})
	if err != nil {
		return nil, err
	}

	keys, err := h.workbenchReader().ListForeignKeyJoinSuggestions(ctx, db.ListForeignKeyJoinSuggestionsParams{
		LeftTableID:    left.ID,
		LeftNamespace:  left.Namespace,
		LeftTable:      left.Name,
		RightTableID:   right.ID,
		RightNamespace: right.Namespace,
		RightTable:     right.Name,
	})
	if err != nil {
		return nil, err
	}

	out := make([]JoinSuggestion, 0, len(history)+len(keys))
	seen := make(map[string]bool, len(history)+len(keys))
	pair := func(l, r string) string { return l + "\x00" + r }
	for _, row := range history {
		seen[pair(row.LeftColumn, row.RightColumn)] = true
		score := row.Score
		out = append(out, JoinSuggestion{
			LeftColumn: row.LeftColumn, RightColumn: row.RightColumn,
			UseCount: int(row.UseCount), LastUsedAt: timePtr(row.LastUsedAt),
			Score: &score, Source: History,
		})
	}
	for _, fk := range keys {
		if seen[pair(fk.LeftColumn, fk.RightColumn)] {
			continue
		}
		seen[pair(fk.LeftColumn, fk.RightColumn)] = true
		out = append(out, JoinSuggestion{
			LeftColumn: fk.LeftColumn, RightColumn: fk.RightColumn,
			Source: ForeignKey,
		})
	}
	return ListJoinSuggestions200JSONResponse(out), nil
}

// grantedTable loads a live table of a catalog granted to the workspace. A
// missing, foreign or removed table is notFound, never an error.
func (h *CatalogHandlers) grantedTable(ctx context.Context, ws pgtype.UUID, id [16]byte) (db.GetGrantedMetadataTableRow, bool, error) {
	t, err := h.workbenchReader().GetGrantedMetadataTable(ctx, db.GetGrantedMetadataTableParams{
		WorkspaceID: ws, TableID: pgtype.UUID{Bytes: id, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return t, true, nil
	}
	if err != nil {
		return t, false, err
	}
	if t.RemovedAt.Valid {
		return t, true, nil
	}
	return t, false, nil
}

// lastNamespace is the one namespace level statement_references stores (B1,
// #554): its schema.
func lastNamespace(ns []string) string {
	if len(ns) == 0 {
		return ""
	}
	return ns[len(ns)-1]
}
