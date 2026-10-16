package bindings

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reconcile action kinds and results (#37).
const (
	ActionCreate   = "create"
	ActionRecreate = "recreate"
	ActionDrop     = "drop"
	ActionNone     = "none"

	ResultApplied  = "applied"
	ResultFailed   = "failed"
	ResultDryRun   = "skipped_dry_run"
	ResultReported = "reported"
)

// DefaultMaxActions bounds the mutating actions of one run.
const DefaultMaxActions = 20

// Action is one thing a reconcile run found, and what it did about it.
type Action struct {
	Catalog   string `json:"catalog"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
	Result    string `json:"result"`
	ErrorCode string `json:"error_code,omitempty"`
}

// Report is one reconcile run of one engine.
type Report struct {
	DryRun     bool
	Actions    []Action
	Adopted    []string // catalogs the engine has that Baleia does not manage
	StartedAt  time.Time
	FinishedAt time.Time
}

// Mutating counts the actions that changed (or would change) something.
func (r Report) Mutating() int {
	n := 0
	for _, a := range r.Actions {
		if a.Kind != ActionNone && a.Result != ResultReported {
			n++
		}
	}
	return n
}

// ReconcileStore is the slice of the database the reconciler reads.
type ReconcileStore interface {
	ListManagedBindingsOfEngine(ctx context.Context, engineID pgtype.UUID) ([]db.CatalogBinding, error)
	ObservedManagedNamesOfEngine(ctx context.Context, engineID pgtype.UUID) ([]string, error)
}

// Reconciler repairs the managed drift of one federated engine and reports
// everything else (#37). It only ever acts on catalogs Baleia manages:
// a catalog the engine has without a managed binding is reported as adopted
// and never dropped, altered or taken over. Every change goes through the
// lifecycle procedures (#38), whose guarded transitions skip a binding a
// user is changing at the same time.
type Reconciler struct {
	Lifecycle Lifecycle
	Store     ReconcileStore
	// Project builds a managed binding's current projection.
	Project func(ctx context.Context, b db.CatalogBinding) (Projection, error)
	// MaxActions bounds the mutating actions per run (0 = DefaultMaxActions);
	// the rest are reported.
	MaxActions int
	// OnAction is told about every mutating action applied (audit).
	OnAction func(ctx context.Context, a Action)
}

// Run checks every managed binding of the engine against what the engine
// reports. A dry run computes the same report without changing anything.
func (r Reconciler) Run(ctx context.Context, dryRun bool) (Report, error) {
	rep := Report{DryRun: dryRun, StartedAt: time.Now()}
	l := r.Lifecycle
	var seen []Observed
	var err error
	if dryRun {
		ectx, cancel := l.engine(ctx)
		seen, err = l.Binder.Observe(ectx, l.Engine)
		cancel()
	} else {
		// Observing also records the engine's catalogs and adopts the
		// unmanaged ones (visible, never managed).
		seen, err = l.Observe(ctx)
	}
	if err != nil {
		return rep, fmt.Errorf("observe the engine: %w", err)
	}
	reserved := l.Binder.ReservedNames()
	present := map[string]bool{}
	for _, o := range seen {
		if o.Present && !slices.Contains(reserved, o.EngineCatalog) && !strings.HasPrefix(o.EngineCatalog, probeCatalogPrefix) {
			present[o.EngineCatalog] = true
		}
	}

	managed, err := r.Store.ListManagedBindingsOfEngine(ctx, l.Engine.ID)
	if err != nil {
		return rep, err
	}
	owned := map[string]bool{}
	budget := r.MaxActions
	if budget <= 0 {
		budget = DefaultMaxActions
	}
	act := func(a Action, apply func() (db.CatalogBinding, error)) {
		switch {
		case dryRun:
			a.Result = ResultDryRun
		case budget <= 0:
			a.Result = ResultReported
		default:
			budget--
			b, err := apply()
			a.Result = ResultApplied
			if err != nil || b.LifecycleState == StateError {
				a.Result = ResultFailed
				a.ErrorCode = ErrorCode(err)
				if err == nil && b.LastErrorCode != nil {
					a.ErrorCode = *b.LastErrorCode
				}
			}
			if r.OnAction != nil {
				r.OnAction(ctx, a)
			}
		}
		rep.Actions = append(rep.Actions, a)
	}

	for _, b := range managed {
		owned[b.EngineCatalogName] = true
		name := b.EngineCatalogName
		switch b.LifecycleState {
		case StateActive, StateError:
			if !b.Enabled {
				continue
			}
			p, err := r.Project(ctx, b)
			if err != nil {
				rep.Actions = append(rep.Actions, Action{Catalog: name, Kind: ActionNone, Reason: "unprojectable", Result: ResultReported, ErrorCode: ErrorCode(err)})
				continue
			}
			if !present[name] {
				act(Action{Catalog: name, Kind: ActionCreate, Reason: "missing_in_engine"}, func() (db.CatalogBinding, error) { return l.Sync(ctx, b, p) })
				continue
			}
			c, err := l.Binder.Confirm(ctx, l.Engine, p)
			if err != nil {
				rep.Actions = append(rep.Actions, Action{Catalog: name, Kind: ActionNone, Reason: "unknown", Result: ResultReported, ErrorCode: ErrorCode(err)})
				continue
			}
			if !c.Confirmed || b.LifecycleState == StateError {
				reason := "version_mismatch"
				if c.Confirmed {
					reason = "binding_in_error"
				}
				act(Action{Catalog: name, Kind: ActionRecreate, Reason: reason}, func() (db.CatalogBinding, error) { return l.Sync(ctx, b, p) })
			}
		case StateDeprovisioning:
			kind, reason := ActionDrop, "pending_drop"
			if !present[name] {
				kind, reason = ActionNone, "already_dropped"
			}
			act(Action{Catalog: name, Kind: kind, Reason: reason}, func() (db.CatalogBinding, error) { return l.Deprovision(ctx, b) })
		}
		// provisioning and updating are in flight; disabled is done.
	}

	// Names Baleia managed that no binding owns any more (an old name a
	// rename could not drop) are still Baleia's to drop.
	leftovers, err := r.Store.ObservedManagedNamesOfEngine(ctx, l.Engine.ID)
	if err != nil {
		return rep, err
	}
	for _, name := range leftovers {
		if owned[name] || !present[name] {
			continue
		}
		owned[name] = true
		act(Action{Catalog: name, Kind: ActionDrop, Reason: "pending_drop"}, func() (db.CatalogBinding, error) { return db.CatalogBinding{}, l.drop(ctx, name) })
	}

	for name := range present {
		if !owned[name] {
			rep.Adopted = append(rep.Adopted, name)
		}
	}
	slices.Sort(rep.Adopted)
	rep.FinishedAt = time.Now()
	return rep, nil
}
