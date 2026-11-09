package content

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
)

// Store is the narrow persistence surface the content service writes through
// (ADR 0020). The signatures keep the sqlc-generated parameter and result
// types so the rest of the domain is unchanged; *repo.Repo implements it with
// GORM, and db.Querier is only a test/fixture seam.
type Store interface {
	CreateQueryFolder(ctx context.Context, arg db.CreateQueryFolderParams) (db.QueryFolder, error)
	CreateSavedQuery(ctx context.Context, arg db.CreateSavedQueryParams) (db.SavedQuery, error)
	CreateSemanticMetric(ctx context.Context, arg db.CreateSemanticMetricParams) (db.SemanticMetric, error)
	CreateSemanticModel(ctx context.Context, arg db.CreateSemanticModelParams) (db.SemanticModel, error)
	CreateSpace(ctx context.Context, arg db.CreateSpaceParams) (db.Space, error)
	DeleteSavedQuery(ctx context.Context, arg db.DeleteSavedQueryParams) (int64, error)
	DeleteSemanticMetric(ctx context.Context, arg db.DeleteSemanticMetricParams) (int64, error)
	DeleteSemanticModel(ctx context.Context, arg db.DeleteSemanticModelParams) (int64, error)
	GetBindingTargetNames(ctx context.Context, id db.UUID) (db.GetBindingTargetNamesRow, error)
	GetCatalogNameByID(ctx context.Context, id db.UUID) (string, error)
	GetContentVersion(ctx context.Context, arg db.GetContentVersionParams) (db.GetContentVersionRow, error)
	GetFolderBySlug(ctx context.Context, arg db.GetFolderBySlugParams) (db.QueryFolder, error)
	GetGrantedCatalogByName(ctx context.Context, arg db.GetGrantedCatalogByNameParams) (db.UUID, error)
	GetQueryFolderByID(ctx context.Context, arg db.GetQueryFolderByIDParams) (db.QueryFolder, error)
	GetSavedQueryByID(ctx context.Context, arg db.GetSavedQueryByIDParams) (db.SavedQuery, error)
	GetSavedQueryBySlug(ctx context.Context, arg db.GetSavedQueryBySlugParams) (db.SavedQuery, error)
	GetSavedQueryForUpdate(ctx context.Context, arg db.GetSavedQueryForUpdateParams) (db.SavedQuery, error)
	GetSemanticMetric(ctx context.Context, arg db.GetSemanticMetricParams) (db.SemanticMetric, error)
	GetSemanticMetricBySlug(ctx context.Context, arg db.GetSemanticMetricBySlugParams) (db.SemanticMetric, error)
	GetSemanticMetricForUpdate(ctx context.Context, arg db.GetSemanticMetricForUpdateParams) (db.SemanticMetric, error)
	GetSemanticModel(ctx context.Context, arg db.GetSemanticModelParams) (db.SemanticModel, error)
	GetSemanticModelBySlug(ctx context.Context, arg db.GetSemanticModelBySlugParams) (db.SemanticModel, error)
	GetSemanticModelForUpdate(ctx context.Context, arg db.GetSemanticModelForUpdateParams) (db.SemanticModel, error)
	GetSpace(ctx context.Context, arg db.GetSpaceParams) (db.Space, error)
	InsertContentVersion(ctx context.Context, arg db.InsertContentVersionParams) (db.ContentVersion, error)
	LatestContentVersion(ctx context.Context, arg db.LatestContentVersionParams) (db.ContentVersion, error)
	ListContentVersions(ctx context.Context, arg db.ListContentVersionsParams) ([]db.ListContentVersionsRow, error)
	ListDeletedContent(ctx context.Context, arg db.ListDeletedContentParams) ([]db.ListDeletedContentRow, error)
	ListSpaceFolders(ctx context.Context, spaceID db.UUID) ([]db.QueryFolder, error)
	ListSpaceMetrics(ctx context.Context, spaceID db.UUID) ([]db.SemanticMetric, error)
	ListSpaceModels(ctx context.Context, spaceID db.UUID) ([]db.SemanticModel, error)
	ListSpaceSavedQueries(ctx context.Context, spaceID db.UUID) ([]db.SavedQuery, error)
	ListWorkspaceTargets(ctx context.Context, workspaceID db.UUID) ([]db.ListWorkspaceTargetsRow, error)
	NextContentVersion(ctx context.Context, arg db.NextContentVersionParams) (int32, error)
	RestoreSavedQuery(ctx context.Context, arg db.RestoreSavedQueryParams) (db.SavedQuery, error)
	RestoreSemanticMetric(ctx context.Context, arg db.RestoreSemanticMetricParams) (db.SemanticMetric, error)
	RestoreSemanticModel(ctx context.Context, arg db.RestoreSemanticModelParams) (db.SemanticModel, error)
	UpdateQueryFolder(ctx context.Context, arg db.UpdateQueryFolderParams) (db.QueryFolder, error)
	UpdateSavedQuery(ctx context.Context, arg db.UpdateSavedQueryParams) (db.SavedQuery, error)
	UpdateSemanticMetric(ctx context.Context, arg db.UpdateSemanticMetricParams) (db.SemanticMetric, error)
	UpdateSemanticModel(ctx context.Context, arg db.UpdateSemanticModelParams) (db.SemanticModel, error)
}

// Tx runs fn in one transaction over a Store. A nil Tx means the store is
// already transaction-bound (the bundle importer) or there is no transaction
// to open (a fixture), so fn runs against the store directly.
type Tx func(ctx context.Context, fn func(Store) error) error
