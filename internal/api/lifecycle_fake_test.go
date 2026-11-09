package api

import (
	"context"
	"slices"
	"sync"

	"github.com/galileostudio/baleia/internal/db"
)

// lifecycleFake is the binding-lifecycle part of the database (#38) for
// handler tests: bindings with their states, the versions written to the
// registry (which the fake plugin confirms unless unconfirmed is set), and
// observations.
type lifecycleFake struct {
	db.Querier
	lmu         sync.Mutex
	tracked     map[[16]byte]db.CatalogBinding
	versions    map[string]string
	unconfirmed bool
	observed    map[string]string
}

func (f *lifecycleFake) init() {
	if f.tracked == nil {
		f.tracked, f.versions, f.observed = map[[16]byte]db.CatalogBinding{}, map[string]string{}, map[string]string{}
	}
}

func (f *lifecycleFake) track(b db.CatalogBinding) db.CatalogBinding {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	f.init()
	f.tracked[b.ID.Bytes] = b
	return b
}

func (f *lifecycleFake) current(id db.UUID) (db.CatalogBinding, bool) {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	f.init()
	b, ok := f.tracked[id.Bytes]
	return b, ok
}

func (f *lifecycleFake) noteRegistry(name string, version *string) {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	f.init()
	if version != nil {
		f.versions[name] = *version
	}
}

func (f *lifecycleFake) TransitionBinding(ctx context.Context, a db.TransitionBindingParams) (db.CatalogBinding, error) {
	if err := ctx.Err(); err != nil {
		return db.CatalogBinding{}, err // like a real database on a dead context
	}
	f.lmu.Lock()
	defer f.lmu.Unlock()
	f.init()
	b, ok := f.tracked[a.ID.Bytes]
	if !ok || !slices.Contains(a.FromStates, b.LifecycleState) {
		return db.CatalogBinding{}, db.ErrNoRows
	}
	b.LifecycleState, b.LastErrorCode, b.LastErrorMessage = a.ToState, a.ErrorCode, a.ErrorMessage
	if a.EngineCatalogName != nil {
		b.EngineCatalogName = *a.EngineCatalogName
	}
	if a.DesiredVersion != nil {
		b.DesiredVersion = a.DesiredVersion
	}
	f.tracked[a.ID.Bytes] = b
	return b, nil
}

func (f *lifecycleFake) GetBindingStatus(_ context.Context, id db.UUID) (db.BindingStatusV, error) {
	b, ok := f.current(id)
	if !ok {
		return db.BindingStatusV{}, db.ErrNoRows
	}
	status := map[string]string{"error": "error", "deprovisioning": "disabled", "disabled": "disabled", "provisioning": "pending", "updating": "pending"}[b.LifecycleState]
	if status == "" {
		status = "synced"
		if f.unconfirmed {
			status = "pending"
		}
	}
	return db.BindingStatusV{BindingID: id, LifecycleState: b.LifecycleState, SyncStatus: status}, nil
}

func (f *lifecycleFake) GetTrinoCatalogRegistryState(_ context.Context, a db.GetTrinoCatalogRegistryStateParams) (db.GetTrinoCatalogRegistryStateRow, error) {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	f.init()
	v, ok := f.versions[a.CatalogName]
	if !ok {
		return db.GetTrinoCatalogRegistryStateRow{}, db.ErrNoRows
	}
	by := "trino"
	if f.unconfirmed {
		by = "baleia"
	}
	return db.GetTrinoCatalogRegistryStateRow{CatalogVersion: &v, Enabled: true, SyncStatus: "synced", UpdatedBy: by}, nil
}

func (f *lifecycleFake) GetManagedBindingOfCatalog(_ context.Context, a db.GetManagedBindingOfCatalogParams) (db.CatalogBinding, error) {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	f.init()
	for _, b := range f.tracked {
		if b.CatalogID == a.CatalogID && b.EngineID == a.EngineID && b.Mode == "managed" {
			return b, nil
		}
	}
	return db.CatalogBinding{}, db.ErrNoRows
}

func (f *lifecycleFake) ManagedBindingNamesOfEngine(context.Context, db.UUID) ([]string, error) {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	var out []string
	for _, b := range f.tracked {
		out = append(out, b.EngineCatalogName)
	}
	return out, nil
}

func (f *lifecycleFake) UpsertEngineCatalogObservation(_ context.Context, a db.UpsertEngineCatalogObservationParams) error {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	f.init()
	f.observed[a.CatalogName] = a.Mode
	return nil
}

func (f *lifecycleFake) MarkEngineCatalogsAbsent(context.Context, db.MarkEngineCatalogsAbsentParams) error {
	return nil
}

func (f *lifecycleFake) UpsertNativeCatalog(_ context.Context, a db.UpsertNativeCatalogParams) (db.Catalog, error) {
	return db.Catalog{ConnectionID: a.ConnectionID, Name: a.Name, Origin: "native"}, nil
}

func (f *lifecycleFake) UpsertAdoptedBinding(context.Context, db.UpsertAdoptedBindingParams) error {
	return nil
}

func (f *lifecycleFake) ObservedManagedNamesOfEngine(context.Context, db.UUID) ([]string, error) {
	return nil, nil
}

func (f *lifecycleFake) MarkEngineCatalogManaged(context.Context, db.MarkEngineCatalogManagedParams) error {
	return nil
}
