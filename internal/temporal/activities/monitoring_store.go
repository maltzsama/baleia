package activities

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/monitoring"
	"github.com/jackc/pgx/v5/pgtype"
)

// monitoringStore is the monitoring and maintenance persistence the worker
// needs: the narrow monitoring service store plus the snapshot, health, run and
// binding writes it shares with sqlc. *db.Queries and *repo.Repo both
// implement it (#773).
type monitoringStore interface {
	monitoring.Store
	UpsertTrinoQuerySnapshot(ctx context.Context, arg db.UpsertTrinoQuerySnapshotParams) (db.TrinoQuerySnapshot, error)
	SetTrinoQueryProfile(ctx context.Context, arg db.SetTrinoQueryProfileParams) error
	InsertClusterSnapshot(ctx context.Context, arg db.InsertClusterSnapshotParams) (db.ClusterSnapshot, error)
	ListEnabledIcebergCatalogs(ctx context.Context) ([]db.ListEnabledIcebergCatalogsRow, error)
	ListTableMaintenancePolicies(ctx context.Context, catalogID pgtype.UUID) ([]db.TableMaintenancePolicy, error)
	UpsertTableHealthSnapshot(ctx context.Context, arg db.UpsertTableHealthSnapshotParams) (db.TableHealthSnapshot, error)
	GetCatalogConnectionByID(ctx context.Context, id pgtype.UUID) (db.CatalogConnection, error)
	ListManagedMaintenancePolicies(ctx context.Context, catalogID pgtype.UUID) ([]db.TableMaintenancePolicy, error)
	GetMaintenanceRun(ctx context.Context, id pgtype.UUID) (db.MaintenanceRun, error)
	GetBinding(ctx context.Context, id pgtype.UUID) (db.CatalogBinding, error)
	SetMaintenanceRunAttempts(ctx context.Context, arg db.SetMaintenanceRunAttemptsParams) error
}

// monitoring returns the GORM store.
func (a *Activities) monitoring() monitoringStore {
	s, ok := a.store.(monitoringStore)
	if !ok {
		return nil
	}
	return s
}
