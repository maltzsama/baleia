package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// The binding API (#80). Managed bindings are written here and by the catalog
// handlers; the binding a catalog was created with on its Trino cluster's
// engine (its "primary" binding) also keeps the catalog's Trino columns in
// step, which the explorer, Lens and retrievers still read.

func bindingNotFound() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "binding not found"}
}

func unprocessable(code ErrorCode, msg string) UnprocessableEntityJSONResponse {
	return UnprocessableEntityJSONResponse{Code: code, Message: msg, Errors: []struct {
		Field   *string `json:"field,omitempty"`
		Message *string `json:"message,omitempty"`
	}{{Message: &msg}}}
}

// unprocessableFields is the code-keyed variant of unprocessable (#67): the UI
// translates each FieldError code next to its input instead of showing msg.
func unprocessableFields(msg string, fields ...FieldError) UnprocessableEntityJSONResponse {
	out := unprocessable(ErrorCodeValidationError, msg)
	out.Fields = &fields
	return out
}

func extraPropsOf(logger *slog.Logger, raw []byte) map[string]string {
	out := map[string]string{}
	decodeStored(logger, raw, &out, "extra_props")
	return out
}

// bindingTarget is everything a write on one binding needs.
type bindingTarget struct {
	catalog db.Catalog
	conn    db.CatalogConnection // the catalog's source connection
	engine  db.Engine
	cluster db.TrinoCluster
}

// primary reports whether b is the binding the catalog's Trino columns
// describe.
func (t bindingTarget) primary() bool { return t.catalog.ClusterID == t.cluster.ID }

// bindingAccess resolves a catalog and an engine for a binding write: write
// access to both, a source catalog and an engine with a binder.
func (h *CatalogHandlers) bindingAccess(ctx context.Context, p authz.Principal, catalogID, engineID pgtype.UUID) (bindingTarget, *notFoundOrForbidden, *UnprocessableEntityJSONResponse, error) {
	cat, conn, err := h.authz().Catalog(ctx, p, catalogID, authz.ActWrite)
	if nf, fb := catalogAccessError(err); nf || fb {
		return bindingTarget{}, &notFoundOrForbidden{notFound: nf, what: "catalog"}, nil, nil
	} else if err != nil {
		return bindingTarget{}, nil, nil, err
	}
	eng, _, err := h.authz().Engine(ctx, p, engineID, authz.ActWrite)
	if nf, fb := catalogAccessError(err); nf || fb {
		return bindingTarget{}, &notFoundOrForbidden{notFound: nf, what: "engine"}, nil, nil
	} else if err != nil {
		return bindingTarget{}, nil, nil, err
	}
	if cat.Origin != "source" {
		u := unprocessable(ErrorCodeBindingUnsupported, "native catalogs belong to their engine and are not bound")
		return bindingTarget{}, nil, &u, nil
	}
	if eng.Kind != h.binder.EngineKind() {
		u := unprocessable(ErrorCodeBindingUnsupported, "engine kind "+eng.Kind+" has no binder")
		return bindingTarget{}, nil, &u, nil
	}
	cluster, err := h.topology.ClusterOfEngine(ctx, eng.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		u := unprocessable(ErrorCodeBindingUnsupported, "the engine has no Trino cluster")
		return bindingTarget{}, nil, &u, nil
	}
	if err != nil {
		return bindingTarget{}, nil, nil, err
	}
	return bindingTarget{catalog: cat, conn: conn, engine: eng, cluster: cluster}, nil, nil, nil
}

type notFoundOrForbidden struct {
	notFound bool
	what     string
}

func (e *notFoundOrForbidden) notFoundResponse() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: e.what + " not found"}
}

func (e *notFoundOrForbidden) forbiddenResponse() ForbiddenJSONResponse {
	return ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to bind this " + e.what}
}

// bindingConflict maps a unique violation of catalog_bindings to a 409.
func bindingConflict(err error) (ConflictJSONResponse, bool) {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		return ConflictJSONResponse{}, false
	}
	if pg.ConstraintName == "catalog_bindings_catalog_id_engine_id_key" {
		return ConflictJSONResponse{Code: ErrorCodeBindingExists, Message: "the catalog is already bound to this engine"}, true
	}
	return ConflictJSONResponse{Code: ErrorCodeNameInUse, Message: "the engine already has a catalog of that name"}, true
}

// syncPrimaryCatalog keeps the catalog's Trino columns in step with its
// primary binding.
func syncPrimaryCatalog(ctx context.Context, q primaryCatalogStore, t bindingTarget, b db.CatalogBinding) error {
	if !t.primary() {
		return nil
	}
	name := b.EngineCatalogName
	updated, err := q.UpdateCatalog(ctx, db.UpdateCatalogParams{
		ID: t.catalog.ID, Name: t.catalog.Name, Description: t.catalog.Description, Warehouse: t.catalog.Warehouse,
		Connector: b.Connector, ExtraProps: b.ExtraProps, TrinoCatalogName: &name,
	})
	if err != nil {
		return err
	}
	if updated.Enabled != b.Enabled {
		return q.SetCatalogEnabled(ctx, db.SetCatalogEnabledParams{ID: t.catalog.ID, Enabled: b.Enabled})
	}
	return nil
}

func (h *core) bindingSpec(t bindingTarget, name, connector string, extra map[string]string) bindings.BindingSpec {
	ref := ""
	if t.catalog.SourceRef != nil {
		ref = *t.catalog.SourceRef
	}
	return bindings.BindingSpec{EngineCatalog: name, Connector: connector, SourceRef: ref, ExtraProps: extra}
}

func (h *CatalogHandlers) CreateCatalogBinding(ctx context.Context, request CreateCatalogBindingRequestObject) (CreateCatalogBindingResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return CreateCatalogBinding401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	ev := audit.Event{Action: audit.BindingCreate, ResourceType: "binding"}
	ctx = audit.WithAction(ctx, ev.Action)
	if request.Body == nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	body := *request.Body
	t, denied, invalid, err := h.bindingAccess(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, pgtype.UUID{Bytes: body.EngineId, Valid: true})
	switch {
	case err != nil:
		return nil, err
	case denied != nil && denied.notFound:
		return CreateCatalogBinding404JSONResponse{NotFoundJSONResponse: denied.notFoundResponse()}, nil
	case denied != nil:
		return CreateCatalogBinding403JSONResponse{ForbiddenJSONResponse: denied.forbiddenResponse()}, nil
	case invalid != nil:
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: *invalid}, nil
	}
	connector, extra := "", map[string]string{}
	if body.Connector != nil {
		connector = *body.Connector
	}
	if body.ExtraProps != nil {
		extra = *body.ExtraProps
	}
	proj, err := h.projectBinding(ctx, t.conn, t.engine.ID, t.catalog.ID, h.bindingSpec(t, body.EngineCatalogName, connector, extra))
	if bindingInvalid(err) {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	if err != nil {
		return nil, err
	}
	rawExtra, _ := json.Marshal(extra)

	var created db.CatalogBinding
	txErr := h.inTx(ctx, func(qtx db.Querier) error {
		b, err := qtx.CreateManagedBinding(ctx, db.CreateManagedBindingParams{
			CatalogID: t.catalog.ID, EngineID: t.engine.ID, EngineCatalogName: proj.EngineCatalog,
			Connector: &proj.Connector, ExtraProps: rawExtra, DesiredVersion: &proj.Version,
		})
		if err != nil {
			return err
		}
		created = b
		return syncPrimaryCatalog(ctx, qtx, t, b)
	})
	if c, ok := bindingConflict(txErr); ok {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return CreateCatalogBinding409JSONResponse{ConflictJSONResponse: c}, nil
	}
	if txErr != nil {
		return nil, fmt.Errorf("create binding: %w", txErr)
	}
	l := h.lifecycleOn(t.cluster, t.engine)
	created, err = l.Provision(ctx, created, proj)
	if errors.Is(err, bindings.ErrConcurrentChange) {
		h.audited(ctx, withID(ev, created.ID).Failed(audit.ReasonConflict))
		return CreateCatalogBinding409JSONResponse{ConflictJSONResponse: concurrentChange(created)}, nil
	}
	if err != nil {
		return nil, err
	}
	h.observe(ctx, l)
	h.catalogCache.Clear()
	h.audited(ctx, withID(ev, created.ID).Allowed())
	return CreateCatalogBinding201JSONResponse(h.bindingResponse(ctx, created, proj.Version)), nil
}

// managedBinding loads a binding for a write: it must exist, be managed and
// the caller must be allowed to write its catalog and engine.
func (h *CatalogHandlers) managedBinding(ctx context.Context, p authz.Principal, id pgtype.UUID) (db.CatalogBinding, bindingTarget, *notFoundOrForbidden, *UnprocessableEntityJSONResponse, error) {
	b, err := h.topology.Binding(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.CatalogBinding{}, bindingTarget{}, &notFoundOrForbidden{notFound: true, what: "binding"}, nil, nil
	}
	if err != nil {
		return db.CatalogBinding{}, bindingTarget{}, nil, nil, err
	}
	t, denied, invalid, err := h.bindingAccess(ctx, p, b.CatalogID, b.EngineID)
	if denied != nil && denied.notFound {
		denied.what = "binding" // never reveal which half was hidden
	}
	if err != nil || denied != nil || invalid != nil {
		return b, t, denied, invalid, err
	}
	if b.Mode != "managed" {
		u := unprocessable(ErrorCodeBindingNotManaged, "only managed bindings are changed through Baleia; this one is "+b.Mode)
		return b, t, nil, &u, nil
	}
	return b, t, nil, nil, nil
}

func (h *CatalogHandlers) UpdateCatalogBinding(ctx context.Context, request UpdateCatalogBindingRequestObject) (UpdateCatalogBindingResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return UpdateCatalogBinding401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	ev := audit.Event{Action: audit.BindingUpdate, ResourceType: "binding", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	ctx = audit.WithAction(ctx, ev.Action)
	cur, t, denied, invalid, err := h.managedBinding(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true})
	switch {
	case err != nil:
		return nil, err
	case denied != nil && denied.notFound:
		return UpdateCatalogBinding404JSONResponse{NotFoundJSONResponse: bindingNotFound()}, nil
	case denied != nil:
		return UpdateCatalogBinding403JSONResponse{ForbiddenJSONResponse: denied.forbiddenResponse()}, nil
	case invalid != nil:
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: *invalid}, nil
	}
	if request.Body == nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "missing body")}, nil
	}
	body := *request.Body
	name, extra, enabled := cur.EngineCatalogName, extraPropsOf(h.logger, cur.ExtraProps), cur.Enabled
	if body.EngineCatalogName != nil {
		name = *body.EngineCatalogName
	}
	if body.ExtraProps != nil {
		extra = *body.ExtraProps
	}
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	connector := ""
	if cur.Connector != nil {
		connector = *cur.Connector
	}
	proj, err := h.projectBinding(ctx, t.conn, t.engine.ID, t.catalog.ID, h.bindingSpec(t, name, connector, extra))
	if bindingInvalid(err) {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	if err != nil {
		return nil, err
	}
	rawExtra, _ := json.Marshal(extra)

	// The state the change enters, and the states it may start from.
	state, from := bindings.StateUpdating, []string{bindings.StateActive, bindings.StateError}
	switch {
	case !enabled && cur.Enabled:
		state, from = bindings.StateDeprovisioning, []string{bindings.StateActive, bindings.StateError, bindings.StateProvisioning, bindings.StateUpdating}
	case !enabled:
		state, from = cur.LifecycleState, []string{bindings.StateDisabled, bindings.StateDeprovisioning}
	case !cur.Enabled:
		state, from = bindings.StateProvisioning, []string{bindings.StateDisabled, bindings.StateDeprovisioning}
	}
	var updated db.CatalogBinding
	txErr := h.inTx(ctx, func(qtx db.Querier) error {
		b, err := qtx.UpdateBinding(ctx, db.UpdateBindingParams{
			ID: cur.ID, EngineCatalogName: name, Connector: &proj.Connector, ExtraProps: rawExtra, Enabled: enabled,
			LifecycleState: state, DesiredVersion: &proj.Version, FromStates: from,
		})
		if err != nil {
			return err
		}
		updated = b
		return syncPrimaryCatalog(ctx, qtx, t, b)
	})
	if errors.Is(txErr, pgx.ErrNoRows) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return UpdateCatalogBinding409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
			Code: ErrorCodeConcurrentChange, Message: "the binding is " + cur.LifecycleState + "; retry when it settles",
		}}, nil
	}
	if c, ok := bindingConflict(txErr); ok {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return UpdateCatalogBinding409JSONResponse{ConflictJSONResponse: c}, nil
	}
	if txErr != nil {
		return nil, fmt.Errorf("update binding: %w", txErr)
	}

	l := h.lifecycleOn(t.cluster, t.engine)
	switch {
	case !enabled && cur.Enabled:
		updated, err = l.Deprovision(ctx, updated)
	case !enabled:
		// stays disabled: nothing on the engine
	case !cur.Enabled:
		updated, err = l.Provision(ctx, updated, proj)
	case name != cur.EngineCatalogName:
		updated, err = l.Rename(ctx, updated, cur.EngineCatalogName, cur.DesiredVersion, proj)
		if err == nil && updated.EngineCatalogName == cur.EngineCatalogName && t.primary() {
			err = syncPrimaryCatalog(ctx, h.stores.primary, t, updated) // the rename was reverted
		}
	default:
		updated, err = l.Update(ctx, updated, proj)
	}
	if errors.Is(err, bindings.ErrConcurrentChange) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return UpdateCatalogBinding409JSONResponse{ConflictJSONResponse: concurrentChange(cur)}, nil
	}
	if err != nil {
		return nil, err
	}
	h.observe(ctx, l)
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return UpdateCatalogBinding200JSONResponse(h.bindingResponse(ctx, updated, proj.Version)), nil
}

func (h *CatalogHandlers) DeleteCatalogBinding(ctx context.Context, request DeleteCatalogBindingRequestObject) (DeleteCatalogBindingResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DeleteCatalogBinding401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	ev := audit.Event{Action: audit.BindingDelete, ResourceType: "binding", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	ctx = audit.WithAction(ctx, ev.Action)
	cur, t, denied, invalid, err := h.managedBinding(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true})
	switch {
	case err != nil:
		return nil, err
	case denied != nil && denied.notFound:
		return DeleteCatalogBinding404JSONResponse{NotFoundJSONResponse: bindingNotFound()}, nil
	case denied != nil:
		return DeleteCatalogBinding403JSONResponse{ForbiddenJSONResponse: denied.forbiddenResponse()}, nil
	case invalid != nil:
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return DeleteCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: *invalid}, nil
	}
	if t.primary() {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteCatalogBinding409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
			Code: ErrorCodeBindingIsPrimary, Message: "this is the catalog's own binding on its Trino cluster; delete the catalog instead",
		}}, nil
	}
	l := h.lifecycleOn(t.cluster, t.engine)
	gone := cur
	if cur.LifecycleState != bindings.StateDisabled {
		gone, err = l.Deprovision(ctx, cur)
	}
	if errors.Is(err, bindings.ErrConcurrentChange) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteCatalogBinding409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConcurrentChange, Message: "the binding changed; retry"}}, nil
	}
	if err != nil {
		return nil, err
	}
	h.observe(ctx, l)
	if gone.LifecycleState != bindings.StateDisabled {
		msg := "the engine did not drop the catalog; the binding stays deprovisioning and is retried"
		if gone.LastErrorMessage != nil {
			msg += ": " + *gone.LastErrorMessage
		}
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteCatalogBinding409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeDropFailed, Message: msg}}, nil
	}
	if err := h.topology.DeleteBinding(ctx, cur.ID); err != nil {
		return nil, err
	}
	h.catalogCache.Clear()
	h.audited(ctx, ev.Allowed())
	return DeleteCatalogBinding204Response{}, nil
}

func (h *CatalogHandlers) SyncCatalogBinding(ctx context.Context, request SyncCatalogBindingRequestObject) (SyncCatalogBindingResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return SyncCatalogBinding401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	ev := audit.Event{Action: audit.BindingSync, ResourceType: "binding", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	ctx = audit.WithAction(ctx, ev.Action)
	cur, t, denied, invalid, err := h.managedBinding(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true})
	switch {
	case err != nil:
		return nil, err
	case denied != nil && denied.notFound:
		return SyncCatalogBinding404JSONResponse{NotFoundJSONResponse: bindingNotFound()}, nil
	case denied != nil:
		return SyncCatalogBinding403JSONResponse{ForbiddenJSONResponse: denied.forbiddenResponse()}, nil
	case invalid != nil:
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return SyncCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: *invalid}, nil
	}
	if !cur.Enabled {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return SyncCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeBindingDisabled, "enable the binding to apply it")}, nil
	}
	connector := ""
	if cur.Connector != nil {
		connector = *cur.Connector
	}
	proj, err := h.projectBinding(ctx, t.conn, t.engine.ID, t.catalog.ID, h.bindingSpec(t, cur.EngineCatalogName, connector, extraPropsOf(h.logger, cur.ExtraProps)))
	if bindingInvalid(err) {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return SyncCatalogBinding422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)}, nil
	}
	if err != nil {
		out := h.bindingResponse(ctx, cur, "")
		msg, status := err.Error(), CatalogBindingSyncStatusError
		out.LastErrorMessage, out.SyncStatus = &msg, &status
		h.audited(ctx, ev.Failed(audit.ReasonEngineError))
		return SyncCatalogBinding409JSONResponse(out), nil
	}
	l := h.lifecycleOn(t.cluster, t.engine)
	synced, err := l.Sync(ctx, cur, proj)
	if errors.Is(err, bindings.ErrConcurrentChange) {
		out := h.bindingResponse(ctx, cur, "")
		msg := "the binding is " + cur.LifecycleState + "; it cannot be synced now"
		out.LastErrorMessage = &msg
		h.audited(ctx, ev.Failed(audit.ReasonEngineError))
		return SyncCatalogBinding409JSONResponse(out), nil
	}
	if err != nil {
		return nil, err
	}
	h.observe(ctx, l)
	out := h.bindingResponse(ctx, synced, proj.Version)
	if synced.LifecycleState != bindings.StateActive {
		h.audited(ctx, ev.Failed(audit.ReasonEngineError))
		return SyncCatalogBinding409JSONResponse(out), nil
	}
	h.audited(ctx, ev.Allowed())
	return SyncCatalogBinding200JSONResponse(out), nil
}

func (h *CatalogHandlers) GetEngineBindingSupport(ctx context.Context, request GetEngineBindingSupportRequestObject) (GetEngineBindingSupportResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetEngineBindingSupport401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	eng, _, err := h.authz().Engine(ctx, p, pgtype.UUID{Bytes: request.Id, Valid: true}, authz.ActRead)
	if errors.Is(err, authz.ErrNotFound) {
		return GetEngineBindingSupport404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}
	out := GetEngineBindingSupport200JSONResponse{}
	if h.binder == nil || eng.Kind != h.binder.EngineKind() {
		return out, nil
	}
	protected, reserved := h.binder.ProtectedKeyPatterns(), h.binder.ReservedNames()
	kinds := make([]connections.Kind, 0)
	for k := range h.binder.Supports() {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	readOnly := h.binder.ReadOnlySources()
	for _, k := range kinds {
		out[string(k)] = BindingSupport{Connectors: h.binder.Supports()[k], ProtectedKeyPatterns: protected, ReservedNames: reserved, WritesSupported: !readOnly[k]}
	}
	return out, nil
}
