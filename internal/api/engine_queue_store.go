package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/workload"
	"github.com/galileostudio/baleia/internal/workload/resourcegroups"
	"github.com/jackc/pgx/v5/pgtype"
)

// engineQueueReader reads one engine's configured capacities.
type engineQueueReader interface {
	ListEngineQueueCapacities(ctx context.Context, engineID pgtype.UUID) ([]db.EngineQueueCapacity, error)
}

// engineQueueTxStore is the save's persistence inside one transaction: the
// capacities, the engine flags and the published resource groups. db.Querier
// (through the adapter below) and *repo.Repo both implement it (#771).
type engineQueueTxStore interface {
	LockTrinoResourceGroups(ctx context.Context) error
	GetEngineByID(ctx context.Context, id pgtype.UUID) (db.Engine, error)
	ListEngines(ctx context.Context) ([]db.Engine, error)
	DeleteEngineQueueCapacities(ctx context.Context, engineID pgtype.UUID) error
	UpsertEngineQueueCapacity(ctx context.Context, arg db.UpsertEngineQueueCapacityParams) error
	SetEngineMaxRunningTotal(ctx context.Context, arg db.SetEngineMaxRunningTotalParams) (db.Engine, error)
	SetEngineTrinoResourceGroups(ctx context.Context, arg db.SetEngineTrinoResourceGroupsParams) (db.Engine, error)
	PublishTrinoResourceGroups(ctx context.Context, engineID pgtype.UUID, environment string, doc workload.ResourceGroups) (int, error)
}

// sqlEngineQueueTxStore adapts the sqlc querier to the save's store. Embedding
// db.Querier promotes its methods without declaring them again in the api
// package (the authz coverage audit reads declarations by name).
type sqlEngineQueueTxStore struct{ db.Querier }

func (s sqlEngineQueueTxStore) PublishTrinoResourceGroups(ctx context.Context, engineID pgtype.UUID, environment string, doc workload.ResourceGroups) (int, error) {
	return resourcegroups.Publish(ctx, s.Querier, engineID, environment, doc)
}

// capacityStore returns the capacity reader: GORM when configured, sqlc
// otherwise.
func (h *core) capacityStore() engineQueueReader {
	if h.store != nil {
		return repo.New(h.store)
	}
	return h.queries
}

// engineQueueTx runs fn in one transaction on the GORM store when configured,
// and on the pool otherwise.
func (h *core) engineQueueTx(ctx context.Context, fn func(engineQueueTxStore) error) error {
	if h.store != nil {
		return repo.New(h.store).TxEngineQueues(ctx, func(r *repo.Repo) error { return fn(r) })
	}
	return h.inTx(ctx, func(q db.Querier) error { return fn(sqlEngineQueueTxStore{Querier: q}) })
}
