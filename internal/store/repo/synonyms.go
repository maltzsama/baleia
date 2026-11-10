package repo

import (
	"context"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// SearchSynonyms persists the search glossary of each workspace (#943).
type SearchSynonyms struct{ db *gorm.DB }

// NewSearchSynonyms builds the glossary store.
func NewSearchSynonyms(db *gorm.DB) *SearchSynonyms { return &SearchSynonyms{db: db} }

// List is a workspace's glossary entries, oldest first.
func (r *SearchSynonyms) List(ctx context.Context, workspaceID string) ([]model.SearchSynonym, error) {
	var rows []model.SearchSynonym
	err := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).Order("created_at").Order("term").Find(&rows).Error
	return rows, err
}

// Create stores an entry. A workspace has one entry per term: another one
// fails with gorm.ErrDuplicatedKey.
func (r *SearchSynonyms) Create(ctx context.Context, row model.SearchSynonym) (model.SearchSynonym, error) {
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return model.SearchSynonym{}, err
	}
	return row, nil
}

// Delete removes an entry of the workspace and reports whether it existed.
func (r *SearchSynonyms) Delete(ctx context.Context, workspaceID, id string) (bool, error) {
	res := r.db.WithContext(ctx).Where("workspace_id = ? AND id = ?", workspaceID, id).Delete(&model.SearchSynonym{})
	return res.RowsAffected > 0, res.Error
}
