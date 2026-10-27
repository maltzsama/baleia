package api

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/engines/manage"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestEngine — POST /engines/{id}/test (#110). Runs the checks on the
// registered instance, so it exercises exactly the adapter a query would use.
func (h *CatalogHandlers) TestEngine(ctx context.Context, request TestEngineRequestObject) (TestEngineResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return TestEngine401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	_, _, err = h.authz().Engine(ctx, p, id, authz.ActWrite)
	if errors.Is(err, authz.ErrNotFound) {
		return TestEngine404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}
	if !h.connectionTests.allow(p.UserID.String(), time.Now()) {
		return TestEngine429JSONResponse{Code: ErrorCodeRateLimited, Message: "too many tests; wait a minute"}, nil
	}
	if h.engines == nil {
		res := failedTest("engine_unavailable", "no adapter for this engine kind is available")
		return TestEngine200JSONResponse(res), nil
	}
	instance, err := h.engines.Get(ctx, id)
	if err != nil {
		res := failedTest("engine_unavailable", "the engine is disabled or has no adapter")
		return TestEngine200JSONResponse(res), nil
	}

	start := time.Now()
	checks := make([]ConnectionCheck, 0, 3)
	res := TestCatalogConnectionResult{Success: true, Checks: &checks}
	add := func(c ConnectionCheck, err error) bool {
		if err != nil {
			code := classifyTestError(err)
			c.Ok, c.ErrorCode = false, &code
			if res.Success {
				res.Success = false
				ec := TestCatalogConnectionResultErrorCode(code)
				msg := codeMessages[code]
				res.ErrorCode, res.ErrorMessage, res.Error = &ec, &msg, ptr(code+": "+msg)
			}
		} else {
			c.Ok = true
		}
		checks = append(checks, c)
		return err == nil
	}

	tctx, cancel := context.WithTimeout(ctx, enginePingTimeout)
	err = instance.Ping(tctx)
	cancel()
	if !add(ConnectionCheck{Name: "reach"}, err) {
		res.LatencyMs = int(time.Since(start).Milliseconds())
		res.Checks = &checks
		return TestEngine200JSONResponse(res), nil
	}

	actx, cancel := context.WithTimeout(ctx, checkTimeout)
	rows, err := queryengine.CollectStringsWith(actx, instance, queryengine.SessionSpec{}, "SELECT 1", 1, queryengine.Callbacks{})
	cancel()
	authCheck := ConnectionCheck{Name: "auth"}
	if err == nil && len(rows) == 1 && len(rows[0]) == 1 {
		authCheck.Detail = ptr("session opened; SELECT 1 returned " + rows[0][0])
	}
	if !add(authCheck, err) {
		res.LatencyMs = int(time.Since(start).Milliseconds())
		res.Checks = &checks
		return TestEngine200JSONResponse(res), nil
	}

	versionCheck := ConnectionCheck{Name: "version"}
	if v, ok := queryengine.As[queryengine.Versioner](instance); ok {
		vctx, vcancel := context.WithTimeout(ctx, engineVersionTimeout)
		version, verr := v.Version(vctx)
		vcancel()
		if verr == nil {
			res.Version = &version
			versionCheck.Detail = &version
		}
		versionCheck.Ok = verr == nil
		versionCheck.ErrorCode = nil
		if verr != nil {
			code := classifyTestError(verr)
			versionCheck.ErrorCode = &code
		}
		checks = append(checks, versionCheck)
	} else {
		skipped := true
		versionCheck.Skipped = &skipped
		versionCheck.Ok = true
		checks = append(checks, versionCheck)
	}
	res.LatencyMs = int(time.Since(start).Milliseconds())
	res.Checks = &checks
	return TestEngine200JSONResponse(res), nil
}

func failedTest(code, message string) TestCatalogConnectionResult {
	ec := TestCatalogConnectionResultErrorCode(code)
	return TestCatalogConnectionResult{
		Success:      false,
		ErrorCode:    &ec,
		ErrorMessage: &message,
		Error:        ptr(code + ": " + message),
		Checks:       &[]ConnectionCheck{},
	}
}

// ListEngineObservations — GET /engines/{id}/observations (#110).
func (h *CatalogHandlers) ListEngineObservations(ctx context.Context, request ListEngineObservationsRequestObject) (ListEngineObservationsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return ListEngineObservations401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, _, err := h.authz().Engine(ctx, p, id, authz.ActRead); errors.Is(err, authz.ErrNotFound) {
		return ListEngineObservations404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	} else if err != nil {
		return nil, err
	}
	rows, err := h.engineAdmin.Observations(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]EngineObservation, 0, len(rows))
	for _, row := range rows {
		out = append(out, EngineObservation{
			CatalogName: row.CatalogName,
			Connector:   row.Connector,
			Present:     row.Present,
			Mode:        row.Mode,
			FirstSeenAt: pgTimestampToTime(row.FirstSeenAt),
			ObservedAt:  pgTimestampToTime(row.ObservedAt),
		})
	}
	return ListEngineObservations200JSONResponse(out), nil
}

// GetEngineImpact — GET /engines/{id}/impact (#110).
func (h *CatalogHandlers) GetEngineImpact(ctx context.Context, request GetEngineImpactRequestObject) (GetEngineImpactResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetEngineImpact401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	if _, _, err := h.authz().Engine(ctx, p, id, authz.ActRead); errors.Is(err, authz.ErrNotFound) {
		return GetEngineImpact404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	} else if err != nil {
		return nil, err
	}
	row, err := h.engineAdmin.Impact(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetEngineImpact200JSONResponse(EngineImpact{
		ManagedBindings:       int(row.ManagedBindings),
		NativeCatalogs:        int(row.NativeCatalogs),
		Grants:                int(row.Grants),
		Metrics:               int(row.Metrics),
		Docs:                  int(row.Docs),
		SavedQueriesTargeting: int(row.SavedQueriesTargeting),
		RunningExecutions:     int(row.RunningExecutions),
	}), nil
}

// DiscoverEngineCatalogs — POST /engines/{id}/discover (#110). Runs native
// catalog discovery now and reports what changed.
func (h *CatalogHandlers) DiscoverEngineCatalogs(ctx context.Context, request DiscoverEngineCatalogsRequestObject) (DiscoverEngineCatalogsResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return DiscoverEngineCatalogs401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	eng, conn, err := h.authz().Engine(ctx, p, id, authz.ActWrite)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return DiscoverEngineCatalogs404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	case errors.Is(err, authz.ErrForbidden):
		return DiscoverEngineCatalogs403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the connection owner or an admin may discover catalogs"}}, nil
	case err != nil:
		return nil, err
	}
	if eng.Class != "direct" {
		return DiscoverEngineCatalogs422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeNotDirect, "only direct engines expose native catalogs")}, nil
	}
	provider, cfg, err := h.connectionConfig(conn)
	if err != nil {
		return DiscoverEngineCatalogs422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeEngineUnavailable, connectionTestError(err))}, nil
	}
	dctx, cancel := context.WithTimeout(ctx, enginePingTimeout)
	defer cancel()
	names, err := catalogs.NativeNames(dctx, provider, cfg, conn.Name)
	if err != nil {
		return DiscoverEngineCatalogs422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeEngineUnreachable, connectionTestError(err))}, nil
	}

	existing, err := h.topology.BindingsOfEngine(ctx, id)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]db.CatalogBinding, len(existing))
	for _, b := range existing {
		if b.Mode == "native" {
			seen[b.EngineCatalogName] = b
		}
	}

	added := make([]string, 0)
	unchanged := make([]string, 0)
	var addedIDs []pgtype.UUID
	err = h.inTx(ctx, func(q db.Querier) error {
		for _, name := range names {
			if _, ok := seen[name]; ok {
				unchanged = append(unchanged, name)
				continue
			}
			cat, err := q.UpsertNativeCatalog(ctx, db.UpsertNativeCatalogParams{ConnectionID: conn.ID, Name: name})
			if err != nil {
				return err
			}
			if _, err := q.UpsertNativeBinding(ctx, db.UpsertNativeBindingParams{CatalogID: cat.ID, EngineID: id, EngineCatalogName: name}); err != nil {
				return err
			}
			added = append(added, name)
			addedIDs = append(addedIDs, cat.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	disabled := make([]string, 0)
	present := make(map[string]struct{}, len(names))
	for _, name := range names {
		present[name] = struct{}{}
	}
	for name, b := range seen {
		if _, ok := present[name]; ok {
			continue
		}
		if err := h.topology.Disable(ctx, b.CatalogID); err != nil {
			return nil, err
		}
		disabled = append(disabled, name)
	}
	h.catalogCache.Clear()
	for _, catID := range addedIDs {
		h.startMetadataSync(ctx, catID)
	}
	return DiscoverEngineCatalogs200JSONResponse{Added: added, Disabled: disabled, Unchanged: unchanged}, nil
}

// GetReconcileRun — GET /engines/{id}/reconcile-runs/{run_id}. Admins only.
func (h *CatalogHandlers) GetReconcileRun(ctx context.Context, request GetReconcileRunRequestObject) (GetReconcileRunResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetReconcileRun401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	if err := p.RequireAdmin(); err != nil {
		return GetReconcileRun403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "administrator role required"}}, nil
	}
	row, err := h.engineAdmin.ReconcileRun(ctx, pgtype.UUID{Bytes: request.Id, Valid: true}, pgtype.UUID{Bytes: request.RunId, Valid: true})
	switch {
	case errors.Is(err, manage.ErrNotFound):
		return GetReconcileRun404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	case errors.Is(err, manage.ErrRunNotFound):
		return GetReconcileRun404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: err.Error()}}, nil
	case err != nil:
		return nil, err
	}
	run, err := reconcileRunToPublic(row)
	if err != nil {
		return nil, err
	}
	if n := h.reconcileMaxActions(); n > 0 {
		run.MaxActions = &n
	}
	return GetReconcileRun200JSONResponse(run), nil
}
