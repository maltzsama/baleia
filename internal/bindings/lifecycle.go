package bindings

// The binding lifecycle (#38): catalog_bindings holds the desired state and
// a lifecycle state Baleia owns; the engine's binder applies it; the
// effective state is the binder's confirmation and the observed state is
// what the engine reports (engine_catalog_observations).
//
//	            create ok             applied + confirmed
//	 (none) ─────────────▶ provisioning ────────────────▶ active
//	                            │ fails                     │ update/rename
//	                            ▼                           ▼
//	                          error ◀──── fails ──────── updating ── ok ──▶ active
//	                            │ sync
//	                            └──────────▶ provisioning
//	 any ──delete/disable──▶ deprovisioning ──dropped──▶ disabled
//	                              │ drop fails: stays, with the error (#37 retries)
//
// Every transition is guarded by the states it may start from; a guard that
// matches no row is a concurrent change (ErrConcurrentChange, 409).

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Lifecycle states.
const (
	StateProvisioning   = "provisioning"
	StateActive         = "active"
	StateUpdating       = "updating"
	StateError          = "error"
	StateDeprovisioning = "deprovisioning"
	StateDisabled       = "disabled"
)

// ErrConcurrentChange means the binding left the state a procedure expected.
var ErrConcurrentChange = errors.New("concurrent_change")

// ErrorCode is the machine-readable code of an engine error: its Code() when
// it has one, else engine_error.
func ErrorCode(err error) string {
	var coded interface{ Code() string }
	if errors.As(err, &coded) && coded.Code() != "" {
		return coded.Code()
	}
	return "engine_error"
}

// LifecycleStore is the part of the database the procedures use.
type LifecycleStore interface {
	TransitionBinding(ctx context.Context, arg db.TransitionBindingParams) (db.CatalogBinding, error)
	ManagedBindingNamesOfEngine(ctx context.Context, engineID pgtype.UUID) ([]string, error)
	UpsertEngineCatalogObservation(ctx context.Context, arg db.UpsertEngineCatalogObservationParams) error
	MarkEngineCatalogsAbsent(ctx context.Context, arg db.MarkEngineCatalogsAbsentParams) error
	UpsertNativeCatalog(ctx context.Context, arg db.UpsertNativeCatalogParams) (db.Catalog, error)
	UpsertAdoptedBinding(ctx context.Context, arg db.UpsertAdoptedBindingParams) error
}

// Lifecycle runs the procedures on one engine's binder.
type Lifecycle struct {
	Store  LifecycleStore
	Binder Binder
	Engine EngineRef
	// ConnectionID is the engine's connection; adopted catalogs belong to it.
	ConnectionID pgtype.UUID
}

func (l Lifecycle) transition(ctx context.Context, id pgtype.UUID, to string, from []string, cause error, rename *string) (db.CatalogBinding, error) {
	arg := db.TransitionBindingParams{ID: id, ToState: to, FromStates: from, EngineCatalogName: rename}
	if cause != nil {
		code, msg := ErrorCode(cause), cause.Error()
		arg.ErrorCode, arg.ErrorMessage = &code, &msg
	}
	b, err := l.Store.TransitionBinding(ctx, arg)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.CatalogBinding{}, ErrConcurrentChange
	}
	return b, err
}

// codedError is a lifecycle failure with its own code.
type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }
func (e *codedError) Code() string  { return e.code }

func coded(code, msg string) error { return &codedError{code: code, err: errors.New(msg)} }

// confirm moves a binding that the engine applied to active, or to error
// when the binder cannot confirm it.
func (l Lifecycle) confirm(ctx context.Context, id pgtype.UUID, from string, p Projection) (db.CatalogBinding, error) {
	c, err := l.Binder.Confirm(ctx, l.Engine, p)
	if err != nil {
		return l.transition(ctx, id, StateError, []string{from}, err, nil)
	}
	if !c.Confirmed {
		return l.transition(ctx, id, StateError, []string{from}, coded(c.Reason, "the engine did not confirm the catalog ("+c.Reason+")"), nil)
	}
	return l.transition(ctx, id, StateActive, []string{from}, nil, nil)
}

// Provision applies a binding written in provisioning (create, enable).
func (l Lifecycle) Provision(ctx context.Context, b db.CatalogBinding, p Projection) (db.CatalogBinding, error) {
	if err := l.Binder.Create(ctx, l.Engine, p); err != nil {
		return l.transition(ctx, b.ID, StateError, []string{StateProvisioning}, err, nil)
	}
	return l.confirm(ctx, b.ID, StateProvisioning, p)
}

// Update applies a changed projection under the same name to a binding
// written in updating.
func (l Lifecycle) Update(ctx context.Context, b db.CatalogBinding, p Projection) (db.CatalogBinding, error) {
	if err := l.Binder.Replace(ctx, l.Engine, p); err != nil {
		return l.transition(ctx, b.ID, StateError, []string{StateUpdating}, err, nil)
	}
	return l.confirm(ctx, b.ID, StateUpdating, p)
}

// Rename moves a binding written in updating under its new name: the new
// catalog is created and confirmed first, then the old one dropped, so the
// catalog never goes missing. If the new name cannot be created the binding
// keeps its old name and state, with the error recorded.
func (l Lifecycle) Rename(ctx context.Context, b db.CatalogBinding, oldName string, p Projection) (db.CatalogBinding, error) {
	if err := l.Binder.Create(ctx, l.Engine, p); err != nil {
		_ = l.Binder.Drop(ctx, l.Engine, p.EngineCatalog) // the new name's leftovers, if any
		return l.transition(ctx, b.ID, StateActive, []string{StateUpdating}, err, &oldName)
	}
	out, err := l.confirm(ctx, b.ID, StateUpdating, p)
	if err != nil || out.LifecycleState != StateActive {
		return out, err
	}
	if derr := l.Binder.Drop(ctx, l.Engine, oldName); derr != nil {
		// The binding is active under its new name; the old catalog is left
		// for the reconciler (#37) and the error says so.
		return l.transition(ctx, b.ID, StateActive, []string{StateActive}, coded("old_name_not_dropped", fmt.Sprintf("%s is still in the engine: %v", oldName, derr)), nil)
	}
	return out, nil
}

// Sync re-applies a binding in error or active.
func (l Lifecycle) Sync(ctx context.Context, b db.CatalogBinding, p Projection) (db.CatalogBinding, error) {
	cur, err := l.transition(ctx, b.ID, StateProvisioning, []string{StateError, StateActive}, nil, nil)
	if err != nil {
		return cur, err
	}
	if err := l.Binder.Replace(ctx, l.Engine, p); err != nil {
		return l.transition(ctx, b.ID, StateError, []string{StateProvisioning}, err, nil)
	}
	return l.confirm(ctx, b.ID, StateProvisioning, p)
}

// Deprovision drops a binding's catalog from the engine (delete, disable).
// A failed drop leaves it in deprovisioning with the error, for the
// reconciler to retry.
func (l Lifecycle) Deprovision(ctx context.Context, b db.CatalogBinding) (db.CatalogBinding, error) {
	cur, err := l.transition(ctx, b.ID, StateDeprovisioning,
		[]string{StateProvisioning, StateActive, StateUpdating, StateError, StateDeprovisioning}, nil, nil)
	if err != nil {
		return cur, err
	}
	if err := l.Binder.Drop(ctx, l.Engine, b.EngineCatalogName); err != nil {
		return l.transition(ctx, b.ID, StateDeprovisioning, []string{StateDeprovisioning}, err, nil)
	}
	return l.transition(ctx, b.ID, StateDisabled, []string{StateDeprovisioning}, nil, nil)
}

// probeCatalogPrefix names the throwaway catalogs connection tests create;
// they are never adopted.
const probeCatalogPrefix = "baleia_test_"

// Observe records what the engine reports: every catalog it has, managed
// when a managed binding owns the name, else adopted (a native catalog of
// the engine's connection with an adopted binding); catalogs it no longer
// reports are marked absent. Reserved (built-in) names are skipped.
func (l Lifecycle) Observe(ctx context.Context) ([]Observed, error) {
	seen, err := l.Binder.Observe(ctx, l.Engine)
	if err != nil {
		return nil, err
	}
	managed, err := l.Store.ManagedBindingNamesOfEngine(ctx, l.Engine.ID)
	if err != nil {
		return nil, err
	}
	reserved := l.Binder.ReservedNames()
	present := []string{}
	for _, o := range seen {
		if !o.Present || slices.Contains(reserved, o.EngineCatalog) || strings.HasPrefix(o.EngineCatalog, probeCatalogPrefix) {
			continue
		}
		present = append(present, o.EngineCatalog)
		mode := "adopted"
		if slices.Contains(managed, o.EngineCatalog) {
			mode = "managed"
		}
		var connector *string
		if o.Connector != "" {
			connector = &o.Connector
		}
		if err := l.Store.UpsertEngineCatalogObservation(ctx, db.UpsertEngineCatalogObservationParams{
			EngineID: l.Engine.ID, CatalogName: o.EngineCatalog, Mode: mode, Connector: connector,
		}); err != nil {
			return nil, err
		}
		if mode == "adopted" && l.ConnectionID.Valid {
			if err := l.adopt(ctx, o.EngineCatalog, connector); err != nil {
				return nil, err
			}
		}
	}
	if err := l.Store.MarkEngineCatalogsAbsent(ctx, db.MarkEngineCatalogsAbsentParams{EngineID: l.Engine.ID, PresentNames: present}); err != nil {
		return nil, err
	}
	return seen, nil
}

// adopt records a catalog Baleia did not create as a native catalog of the
// engine's connection with an adopted binding (#13): visible, never managed.
func (l Lifecycle) adopt(ctx context.Context, name string, connector *string) error {
	cat, err := l.Store.UpsertNativeCatalog(ctx, db.UpsertNativeCatalogParams{ConnectionID: l.ConnectionID, Name: name})
	if err != nil {
		return err
	}
	return l.Store.UpsertAdoptedBinding(ctx, db.UpsertAdoptedBindingParams{
		CatalogID: cat.ID, EngineID: l.Engine.ID, EngineCatalogName: name, Connector: connector,
	})
}
