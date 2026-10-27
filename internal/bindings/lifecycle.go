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
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	ObservedManagedNamesOfEngine(ctx context.Context, engineID pgtype.UUID) ([]string, error)
	MarkEngineCatalogManaged(ctx context.Context, arg db.MarkEngineCatalogManagedParams) error
}

// Lifecycle runs the procedures on one engine's binder.
type Lifecycle struct {
	Store  LifecycleStore
	Binder Binder
	Engine EngineRef
	// ConnectionID is the engine's connection; adopted catalogs belong to it.
	ConnectionID pgtype.UUID
	// DDLTimeout bounds each call to the engine; 0 means 30s. State writes
	// never inherit it, so an engine that times out still leaves the binding
	// in error with the cause recorded.
	DDLTimeout time.Duration
	// OnDDL observes each catalog DDL the engine was asked for (action
	// create, replace or drop); nil for none (metrics.Catalog.DDL).
	OnDDL func(action string, err error)
}

func (l Lifecycle) ddl(action string, err error) {
	if l.OnDDL != nil {
		l.OnDDL(action, err)
	}
}

// stateWriteTimeout bounds a state write once the request may be gone.
const stateWriteTimeout = 10 * time.Second

// engine is the context of one call to the engine.
func (l Lifecycle) engine(ctx context.Context) (context.Context, context.CancelFunc) {
	d := l.DDLTimeout
	if d == 0 {
		d = 30 * time.Second
	}
	return context.WithTimeout(ctx, d)
}

func (l Lifecycle) transition(ctx context.Context, id pgtype.UUID, to string, from []string, cause error, rename *string) (db.CatalogBinding, error) {
	return l.transitionTo(ctx, id, to, from, cause, rename, nil)
}

// transitionTo is transition that also sets the desired version. The write
// survives the request and the engine's deadline (stateWriteTimeout).
func (l Lifecycle) transitionTo(ctx context.Context, id pgtype.UUID, to string, from []string, cause error, rename, version *string) (db.CatalogBinding, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateWriteTimeout)
	defer cancel()
	arg := db.TransitionBindingParams{ID: id, ToState: to, FromStates: from, EngineCatalogName: rename, DesiredVersion: version}
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
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateWriteTimeout)
	defer cancel()
	c, err := l.Binder.Confirm(cctx, l.Engine, p)
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
	if err := l.create(ctx, p); err != nil {
		return l.transition(ctx, b.ID, StateError, []string{StateProvisioning}, err, nil)
	}
	return l.confirm(ctx, b.ID, StateProvisioning, p)
}

// create applies a new catalog. When the engine already has one of that
// name — which Baleia may not own — nothing on the engine is touched; only
// the bookkeeping this Create wrote is undone.
func (l Lifecycle) create(ctx context.Context, p Projection) (err error) {
	ctx, span := ddlSpan(ctx, "create", p.EngineCatalog, p.Connector)
	defer endDDLSpan(span, &err)
	ectx, cancel := l.engine(ctx)
	defer cancel()
	err = l.Binder.Create(ectx, l.Engine, p)
	l.ddl("create", err)
	if errors.Is(err, ErrCatalogExists) {
		if u, ok := l.Binder.(Unstager); ok {
			uctx, ucancel := context.WithTimeout(context.WithoutCancel(ctx), stateWriteTimeout)
			defer ucancel()
			// Best effort: the create error below is what matters.
			_ = u.Unstage(uctx, l.Engine, p.EngineCatalog)
		}
	}
	return err
}

func (l Lifecycle) drop(ctx context.Context, name string) (err error) {
	ctx, span := ddlSpan(ctx, "drop", name, "")
	defer endDDLSpan(span, &err)
	ectx, cancel := l.engine(ctx)
	defer cancel()
	err = l.Binder.Drop(ectx, l.Engine, name)
	l.ddl("drop", err)
	return err
}

func (l Lifecycle) replace(ctx context.Context, p Projection) (err error) {
	ctx, span := ddlSpan(ctx, "replace", p.EngineCatalog, p.Connector)
	defer endDDLSpan(span, &err)
	ectx, cancel := l.engine(ctx)
	defer cancel()
	err = l.Binder.Replace(ectx, l.Engine, p)
	l.ddl("replace", err)
	return err
}

// Update applies a changed projection under the same name to a binding
// written in updating.
func (l Lifecycle) Update(ctx context.Context, b db.CatalogBinding, p Projection) (db.CatalogBinding, error) {
	if err := l.replace(ctx, p); err != nil {
		return l.transition(ctx, b.ID, StateError, []string{StateUpdating}, err, nil)
	}
	return l.confirm(ctx, b.ID, StateUpdating, p)
}

// Rename moves a binding written in updating under its new name: the new
// catalog is created and confirmed first, then the old one dropped, so the
// catalog never goes missing. If the new name cannot be created the binding
// keeps its old name and version, active, with the error recorded; a name
// the engine already had is left alone. An old name that cannot be dropped
// stays recorded as managed, so it is never adopted and the reconciler (#37)
// drops it.
func (l Lifecycle) Rename(ctx context.Context, b db.CatalogBinding, oldName string, oldVersion *string, p Projection) (db.CatalogBinding, error) {
	if err := l.create(ctx, p); err != nil {
		if !errors.Is(err, ErrCatalogExists) {
			_ = l.drop(ctx, p.EngineCatalog) // the new name's leftovers, if any
		}
		return l.transitionTo(ctx, b.ID, StateActive, []string{StateUpdating}, err, &oldName, oldVersion)
	}
	out, err := l.confirm(ctx, b.ID, StateUpdating, p)
	if err != nil || out.LifecycleState != StateActive {
		return out, err
	}
	if derr := l.drop(ctx, oldName); derr != nil {
		mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateWriteTimeout)
		defer cancel()
		if merr := l.Store.MarkEngineCatalogManaged(mctx, db.MarkEngineCatalogManagedParams{EngineID: l.Engine.ID, CatalogName: oldName}); merr != nil {
			return out, merr
		}
		return l.transition(ctx, b.ID, StateActive, []string{StateActive}, coded("old_name_not_dropped", fmt.Sprintf("%s is still in the engine: %v", oldName, derr)), nil)
	}
	return out, nil
}

// Sync re-applies a binding in error or active.
func (l Lifecycle) Sync(ctx context.Context, b db.CatalogBinding, p Projection) (db.CatalogBinding, error) {
	cur, err := l.transitionTo(ctx, b.ID, StateProvisioning, []string{StateError, StateActive}, nil, nil, &p.Version)
	if err != nil {
		return cur, err
	}
	if err := l.replace(ctx, p); err != nil {
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
	if err := l.drop(ctx, b.EngineCatalogName); err != nil {
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
	ectx, cancel := l.engine(ctx)
	seen, err := l.Binder.Observe(ectx, l.Engine)
	cancel()
	if err != nil {
		return nil, err
	}
	managed, err := l.Store.ManagedBindingNamesOfEngine(ctx, l.Engine.ID)
	if err != nil {
		return nil, err
	}
	leftovers, err := l.Store.ObservedManagedNamesOfEngine(ctx, l.Engine.ID)
	if err != nil {
		return nil, err
	}
	managed = append(managed, leftovers...)
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

// tracer names the catalog DDL spans (#380).
func tracer() trace.Tracer { return obs.Tracer("bindings") }

// ddlSpan starts the span of one catalog DDL: the action, the catalog and its
// connector, never the properties, which carry secrets.
func ddlSpan(ctx context.Context, action, catalog, connector string) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{
		attribute.String("baleia.binding.action", action),
		attribute.String("baleia.catalog", catalog),
	}
	if connector != "" {
		attrs = append(attrs, attribute.String("baleia.connector", connector))
	}
	return tracer().Start(ctx, "binding."+action, trace.WithAttributes(attrs...))
}

// endDDLSpan ends span, failed when *err is set.
func endDDLSpan(span trace.Span, err *error) {
	obs.FailSpan(span, *err, "catalog ddl failed")
	span.End()
}
