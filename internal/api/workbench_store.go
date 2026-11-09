package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
)

// workbenchReads are the workbench reads that have a GORM path (#770): the
// agent lookup of a run, the Trino profile of a statement, and the join
// suggestions of two tables. *repo.Repo implements it.
type workbenchReads interface {
	GetAgentByID(ctx context.Context, id db.UUID) (db.Agent, error)
	GetTrinoQuerySnapshot(ctx context.Context, trinoQueryID string) (db.TrinoQuerySnapshot, error)
	ListJoinSuggestions(ctx context.Context, arg db.ListJoinSuggestionsParams) ([]db.ListJoinSuggestionsRow, error)
	ListForeignKeyJoinSuggestions(ctx context.Context, arg db.ListForeignKeyJoinSuggestionsParams) ([]db.ListForeignKeyJoinSuggestionsRow, error)
	GetGrantedMetadataTable(ctx context.Context, arg db.GetGrantedMetadataTableParams) (db.GetGrantedMetadataTableRow, error)
}

// workbenchReader selects the GORM store when it is configured, sqlc
// otherwise.
func (h *core) workbenchReader() workbenchReads {
	if h.store != nil {
		return repo.New(h.store)
	}
	return h.queries
}
