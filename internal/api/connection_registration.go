package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/catalogs/conns"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/engines/manage"
	"github.com/galileostudio/baleia/internal/store/repo"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Connection registration (ADR 0029): POST /catalog-connections owns the whole
// initial registration of a data source. The connection's access mode decides
// what is created with it:
//
//   - direct: the connection, its native engine, the native catalogs and their
//     bindings, in one database transaction. Reachability and discovery run
//     before the transaction opens, and the metadata sync is started after the
//     commit.
//   - federated: the connection, its logical catalogs and their bindings on the
//     selected engines, in one database transaction. The engines apply the
//     catalogs after the commit, through the binding lifecycle, and the
//     response reports what each engine did. A commit is not an activation.

// registrationStore is the persistence a registration writes in one
// transaction: the connection, a direct engine with its native catalogs, and
// logical catalogs with their bindings and grants. *repo.Repo implements it
// over the GORM store, db.Querier over the pool.
type registrationStore interface {
	catalogWriteStore
	manage.TxStore
	CreateCatalogConnection(ctx context.Context, arg db.CreateCatalogConnectionParams) (db.CatalogConnection, error)
	CreateManagedBinding(ctx context.Context, arg db.CreateManagedBindingParams) (db.CatalogBinding, error)
}

// inRegistrationTx runs fn in one transaction, on the GORM store when it is
// configured and on the pool otherwise.
func (h *core) inRegistrationTx(ctx context.Context, fn func(registrationStore) error) error {
	if h.store != nil {
		return repo.New(h.store).TxCatalogWrite(ctx, func(r *repo.Repo) error { return fn(r) })
	}
	return h.inTx(ctx, func(q db.Querier) error { return fn(q) })
}

// registration is a validated create request, ready to be persisted.
type registration struct {
	principal authz.Principal
	event     audit.Event
	body      *CreateCatalogConnectionRequest
	config    connections.Config
	provider  connections.Provider
	params    db.CreateCatalogConnectionParams
}

// catalogOutput is one logical catalog a connection exposes: its name, the
// name it has on the engine, and the connector the registry assigns it.
type catalogOutput struct{ name, engineName, connector string }

// planCatalogOutputs checks the requested catalogs against the connectors the
// connection type declares (one request output per connector, in registry
// order) and returns them in provisioning order: the hive connector last,
// because its table redirection (#86) names only siblings that are already
// active. A refused request is returned as the 422 to send.
func planCatalogOutputs(connectors []string, body CreateCatalogRequest) ([]catalogOutput, *UnprocessableEntityJSONResponse) {
	refuse := func(r UnprocessableEntityJSONResponse) ([]catalogOutput, *UnprocessableEntityJSONResponse) {
		return nil, &r
	}
	if len(body.Catalogs) == 0 {
		return refuse(unprocessable(ErrorCodeValidationError, "catalogs are required"))
	}
	if len(connectors) != len(body.Catalogs) {
		return refuse(unprocessable(ErrorCodeValidationError, fmt.Sprintf("this connection type exposes %d catalog outputs", len(connectors))))
	}
	seenName := map[string]bool{}
	seenEngine := map[string]bool{}
	for _, c := range body.Catalogs {
		switch {
		case c.Name == "":
			return refuse(unprocessableFields("name is required", FieldError{Field: "name", Code: FieldCodeRequired}))
		case seenName[c.Name]:
			return refuse(unprocessableFields("catalog names must be unique", FieldError{Field: "name", Code: FieldCodeInvalidFormat}))
		case !validEngineCatalogName(c.TrinoCatalogName):
			return refuse(unprocessableFields(
				"trino_catalog_name must match ^[a-z][a-z0-9_]{0,62}$",
				FieldError{Field: "trino_catalog_name", Code: FieldCodeInvalidFormat},
			))
		case seenEngine[c.TrinoCatalogName]:
			return refuse(unprocessableFields("trino catalog names must be unique", FieldError{Field: "trino_catalog_name", Code: FieldCodeInvalidFormat}))
		}
		seenName[c.Name] = true
		seenEngine[c.TrinoCatalogName] = true
	}
	ordered := make([]catalogOutput, 0, len(connectors))
	for _, hive := range []bool{false, true} {
		for i, connector := range connectors {
			if (connector == "hive") == hive {
				ordered = append(ordered, catalogOutput{body.Catalogs[i].Name, body.Catalogs[i].TrinoCatalogName, connector})
			}
		}
	}
	return ordered, nil
}

// registrationFieldError is a 422 on one field of the create request.
func registrationFieldError(code ErrorCode, field, msg string) CreateCatalogConnectionResponseObject {
	return CreateCatalogConnection422JSONResponse{UnprocessableEntityJSONResponse: (&configError{
		code: code, fields: []connections.FieldError{{Key: field, Message: msg}},
	}).response()}
}

// registrationConflict maps a unique violation of a registration to a 409:
// stage says which write failed, so a taken name is reported for what it names.
func registrationConflict(stage string, err error) (CreateCatalogConnectionResponseObject, bool) {
	pg, ok := db.AsPGError(err)
	if !ok || pg.Code != "23505" {
		return nil, false
	}
	var msg string
	switch stage {
	case "connection":
		msg = conns.ErrNameTaken.Error()
	case "engine":
		msg = manage.CreateError(err).Error()
	case "binding":
		c, _ := bindingConflict(err)
		msg = c.Message
	default:
		msg = "a resource of this registration already exists"
	}
	return CreateCatalogConnection409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: msg}}, true
}

// createdConnection is the 201 body: the connection with what its creation
// made.
func (h *core) createdConnection(ctx context.Context, conn db.CatalogConnection, engine *Engine, cats []Catalog) CatalogConnectionCreated {
	c := h.withConnectionActions(ctx, conn)
	out := CatalogConnectionCreated{
		AllowedActions: c.AllowedActions, Catalogs: cats, Config: c.Config, CreatedAt: c.CreatedAt,
		CredentialsReadOnly: c.CredentialsReadOnly, Engine: engine, HasCredentials: c.HasCredentials,
		Host: c.Host, Id: c.Id, Name: c.Name, Port: c.Port, Type: c.Type, UpdatedAt: c.UpdatedAt, Url: c.Url,
	}
	if c.AccessMode != nil {
		mode := CatalogConnectionCreatedAccessMode(*c.AccessMode)
		out.AccessMode = &mode
	}
	if out.Catalogs == nil {
		out.Catalogs = []Catalog{}
	}
	return out
}

// registerOnly stores a connection that has no data access mode (an
// engine-family resource): nothing is created with it.
func (h *CatalogHandlers) registerOnly(ctx context.Context, r registration) (CreateCatalogConnectionResponseObject, error) {
	if r.body.EngineIds != nil || r.body.CatalogRegistration != nil {
		h.audited(ctx, r.event.Failed(audit.ReasonValidation))
		return registrationFieldError(ErrorCodeValidationError, "access_mode", "this connection type has no data access mode, so it takes no engine_ids or catalog_registration"), nil
	}
	conn, err := h.conns.Create(ctx, r.params)
	if errors.Is(err, conns.ErrNameTaken) {
		h.audited(ctx, r.event.Failed(audit.ReasonConflict))
		return CreateCatalogConnection409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: err.Error()}}, nil
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "CreateCatalogConnection failed", "err", err)
		return nil, err
	}
	return h.registered(ctx, r, conn, nil, nil), nil
}

// registered audits a stored registration and builds the 201.
func (h *CatalogHandlers) registered(ctx context.Context, r registration, conn db.CatalogConnection, engine *Engine, cats []Catalog) CreateCatalogConnectionResponseObject {
	h.catalogCache.Clear()
	ev := r.event
	ev.Changes = map[string]audit.Change{"name": {To: conn.Name}, "type": {To: conn.Type}, "url": {To: conn.Url}}
	if conn.AccessMode != nil {
		ev.Changes["access_mode"] = audit.Change{To: *conn.AccessMode}
	}
	if engine != nil {
		ev.Changes["engine"] = audit.Change{To: engine.Name}
	}
	if len(cats) > 0 {
		ev.Changes["catalogs"] = audit.Change{To: fmt.Sprint(len(cats))}
	}
	h.audited(ctx, ev.Allowed())
	return CreateCatalogConnection201JSONResponse(h.createdConnection(ctx, conn, engine, cats))
}

// registerDirect creates a direct connection with the native engine that runs
// on it.
func (h *CatalogHandlers) registerDirect(ctx context.Context, r registration) (CreateCatalogConnectionResponseObject, error) {
	fail := func(reason string, resp CreateCatalogConnectionResponseObject) (CreateCatalogConnectionResponseObject, error) {
		h.audited(ctx, r.event.Failed(reason))
		return resp, nil
	}
	if r.body.EngineIds != nil || r.body.CatalogRegistration != nil {
		return fail(audit.ReasonValidation, registrationFieldError(ErrorCodeValidationError, "access_mode",
			"a direct connection owns its engine and discovers its catalogs, so it takes no engine_ids or catalog_registration"))
	}
	if !connections.HasRole(r.provider, connections.RoleDirect) {
		return fail(audit.ReasonValidation, registrationFieldError("engine_unsupported", "access_mode",
			fmt.Sprintf("connections of type %q cannot be direct engines", r.params.Type)))
	}
	engineName := manage.NormalizeName(r.params.Name)
	if engineName == "" {
		return fail(audit.ReasonValidation, registrationFieldError(ErrorCodeValidationError, "name",
			"the name cannot be turned into an engine name; use letters or digits"))
	}

	// Reachability and discovery come first and hold no transaction open.
	pingCtx, cancel := context.WithTimeout(ctx, enginePingTimeout)
	defer cancel()
	if err := r.provider.Ping(pingCtx, r.config); err != nil {
		h.logger.InfoContext(ctx, "connection reachability check failed", "connection", r.params.Name, "err", err)
		return fail(audit.ReasonValidation, registrationFieldError("engine_unreachable", "config", connectionTestError(err)))
	}
	natives, err := catalogs.NativeNames(pingCtx, r.provider, r.config, r.params.Name)
	if err != nil {
		return fail(audit.ReasonValidation, registrationFieldError("engine_unreachable", "config", connectionTestError(err)))
	}

	var (
		conn      db.CatalogConnection
		eng       db.Engine
		nativeIDs []db.UUID
		stage     = "connection"
	)
	txErr := h.inRegistrationTx(ctx, func(q registrationStore) error {
		var err error
		if conn, err = q.CreateCatalogConnection(ctx, r.params); err != nil {
			return err
		}
		stage = "engine"
		eng, nativeIDs, err = manage.CreateIn(ctx, q, manage.NewEngine{
			Name: engineName, Kind: r.provider.EngineKind(), ConnectionID: conn.ID, CreatedBy: r.principal.UserID, Natives: natives,
		})
		return err
	})
	if resp, ok := registrationConflict(stage, txErr); ok {
		return fail(audit.ReasonConflict, resp)
	}
	if txErr != nil {
		h.logger.ErrorContext(ctx, "register direct connection failed", "stage", stage, "err", txErr)
		return nil, fmt.Errorf("register direct connection: %w", txErr)
	}

	// The registration is complete and committed; the metadata sync that
	// follows never decides whether it succeeded.
	for _, id := range nativeIDs {
		h.startMetadataSync(ctx, id)
	}
	rows, err := h.topology.CatalogsOfConnection(ctx, conn.ID)
	if err != nil {
		h.logger.WarnContext(ctx, "list the native catalogs of a new connection", "connection", conn.ID, "err", err)
	}
	cats := make([]Catalog, 0, len(rows))
	for _, row := range rows {
		cats = append(cats, h.catalogWithStatus(ctx, row))
	}
	engine := h.withEngineActions(ctx, eng)
	return h.registered(ctx, r, conn, &engine, cats), nil
}

// federatedTarget is a federated engine the catalogs are exposed through.
type federatedTarget struct {
	engine  db.Engine
	cluster db.TrinoCluster
}

// federatedEngines resolves and checks the engines a federated registration
// names: each one exists, the caller may write to it, it has a binder and a
// cluster, and no engine is named twice.
func (h *CatalogHandlers) federatedEngines(ctx context.Context, r registration) ([]federatedTarget, CreateCatalogConnectionResponseObject, error) {
	ids := []openapi_types.UUID{}
	if r.body.EngineIds != nil {
		ids = *r.body.EngineIds
	}
	if len(ids) == 0 {
		return nil, registrationFieldError(ErrorCodeValidationError, "engine_ids", "choose at least one federated engine"), nil
	}
	seen := map[openapi_types.UUID]bool{}
	targets := make([]federatedTarget, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			return nil, registrationFieldError(ErrorCodeValidationError, "engine_ids", "an engine is listed twice"), nil
		}
		seen[id] = true
		eng, _, err := h.authz().Engine(ctx, r.principal, db.UUID{Bytes: id, Valid: true}, authz.ActWrite)
		if notFound, forbidden := catalogAccessError(err); notFound {
			return nil, CreateCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "engine not found"}}, nil
		} else if forbidden {
			return nil, CreateCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to expose catalogs through this engine"}}, nil
		} else if err != nil {
			return nil, nil, err
		}
		if eng.Kind != h.binder.EngineKind() {
			return nil, registrationFieldError(ErrorCodeBindingUnsupported, "engine_ids", "engine kind "+eng.Kind+" has no binder"), nil
		}
		cluster, err := h.topology.ClusterOfEngine(ctx, eng.ID)
		if errors.Is(err, db.ErrNoRows) {
			return nil, registrationFieldError(ErrorCodeBindingUnsupported, "engine_ids", "the engine has no Trino cluster"), nil
		}
		if err != nil {
			return nil, nil, err
		}
		targets = append(targets, federatedTarget{engine: eng, cluster: cluster})
	}
	return targets, nil, nil
}

// federatedPlan is one logical catalog with its projection.
type federatedPlan struct {
	out  catalogOutput
	proj bindings.Projection
}

// registerFederated creates a federated connection, its logical catalogs and
// their bindings on the selected engines. The rows commit together; the
// engines then apply the catalogs through the binding lifecycle.
func (h *CatalogHandlers) registerFederated(ctx context.Context, r registration) (CreateCatalogConnectionResponseObject, error) {
	fail := func(reason string, resp CreateCatalogConnectionResponseObject) (CreateCatalogConnectionResponseObject, error) {
		h.audited(ctx, r.event.Failed(reason))
		return resp, nil
	}
	if !connections.TrinoBindable(r.provider) {
		return fail(audit.ReasonValidation, registrationFieldError("binding_unsupported", "access_mode",
			fmt.Sprintf("connections of type %q cannot be exposed through a federated engine", r.params.Type)))
	}
	if r.body.CatalogRegistration == nil {
		return fail(audit.ReasonValidation, registrationFieldError(ErrorCodeValidationError, "catalog_registration",
			"required for a federated connection"))
	}
	reg := *r.body.CatalogRegistration

	targets, resp, err := h.federatedEngines(ctx, r)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		return fail(audit.ReasonValidation, resp)
	}
	ordered, invalid := planCatalogOutputs(h.connections.CatalogTypes(r.config.Kind), reg)
	if invalid != nil {
		return fail(audit.ReasonValidation, CreateCatalogConnection422JSONResponse{UnprocessableEntityJSONResponse: *invalid})
	}
	warehouse := ""
	if reg.Warehouse != nil {
		warehouse = *reg.Warehouse
	}
	extra := map[string]string{}
	if reg.ExtraProps != nil {
		extra = *reg.ExtraProps
	}
	rawExtra, _ := json.Marshal(extra)

	var shareWith db.UUID
	if reg.ShareWithWorkspaceId != nil {
		if err := requireWorkspaceAccess(ctx, h.access, *reg.ShareWithWorkspaceId); err != nil {
			if errors.Is(err, ErrWorkspaceNotFound) {
				return fail(audit.ReasonValidation, CreateCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}})
			}
			return nil, err
		}
		shareWith = db.UUID{Bytes: *reg.ShareWithWorkspaceId, Valid: true}
	}

	// Project every output before anything is written, so a configuration the
	// binder refuses is a 422 and not a half-registered connection. A hive
	// output is projected again once its siblings are active.
	plans := make([]federatedPlan, 0, len(ordered))
	for _, out := range ordered {
		spec := bindings.BindingSpec{EngineCatalog: out.engineName, Connector: out.connector, SourceRef: warehouse, ExtraProps: copyProps(extra)}
		proj, err := h.binder.Project(spec, r.config)
		if bindingInvalid(err) {
			return fail(audit.ReasonValidation, CreateCatalogConnection422JSONResponse{UnprocessableEntityJSONResponse: bindingUnprocessable(err)})
		}
		if err != nil {
			return nil, err
		}
		plans = append(plans, federatedPlan{out, proj})
	}

	// The first engine is the primary one: the catalogs' Trino columns name
	// its cluster, as they do for a catalog created on a connection.
	primary := targets[0]
	type created struct {
		cat      db.Catalog
		bindings []db.CatalogBinding // parallel to targets
		proj     bindings.Projection
	}
	var (
		conn  db.CatalogConnection
		made  = make([]created, 0, len(plans))
		stage = "connection"
	)
	txErr := h.inRegistrationTx(ctx, func(q registrationStore) error {
		var err error
		made = made[:0]
		if conn, err = q.CreateCatalogConnection(ctx, r.params); err != nil {
			return err
		}
		for _, pl := range plans {
			stage = "catalog"
			proj := pl.proj
			cat, err := q.CreateCatalog(ctx, db.CreateCatalogParams{
				ConnectionID:     conn.ID,
				Name:             pl.out.name,
				Description:      reg.Description,
				TrinoCatalogName: &pl.out.engineName,
				ClusterID:        primary.cluster.ID,
				Warehouse:        &warehouse,
				Connector:        &proj.Connector,
				ExtraProps:       rawExtra,
			})
			if err != nil {
				return err
			}
			stage = "binding"
			row := created{cat: cat, proj: proj}
			primaryBinding, err := upsertPrimaryBinding(ctx, q, primary.cluster, cat, bindings.StateProvisioning, proj.Version)
			if err != nil {
				return err
			}
			row.bindings = append(row.bindings, primaryBinding)
			for _, t := range targets[1:] {
				b, err := q.CreateManagedBinding(ctx, db.CreateManagedBindingParams{
					CatalogID: cat.ID, EngineID: t.engine.ID, EngineCatalogName: proj.EngineCatalog,
					Connector: &proj.Connector, ExtraProps: rawExtra, DesiredVersion: &proj.Version,
				})
				if err != nil {
					return err
				}
				row.bindings = append(row.bindings, b)
			}
			if shareWith.Valid {
				stage = "grant"
				if _, err := q.UpsertWorkspaceCatalogGrant(ctx, db.UpsertWorkspaceCatalogGrantParams{
					WorkspaceID: shareWith, CatalogID: cat.ID, GrantedBy: r.principal.UserID,
				}); err != nil {
					return err
				}
			}
			made = append(made, row)
		}
		return nil
	})
	if resp, ok := registrationConflict(stage, txErr); ok {
		return fail(audit.ReasonConflict, resp)
	}
	if txErr != nil {
		h.logger.ErrorContext(ctx, "register federated connection failed", "stage", stage, "err", txErr)
		return nil, fmt.Errorf("register federated connection: %w", txErr)
	}

	// Committed: the desired state is persisted. Each engine now applies it; a
	// failure is recorded on the binding and reported, never undone, and the
	// reconciler repairs it.
	h.catalogCache.Clear()
	for i, t := range targets {
		l := h.lifecycleOn(t.cluster, t.engine)
		for j := range made {
			made[j].bindings[i] = h.provisionRegistered(ctx, l, t, conn, made[j].cat, made[j].bindings[i], made[j].proj, warehouse, extra)
		}
		h.observe(ctx, l)
	}
	cats := make([]Catalog, 0, len(made))
	for _, m := range made {
		h.startMetadataSync(ctx, m.cat.ID)
		cats = append(cats, h.catalogResponse(ctx, m.cat, m.bindings[0], m.proj.Version))
	}
	return h.registered(ctx, r, conn, nil, cats), nil
}

// provisionRegistered applies one committed binding on its engine and returns
// it as the lifecycle left it. A hive binding is projected again first: its
// table redirection names the siblings that are active by now.
func (h *CatalogHandlers) provisionRegistered(ctx context.Context, l bindings.Lifecycle, t federatedTarget, conn db.CatalogConnection, cat db.Catalog, b db.CatalogBinding, proj bindings.Projection, warehouse string, extra map[string]string) db.CatalogBinding {
	if proj.Connector == "hive" {
		spec := bindings.BindingSpec{EngineCatalog: proj.EngineCatalog, Connector: proj.Connector, SourceRef: warehouse, ExtraProps: copyProps(extra)}
		redone, err := h.projectBinding(ctx, conn, t.engine.ID, cat.ID, spec)
		if err != nil {
			h.logger.WarnContext(ctx, "project a hive binding with its siblings", "binding", b.ID, "err", err)
		} else if redone.Version != proj.Version {
			raw, _ := json.Marshal(spec.ExtraProps)
			updated, err := h.rewriteDesired(ctx, t, cat, b, redone, raw)
			if err != nil {
				h.logger.WarnContext(ctx, "update the desired state of a hive binding", "binding", b.ID, "err", err)
			} else {
				b, proj = updated, redone
			}
		}
	}
	out, err := l.Provision(ctx, b, proj)
	if err != nil {
		// A concurrent change leaves the binding as the other writer set it;
		// the response reports whatever state it is in.
		h.logger.WarnContext(ctx, "apply a registered binding", "binding", b.ID, "err", err)
		return b
	}
	return out
}

// rewriteDesired replaces the desired version and properties of a binding that
// has not been applied yet, keeping the catalog's Trino columns in step when
// the binding is the catalog's primary one.
func (h *CatalogHandlers) rewriteDesired(ctx context.Context, t federatedTarget, cat db.Catalog, b db.CatalogBinding, proj bindings.Projection, rawExtra []byte) (db.CatalogBinding, error) {
	var updated db.CatalogBinding
	err := h.inRegistrationTx(ctx, func(q registrationStore) error {
		var err error
		updated, err = q.UpdateBinding(ctx, db.UpdateBindingParams{
			ID: b.ID, EngineCatalogName: proj.EngineCatalog, Connector: &proj.Connector, ExtraProps: rawExtra,
			Enabled: b.Enabled, LifecycleState: bindings.StateProvisioning, DesiredVersion: &proj.Version,
			FromStates: []string{bindings.StateProvisioning},
		})
		if err != nil {
			return err
		}
		return syncPrimaryCatalog(ctx, q, bindingTarget{catalog: cat, engine: t.engine, cluster: t.cluster}, updated)
	})
	return updated, err
}

// copyProps returns a copy of a property map, so projecting one output never
// shows its additions to the next.
func copyProps(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
