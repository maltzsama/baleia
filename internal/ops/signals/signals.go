// Package signals reads the operational signals behind the system status
// page (#291) and the attention list (#261): engines, binding states,
// metadata freshness, reconcile runs, audit export lag, plugin confirmation,
// and the rows that need someone's attention. The caller decides what each
// signal means and who may see it.
package signals

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Status reads what the status page shows; *db.Queries implements it.
type Status interface {
	ListEngines(ctx context.Context) ([]db.Engine, error)
	ListEnginesVisibleToUser(ctx context.Context, userID pgtype.UUID) ([]db.Engine, error)
	CountManagedBindingsByState(ctx context.Context) ([]db.CountManagedBindingsByStateRow, error)
	MetadataStatusCounts(ctx context.Context, staleSeconds int32) (db.MetadataStatusCountsRow, error)
	LatestReconcileRuns(ctx context.Context) ([]db.LatestReconcileRunsRow, error)
	AuditExportLagSeconds(ctx context.Context) (float64, error)
	PluginLastConfirmation(ctx context.Context, updatedBy string) (pgtype.Timestamptz, error)
}

// Attention reads the rows that need attention; *db.Queries implements it.
type Attention interface {
	ListAttentionBindingErrors(ctx context.Context) ([]db.ListAttentionBindingErrorsRow, error)
	ListAttentionMetadataSyncFailed(ctx context.Context) ([]db.ListAttentionMetadataSyncFailedRow, error)
	ListAttentionMetadataStale(ctx context.Context, secs float64) ([]db.ListAttentionMetadataStaleRow, error)
	ListAttentionRetrieverErrors(ctx context.Context) ([]db.ListAttentionRetrieverErrorsRow, error)
	ListAttentionReconcileDrift(ctx context.Context) ([]db.ListAttentionReconcileDriftRow, error)
	GetAttentionAuditExportLag(ctx context.Context) (pgtype.Timestamptz, error)
}

// Store is Status and Attention.
type Store interface {
	Status
	Attention
}

// Service reads signals.
type Service struct{ store Store }

// New builds the service; a nil store makes it unavailable.
func New(store Store) *Service { return &Service{store: store} }

// Available reports whether there is a database to read from.
func (s *Service) Available() bool { return s != nil && s.store != nil }

// Engines returns every engine to an admin, and to anyone else the engines
// they may see.
func (s *Service) Engines(ctx context.Context, userID pgtype.UUID, admin bool) ([]db.Engine, error) {
	if admin {
		return s.store.ListEngines(ctx)
	}
	return s.store.ListEnginesVisibleToUser(ctx, userID)
}

// BindingStates counts managed bindings by lifecycle state.
func (s *Service) BindingStates(ctx context.Context) ([]db.CountManagedBindingsByStateRow, error) {
	return s.store.CountManagedBindingsByState(ctx)
}

// MetadataCounts counts catalogs, stale ones (not synced for staleSeconds)
// and failed ones.
func (s *Service) MetadataCounts(ctx context.Context, staleSeconds int32) (db.MetadataStatusCountsRow, error) {
	return s.store.MetadataStatusCounts(ctx, staleSeconds)
}

// LatestReconcileRuns returns each engine's latest reconcile run.
func (s *Service) LatestReconcileRuns(ctx context.Context) ([]db.LatestReconcileRunsRow, error) {
	return s.store.LatestReconcileRuns(ctx)
}

// AuditExportLag is how far, in seconds, the audit export is behind.
func (s *Service) AuditExportLag(ctx context.Context) (float64, error) {
	return s.store.AuditExportLagSeconds(ctx)
}

// PluginLastConfirmation is when the Trino plugin last confirmed its
// configuration; invalid when it never did.
func (s *Service) PluginLastConfirmation(ctx context.Context) (pgtype.Timestamptz, error) {
	return s.store.PluginLastConfirmation(ctx, "trino")
}

// BindingErrors lists bindings in error.
func (s *Service) BindingErrors(ctx context.Context) ([]db.ListAttentionBindingErrorsRow, error) {
	return s.store.ListAttentionBindingErrors(ctx)
}

// MetadataFailures lists catalogs whose last metadata pass failed.
func (s *Service) MetadataFailures(ctx context.Context) ([]db.ListAttentionMetadataSyncFailedRow, error) {
	return s.store.ListAttentionMetadataSyncFailed(ctx)
}

// MetadataStale lists catalogs not synced for staleSeconds.
func (s *Service) MetadataStale(ctx context.Context, staleSeconds float64) ([]db.ListAttentionMetadataStaleRow, error) {
	return s.store.ListAttentionMetadataStale(ctx, staleSeconds)
}

// RetrieverErrors lists retrievers whose last run failed.
func (s *Service) RetrieverErrors(ctx context.Context) ([]db.ListAttentionRetrieverErrorsRow, error) {
	return s.store.ListAttentionRetrieverErrors(ctx)
}

// ReconcileDrift lists engines whose catalogs drifted from Baleia's.
func (s *Service) ReconcileDrift(ctx context.Context) ([]db.ListAttentionReconcileDriftRow, error) {
	return s.store.ListAttentionReconcileDrift(ctx)
}

// AuditExportSince is the oldest event the audit export has not shipped;
// invalid when it is caught up.
func (s *Service) AuditExportSince(ctx context.Context) (pgtype.Timestamptz, error) {
	return s.store.GetAttentionAuditExportLag(ctx)
}
