package api

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

// dataPolicyGuard says where data policies tie a catalog to the engines that
// serve it (ADR 0028): a catalog that carries policies may be bound only to
// engines that enforce them.
type dataPolicyGuard interface {
	CatalogHasPolicies(ctx context.Context, catalogID uuid.UUID) (bool, error)
	EngineServesPoliciedCatalogs(ctx context.Context, engineID uuid.UUID) (bool, error)
}

// errPoliciesUnenforced aborts a write that would leave a catalog's data
// policies on an engine that does not enforce them.
var errPoliciesUnenforced = errors.New("data policies would be unenforced")

// noDataPolicies is the guard of an API built without a store: only tests
// build one, and they have no policies.
type noDataPolicies struct{}

func (noDataPolicies) CatalogHasPolicies(context.Context, uuid.UUID) (bool, error) {
	return false, nil
}

func (noDataPolicies) EngineServesPoliciedCatalogs(context.Context, uuid.UUID) (bool, error) {
	return false, nil
}

// policiesUnenforced reports whether binding catalog to engine would leave
// the catalog's data policies unenforced.
func (h *CatalogHandlers) policiesUnenforced(ctx context.Context, catalog db.Catalog, engine db.Engine) (bool, error) {
	if engine.DataPolicies {
		return false, nil
	}
	return h.dataPolicies.CatalogHasPolicies(ctx, uuid.UUID(catalog.ID.Bytes))
}

func dataPoliciesUnenforced(msg string) UnprocessableEntityJSONResponse {
	return unprocessable(ErrorCodeDataPoliciesUnenforced, msg)
}
