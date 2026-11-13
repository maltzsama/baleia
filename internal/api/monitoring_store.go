package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
)

// opsReads are the operational reads and writes with a GORM path (#773):
// table health, maintenance parameters and runs, bindings for maintenance,
// the Iceberg catalog list and query classes. *repo.Repo implements it.
type opsReads interface {
	GetTableHealthByTable(ctx context.Context, arg db.GetTableHealthByTableParams) (db.TableHealthSnapshot, error)
	ListTableMaintenancePolicies(ctx context.Context, catalogID db.UUID) ([]db.TableMaintenancePolicy, error)
	ListLastMaintenancePerOperation(ctx context.Context, arg db.ListLastMaintenancePerOperationParams) ([]db.ListLastMaintenancePerOperationRow, error)
	ListEnabledIcebergCatalogs(ctx context.Context, kinds []string) ([]db.ListEnabledIcebergCatalogsRow, error)
	ListBindingsByCatalog(ctx context.Context, catalogID db.UUID) ([]db.CatalogBinding, error)
	ListQueryClasses(ctx context.Context, arg db.ListQueryClassesParams) ([]db.ListQueryClassesRow, error)
	CreatePlannedMaintenanceRun(ctx context.Context, arg db.CreatePlannedMaintenanceRunParams) (db.MaintenanceRun, error)
}

// workloadRuleStore is the ordered workload-rule persistence (#539, #773).
// *repo.Repo implements it.
type workloadRuleStore interface {
	ListWorkloadRules(ctx context.Context) ([]db.WorkloadRule, error)
	GetWorkloadRule(ctx context.Context, id db.UUID) (db.WorkloadRule, error)
	MaxWorkloadRulePosition(ctx context.Context) (int32, error)
	CreateWorkloadRule(ctx context.Context, arg db.CreateWorkloadRuleParams) (db.WorkloadRule, error)
	UpdateWorkloadRule(ctx context.Context, arg db.UpdateWorkloadRuleParams) (db.WorkloadRule, error)
	DeleteWorkloadRule(ctx context.Context, id db.UUID) (int64, error)
}

// opsStore selects the GORM store when it is configured, sqlc otherwise.
func (h *core) opsStore() opsReads {
	if h.store != nil {
		return repo.New(h.store)
	}
	return h.queries
}

// workloadRulesStore selects the GORM store when it is configured, sqlc
// otherwise.
func (h *core) workloadRulesStore() workloadRuleStore {
	if h.store != nil {
		return repo.New(h.store)
	}
	return h.queries
}
