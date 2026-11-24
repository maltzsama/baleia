package api

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/datapolicy"
	"github.com/galileostudio/baleia/internal/datapolicy/viewsql"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// DataPolicyHandlers serve row filters and column masks (ADR 0028, #1144):
// authoring on the admin plane of each catalog, publication into signed
// revisions, and the integration status of each engine.
type DataPolicyHandlers struct {
	*core
	policies  *repo.DataPolicies
	publisher *datapolicy.Publisher
	// views reads the views of the catalogs bound to enforcing engines;
	// inspectViews in production.
	views func(ctx context.Context) (datapolicy.ViewInspection, error)
}

// dataPolicyStatusStale is how long an engine's OPA status report stays
// current; OPA reports at every bundle download, a few times a minute.
const dataPolicyStatusStale = 10 * time.Minute

// viewConnectors are the Trino connectors that can hold Trino views: a
// catalog bound through one must have views Baleia can read before any
// policy is published (ADR 0028, fail closed).
var viewConnectors = map[string]bool{"iceberg": true, "hive": true, "delta_lake": true, "memory": true}

func newDataPolicyHandlers(c *core, key *rsa.PrivateKey) *DataPolicyHandlers {
	h := &DataPolicyHandlers{core: c}
	h.views = h.inspectViews
	if c.store == nil {
		return h
	}
	h.policies = repo.NewDataPolicies(c.store)
	if key != nil && c.s3 != nil {
		trusted := map[string]bool{}
		for _, name := range c.settings.DataPolicy.Trusted {
			if name != "" {
				trusted[name] = true
			}
		}
		h.publisher = &datapolicy.Publisher{
			Store: h.policies, Objects: c.s3, Key: key, Lease: c.settings.DataPolicy.Lease,
			IdentityMode: c.settings.DataPolicy.IdentityMode, Trusted: trusted,
		}
	}
	return h
}

// ── authoring ─────────────────────────────────────────────────────────────────

// dataPolicyCatalog checks the caller may manage the catalog's data policies:
// the owner of its connection or a global admin (manage_data_policies).
func (h *DataPolicyHandlers) dataPolicyCatalog(ctx context.Context, id uuid.UUID) (db.Catalog, db.CatalogConnection, authz.Principal, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return db.Catalog{}, db.CatalogConnection{}, p, authz.ErrUnauthenticated
	}
	cat, conn, err := h.authz().Catalog(ctx, p, db.UUID{Bytes: id, Valid: true}, authz.ActAdmin)
	return cat, conn, p, err
}

// errUnsupportedCatalog: the catalog cannot carry data policies.
var errUnsupportedCatalog = errors.New("the catalog's views cannot be read")

// catalogNames returns the catalog's names on the engines that serve it and
// its bindings, in creation order, to type-check on. Every binding must be on an engine that
// enforces data policies, and there must be one (errPoliciesUnenforced); the
// catalog's connection must serve Iceberg REST, whose views Baleia reads
// (errUnsupportedCatalog).
func (h *DataPolicyHandlers) catalogNames(ctx context.Context, cat db.Catalog, conn db.CatalogConnection) ([]string, []db.CatalogBinding, error) {
	provider, err := h.connections.Get(connections.Kind(conn.Type))
	if err != nil || cat.Origin != "source" {
		return nil, nil, errUnsupportedCatalog
	}
	if _, rest := provider.(connections.IcebergRESTEndpoint); !rest {
		return nil, nil, errUnsupportedCatalog
	}
	bindings, err := h.queries.ListBindingsByCatalog(ctx, cat.ID)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(bindings))
	for _, b := range bindings {
		eng, err := h.queries.GetEngineByID(ctx, b.EngineID)
		if err != nil {
			return nil, nil, err
		}
		if !eng.DataPolicies {
			return nil, nil, errPoliciesUnenforced
		}
		names = append(names, b.EngineCatalogName)
	}
	if len(names) == 0 {
		return nil, nil, errPoliciesUnenforced
	}
	return slices.Compact(slices.Sorted(slices.Values(names))), bindings, nil
}

// expressionTypes is what an engine needs to type-check a policy; the Trino
// engine implements it.
type expressionTypes interface {
	ExpressionTypes(ctx context.Context, table []string, exprs []string) ([]string, error)
}

// checkPolicy runs the language check against the table's stored columns
// and the type check on the first binding whose engine can be asked; every
// binding is on an engine that enforces data policies. When none can be
// asked the policy stays pending: it is checked again at its next update.
func (h *DataPolicyHandlers) checkPolicy(ctx context.Context, p *datapolicy.Policy, cat db.Catalog, bindings []db.CatalogBinding) {
	columns := h.tableColumns(ctx, cat.ID, p.Target.Schema, p.Target.Table)
	p.Validation, p.Compiled, p.InvalidReason, p.InvalidDetail = datapolicy.ValidationPending, "", "", ""
	for _, binding := range bindings {
		eng, err := h.engines.Get(ctx, binding.EngineID)
		if err != nil {
			h.logger.WarnContext(ctx, "data policy type check: engine unavailable", "engine_id", binding.EngineID, "err", err)
			continue
		}
		typer, ok := eng.(expressionTypes)
		if !ok {
			continue
		}
		table := []string{binding.EngineCatalogName, p.Target.Schema, p.Target.Table}
		if err := datapolicy.Check(ctx, p, columns, typer, table); err != nil {
			h.logger.WarnContext(ctx, "data policy type check failed to run", "engine_id", binding.EngineID, "err", err)
			continue
		}
		return
	}
}

// tableColumns returns a table's stored column names, or nil when its
// metadata was not synced (the engine's type check then finds a wrong one).
func (h *DataPolicyHandlers) tableColumns(ctx context.Context, catalogID db.UUID, schema, table string) []string {
	id, err := h.queries.GetMetadataTableByName(ctx, db.GetMetadataTableByNameParams{CatalogID: catalogID, Namespace: []string{schema}, Name: table})
	if err != nil {
		return nil
	}
	rows, err := h.queries.ListMetadataColumns(ctx, id)
	if err != nil || len(rows) == 0 {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func (h *DataPolicyHandlers) ListCatalogDataPolicies(ctx context.Context, request ListCatalogDataPoliciesRequestObject) (ListCatalogDataPoliciesResponseObject, error) {
	cat, conn, _, err := h.dataPolicyCatalog(ctx, request.Id)
	if resp, done := catalogAccessResponse[ListCatalogDataPoliciesResponseObject](err,
		ListCatalogDataPolicies401JSONResponse{UnauthorizedJSONResponse: dataPolicyUnauthorized()},
		ListCatalogDataPolicies403JSONResponse{ForbiddenJSONResponse: forbiddenDataPolicies()},
		ListCatalogDataPolicies404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}},
	); done {
		return resp, nil
	}
	if err != nil {
		return nil, err
	}
	policies, err := h.policies.ListByCatalog(ctx, uuid.UUID(cat.ID.Bytes))
	if err != nil {
		return nil, err
	}
	out := CatalogDataPolicies{Enabled: h.settings.DataPolicy.Enabled, Names: []string{}, Policies: make([]DataPolicy, 0, len(policies))}
	for _, p := range policies {
		out.Policies = append(out.Policies, dataPolicyToPublic(p))
	}
	// A catalog that cannot carry policies has no names and no draft; a
	// failed lookup is an error, never a known draft state.
	names, _, err := h.catalogNames(ctx, cat, conn)
	if _, refused := catalogTargetRefusal(err); refused {
		return ListCatalogDataPolicies200JSONResponse(out), nil
	}
	if err != nil {
		return nil, err
	}
	out.Names = names
	if h.publisher != nil {
		if out.Draft, err = h.publisher.CatalogChanged(ctx, uuid.UUID(cat.ID.Bytes), names); err != nil {
			return nil, err
		}
	}
	return ListCatalogDataPolicies200JSONResponse(out), nil
}

func (h *DataPolicyHandlers) CreateDataPolicy(ctx context.Context, request CreateDataPolicyRequestObject) (CreateDataPolicyResponseObject, error) {
	ev := audit.Event{Action: audit.DataPolicyCreate, ResourceType: "data_policy"}
	ctx = audit.WithAction(ctx, ev.Action)
	cat, conn, principal, err := h.dataPolicyCatalog(ctx, request.Id)
	if resp, done := catalogAccessResponse[CreateDataPolicyResponseObject](err,
		CreateDataPolicy401JSONResponse{UnauthorizedJSONResponse: dataPolicyUnauthorized()},
		CreateDataPolicy403JSONResponse{ForbiddenJSONResponse: forbiddenDataPolicies()},
		CreateDataPolicy404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}},
	); done {
		return resp, nil
	}
	if err != nil {
		return nil, err
	}
	if !h.settings.DataPolicy.Enabled {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return CreateDataPolicy409JSONResponse{ConflictJSONResponse: dataPoliciesDisabled()}, nil
	}
	if request.Body == nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateDataPolicy422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "a body is required")}, nil
	}
	_, bindings, err := h.catalogNames(ctx, cat, conn)
	if resp, ok := catalogTargetRefusal(err); ok {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateDataPolicy422JSONResponse{UnprocessableEntityJSONResponse: resp}, nil
	}
	if err != nil {
		return nil, err
	}
	p := dataPolicyFromRequest(*request.Body, uuid.UUID(cat.ID.Bytes))
	p.CreatedBy = uuid.UUID(principal.UserID.Bytes)
	if resp, invalid := validateDataPolicy(&p); invalid {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return CreateDataPolicy422JSONResponse{UnprocessableEntityJSONResponse: resp}, nil
	}
	h.checkPolicy(ctx, &p, cat, bindings)
	created, err := h.policies.Create(ctx, p)
	if errors.Is(err, datapolicy.ErrNameTaken) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return CreateDataPolicy409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeNameInUse, Message: "the table already has a data policy of that name"}}, nil
	}
	if err != nil {
		return nil, err
	}
	h.audited(ctx, withID(ev, db.UUID{Bytes: created.ID, Valid: true}).Allowed())
	return CreateDataPolicy201JSONResponse(dataPolicyToPublic(created)), nil
}

// policyAccess loads a policy and checks the caller on its catalog.
func (h *DataPolicyHandlers) policyAccess(ctx context.Context, id uuid.UUID) (datapolicy.Policy, db.Catalog, db.CatalogConnection, error) {
	p, err := h.policies.Get(ctx, id)
	if errors.Is(err, datapolicy.ErrNotFound) {
		return datapolicy.Policy{}, db.Catalog{}, db.CatalogConnection{}, authz.ErrNotFound
	}
	if err != nil {
		return datapolicy.Policy{}, db.Catalog{}, db.CatalogConnection{}, err
	}
	cat, conn, _, err := h.dataPolicyCatalog(ctx, p.Target.CatalogID)
	return p, cat, conn, err
}

func (h *DataPolicyHandlers) GetDataPolicy(ctx context.Context, request GetDataPolicyRequestObject) (GetDataPolicyResponseObject, error) {
	p, _, _, err := h.policyAccess(ctx, request.Id)
	switch {
	case errors.Is(err, authz.ErrUnauthenticated):
		return GetDataPolicy401JSONResponse{UnauthorizedJSONResponse: dataPolicyUnauthorized()}, nil
	case errors.Is(err, authz.ErrNotFound), errors.Is(err, authz.ErrForbidden):
		return GetDataPolicy404JSONResponse{NotFoundJSONResponse: dataPolicyNotFound()}, nil
	case err != nil:
		return nil, err
	}
	return GetDataPolicy200JSONResponse(dataPolicyToPublic(p)), nil
}

func (h *DataPolicyHandlers) UpdateDataPolicy(ctx context.Context, request UpdateDataPolicyRequestObject) (UpdateDataPolicyResponseObject, error) {
	ev := audit.Event{Action: audit.DataPolicyUpdate, ResourceType: "data_policy", ResourceID: request.Id.String()}
	ctx = audit.WithAction(ctx, ev.Action)
	current, cat, conn, err := h.policyAccess(ctx, request.Id)
	switch {
	case errors.Is(err, authz.ErrUnauthenticated):
		return UpdateDataPolicy401JSONResponse{UnauthorizedJSONResponse: dataPolicyUnauthorized()}, nil
	case errors.Is(err, authz.ErrNotFound):
		return UpdateDataPolicy404JSONResponse{NotFoundJSONResponse: dataPolicyNotFound()}, nil
	case errors.Is(err, authz.ErrForbidden):
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return UpdateDataPolicy403JSONResponse{ForbiddenJSONResponse: forbiddenDataPolicies()}, nil
	case err != nil:
		return nil, err
	}
	if !h.settings.DataPolicy.Enabled {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return UpdateDataPolicy409JSONResponse{ConflictJSONResponse: dataPoliciesDisabled()}, nil
	}
	if request.Body == nil {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateDataPolicy422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "a body is required")}, nil
	}
	_, bindings, err := h.catalogNames(ctx, cat, conn)
	if resp, ok := catalogTargetRefusal(err); ok {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateDataPolicy422JSONResponse{UnprocessableEntityJSONResponse: resp}, nil
	}
	if err != nil {
		return nil, err
	}
	body := *request.Body
	p := dataPolicyFromRequest(CreateDataPolicyRequest{
		Name: body.Name, Description: body.Description, Kind: body.Kind, Schema: body.Schema, Table: body.Table,
		Column: body.Column, Mode: body.Mode, Priority: body.Priority, Subjects: body.Subjects,
		Expression: body.Expression, Template: body.Template, State: body.State,
	}, current.Target.CatalogID)
	p.ID, p.CreatedBy, p.CreatedAt = current.ID, current.CreatedBy, current.CreatedAt
	if resp, invalid := validateDataPolicy(&p); invalid {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return UpdateDataPolicy422JSONResponse{UnprocessableEntityJSONResponse: resp}, nil
	}
	h.checkPolicy(ctx, &p, cat, bindings)
	updated, err := h.policies.Update(ctx, p, body.Version)
	switch {
	case errors.Is(err, datapolicy.ErrVersionConflict):
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return UpdateDataPolicy409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeVersionConflict, Message: "the policy changed since it was read"}}, nil
	case errors.Is(err, datapolicy.ErrNameTaken):
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return UpdateDataPolicy409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeNameInUse, Message: "the table already has a data policy of that name"}}, nil
	case errors.Is(err, datapolicy.ErrNotFound):
		return UpdateDataPolicy404JSONResponse{NotFoundJSONResponse: dataPolicyNotFound()}, nil
	case err != nil:
		return nil, err
	}
	h.audited(ctx, ev.Allowed())
	return UpdateDataPolicy200JSONResponse(dataPolicyToPublic(updated)), nil
}

func (h *DataPolicyHandlers) DeleteDataPolicy(ctx context.Context, request DeleteDataPolicyRequestObject) (DeleteDataPolicyResponseObject, error) {
	ev := audit.Event{Action: audit.DataPolicyDelete, ResourceType: "data_policy", ResourceID: request.Id.String()}
	ctx = audit.WithAction(ctx, ev.Action)
	_, _, _, err := h.policyAccess(ctx, request.Id)
	switch {
	case errors.Is(err, authz.ErrUnauthenticated):
		return DeleteDataPolicy401JSONResponse{UnauthorizedJSONResponse: dataPolicyUnauthorized()}, nil
	case errors.Is(err, authz.ErrNotFound):
		return DeleteDataPolicy404JSONResponse{NotFoundJSONResponse: dataPolicyNotFound()}, nil
	case errors.Is(err, authz.ErrForbidden):
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return DeleteDataPolicy403JSONResponse{ForbiddenJSONResponse: forbiddenDataPolicies()}, nil
	case err != nil:
		return nil, err
	}
	if _, err := h.policies.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	h.audited(ctx, ev.Allowed())
	return DeleteDataPolicy204Response{}, nil
}

// ── publication ───────────────────────────────────────────────────────────────

// inspectViews reads the views of every catalog bound to an engine that
// enforces data policies. A catalog bound through a connector that holds
// Trino views, whose views Baleia cannot read, is listed as uninspectable:
// publishing refuses it (fail closed).
func (h *DataPolicyHandlers) inspectViews(ctx context.Context) (datapolicy.ViewInspection, error) {
	engines, err := h.queries.ListEngines(ctx)
	if err != nil {
		return datapolicy.ViewInspection{}, err
	}
	names := map[db.UUID][]string{}
	viewCapable := map[db.UUID]bool{}
	var order []db.UUID
	for _, eng := range engines {
		if !eng.DataPolicies {
			continue
		}
		bindings, err := h.queries.ListBindingsByEngine(ctx, eng.ID)
		if err != nil {
			return datapolicy.ViewInspection{}, err
		}
		for _, b := range bindings {
			if _, seen := names[b.CatalogID]; !seen {
				order = append(order, b.CatalogID)
			}
			names[b.CatalogID] = append(names[b.CatalogID], b.EngineCatalogName)
			if b.Connector == nil || viewConnectors[*b.Connector] {
				viewCapable[b.CatalogID] = true
			}
		}
	}
	var out datapolicy.ViewInspection
	for _, catalogID := range order {
		views, readable, err := h.catalogViews(ctx, catalogID, names[catalogID])
		if err != nil {
			return datapolicy.ViewInspection{}, err
		}
		if !readable {
			if viewCapable[catalogID] {
				out.Uninspectable = append(out.Uninspectable, names[catalogID]...)
			}
			continue
		}
		out.Views = append(out.Views, views...)
	}
	return out, nil
}

// catalogViews reads every view of a catalog through its connection, or
// reports that its views cannot be read.
func (h *DataPolicyHandlers) catalogViews(ctx context.Context, catalogID db.UUID, names []string) ([]datapolicy.CatalogView, bool, error) {
	cat, err := h.queries.GetCatalogByID(ctx, catalogID)
	if err != nil {
		return nil, false, err
	}
	if cat.Origin != "source" {
		return nil, false, nil
	}
	conn, err := h.queries.GetCatalogConnectionByID(ctx, cat.ConnectionID)
	if err != nil {
		return nil, false, err
	}
	provider, err := h.connections.Get(connections.Kind(conn.Type))
	if err != nil {
		return nil, false, nil //nolint:nilerr // an unknown kind is a catalog whose views cannot be read
	}
	if _, rest := provider.(connections.IcebergRESTEndpoint); !rest {
		return nil, false, nil
	}
	plain, err := catalogs.OpenCredentials(conn, h.keyring)
	if err != nil {
		return nil, false, err
	}
	cfg, err := providers.FromStored(h.connections, provider.Kind(), conn.ID, conn.Url, conn.Config, plain)
	if err != nil {
		return nil, false, err
	}
	reader, err := provider.OpenMetadata(ctx, cfg)
	if err != nil {
		return nil, false, fmt.Errorf("open the catalog manager of %s: %w", cat.Name, err)
	}
	defer func() { _ = reader.Close() }()
	vr, ok := reader.(connections.ViewReader)
	if !ok {
		return nil, false, nil
	}
	ref := catalogSourceRef(cat)
	var out []datapolicy.CatalogView
	var walk func(parent connections.Namespace) error
	walk = func(parent connections.Namespace) error {
		namespaces, err := reader.ListNamespaces(ctx, ref, parent)
		if err != nil {
			return err
		}
		for _, ns := range namespaces {
			viewNames, err := vr.ListViews(ctx, ref, ns)
			if err != nil {
				return err
			}
			for _, name := range viewNames {
				v, err := vr.LoadView(ctx, ref, ns, name)
				if err != nil {
					return err
				}
				out = append(out, viewsql.Resolve(names, v))
			}
			if err := walk(ns); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(nil); err != nil {
		return nil, false, fmt.Errorf("read the views of %s: %w", cat.Name, err)
	}
	return out, true, nil
}

// catalogSourceRef is the catalog's name inside its connection.
func catalogSourceRef(c db.Catalog) string {
	if c.SourceRef != nil && *c.SourceRef != "" {
		return *c.SourceRef
	}
	if c.Warehouse != nil && *c.Warehouse != "" {
		return *c.Warehouse
	}
	return c.Name
}

func (h *DataPolicyHandlers) PublishCatalogDataPolicies(ctx context.Context, request PublishCatalogDataPoliciesRequestObject) (PublishCatalogDataPoliciesResponseObject, error) {
	ev := audit.Event{Action: audit.DataPolicyPublish, ResourceType: "catalog", ResourceID: request.Id.String()}
	ctx = audit.WithAction(ctx, ev.Action)
	cat, conn, principal, err := h.dataPolicyCatalog(ctx, request.Id)
	if resp, done := catalogAccessResponse[PublishCatalogDataPoliciesResponseObject](err,
		PublishCatalogDataPolicies401JSONResponse{UnauthorizedJSONResponse: dataPolicyUnauthorized()},
		PublishCatalogDataPolicies403JSONResponse{ForbiddenJSONResponse: forbiddenDataPolicies()},
		PublishCatalogDataPolicies404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}},
	); done {
		return resp, nil
	}
	if err != nil {
		return nil, err
	}
	if !h.settings.DataPolicy.Enabled || h.publisher == nil {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return PublishCatalogDataPolicies409JSONResponse{ConflictJSONResponse: dataPoliciesDisabled()}, nil
	}
	names, _, err := h.catalogNames(ctx, cat, conn)
	if resp, ok := catalogTargetRefusal(err); ok {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return PublishCatalogDataPolicies422JSONResponse{DataPolicyRefusedJSONResponse: DataPolicyRefusedJSONResponse{
			Code: resp.Code, Message: resp.Message, Problems: []DataPolicyProblem{},
		}}, nil
	}
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonInternal))
		return nil, err
	}
	views, err := h.views(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "data policy publish: views could not be read", "err", err)
		h.audited(ctx, ev.Failed(audit.ReasonInternal))
		return PublishCatalogDataPolicies503JSONResponse{ServiceUnavailableJSONResponse: ServiceUnavailableJSONResponse{
			Code: ErrorCodeServiceUnavailable, Message: "the catalog managers' views could not be read",
		}}, nil
	}
	rev, err := h.publisher.PublishCatalog(ctx, uuid.UUID(cat.ID.Bytes), names, views, uuid.UUID(principal.UserID.Bytes))
	if resp, ok := catalogTargetRefusal(err); ok {
		// An engine stopped enforcing between the check above and the commit.
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return PublishCatalogDataPolicies422JSONResponse{DataPolicyRefusedJSONResponse: DataPolicyRefusedJSONResponse{
			Code: resp.Code, Message: resp.Message, Problems: []DataPolicyProblem{},
		}}, nil
	}
	if refused, ok := publishRefusal(err); ok {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return PublishCatalogDataPolicies422JSONResponse{DataPolicyRefusedJSONResponse: refused}, nil
	}
	if errors.Is(err, datapolicy.ErrVersionConflict) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return PublishCatalogDataPolicies409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConcurrentChange, Message: "another publish landed first; try again"}}, nil
	}
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonInternal))
		return nil, err
	}
	ev.Changes = map[string]audit.Change{"revision": {To: rev.ID.String()}}
	h.audited(ctx, ev.Allowed())
	return PublishCatalogDataPolicies201JSONResponse(revisionToPublic(rev, true)), nil
}

func (h *DataPolicyHandlers) ListCatalogDefinerViews(ctx context.Context, request ListCatalogDefinerViewsRequestObject) (ListCatalogDefinerViewsResponseObject, error) {
	cat, conn, _, err := h.dataPolicyCatalog(ctx, request.Id)
	if resp, done := catalogAccessResponse[ListCatalogDefinerViewsResponseObject](err,
		ListCatalogDefinerViews401JSONResponse{UnauthorizedJSONResponse: dataPolicyUnauthorized()},
		ListCatalogDefinerViews403JSONResponse{ForbiddenJSONResponse: forbiddenDataPolicies()},
		ListCatalogDefinerViews404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "catalog not found"}},
	); done {
		return resp, nil
	}
	if err != nil {
		return nil, err
	}
	out := []DataPolicyDefinerView{}
	names, _, err := h.catalogNames(ctx, cat, conn)
	switch {
	case errors.Is(err, errPoliciesUnenforced):
		// The draft cannot be published while an engine does not enforce
		// it, but the published revision stays in force: only it counts.
		names = nil
	case errors.Is(err, errUnsupportedCatalog):
		return ListCatalogDefinerViews200JSONResponse(out), nil
	case err != nil:
		return nil, err
	}
	policies, err := h.policies.ListByCatalog(ctx, uuid.UUID(cat.ID.Bytes))
	if err != nil {
		return nil, err
	}
	// The views are judged against the policies in force and the draft: a
	// policy deleted from the draft still applies until the next publish.
	// Every enabled draft policy counts, validated or not, since it is the
	// one a publish would put in force; a disabled one would not be.
	inForce, published, err := h.publishedCatalog(ctx, uuid.UUID(cat.ID.Bytes))
	if err != nil {
		return nil, err
	}
	policied := map[datapolicy.TableName]bool{}
	if published {
		policied = datapolicy.Content{Catalogs: map[string]datapolicy.CatalogContent{"published": inForce}}.PoliciedTables()
	}
	for _, p := range policies {
		if p.State != datapolicy.StateEnabled {
			continue
		}
		for _, name := range names {
			policied[datapolicy.NewTableName(name, p.Target.Schema, p.Target.Table)] = true
		}
	}
	views, err := h.views(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "data policy definer views: views could not be read", "err", err)
		return ListCatalogDefinerViews503JSONResponse{ServiceUnavailableJSONResponse: ServiceUnavailableJSONResponse{
			Code: ErrorCodeServiceUnavailable, Message: "the catalog managers' views could not be read",
		}}, nil
	}
	trusted := map[string]bool{}
	for _, name := range h.settings.DataPolicy.Trusted {
		trusted[name] = true
	}
	for _, r := range datapolicy.DefinerReach(views.Views, policied) {
		reads := make([]string, 0, len(r.Reads))
		for _, t := range r.Reads {
			reads = append(reads, t.String())
		}
		out = append(out, DataPolicyDefinerView{View: r.View.String(), Owner: r.Owner, Reads: reads, Trusted: trusted[r.Owner], Unreadable: r.Unreadable})
	}
	return ListCatalogDefinerViews200JSONResponse(out), nil
}

// publishedCatalog returns what the published revision holds for a catalog,
// and whether it holds anything.
func (h *DataPolicyHandlers) publishedCatalog(ctx context.Context, catalogID uuid.UUID) (datapolicy.CatalogContent, bool, error) {
	rev, err := h.policies.PublishedRevision(ctx)
	if errors.Is(err, datapolicy.ErrNotFound) {
		return datapolicy.CatalogContent{}, false, nil
	}
	if err != nil {
		return datapolicy.CatalogContent{}, false, err
	}
	var content datapolicy.Content
	if err := json.Unmarshal(rev.Content, &content); err != nil {
		return datapolicy.CatalogContent{}, false, fmt.Errorf("read revision %s: %w", rev.ID, err)
	}
	catalog, ok := content.Catalogs[catalogID.String()]
	return catalog, ok, nil
}

// ── revisions and status (global admin) ──────────────────────────────────────

func (h *DataPolicyHandlers) globalAdmin(ctx context.Context) (authz.Principal, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return p, authz.ErrUnauthenticated
	}
	if !p.IsAdmin() {
		return p, authz.ErrForbidden
	}
	return p, nil
}

func (h *DataPolicyHandlers) ListDataPolicyRevisions(ctx context.Context, request ListDataPolicyRevisionsRequestObject) (ListDataPolicyRevisionsResponseObject, error) {
	switch _, err := h.globalAdmin(ctx); {
	case errors.Is(err, authz.ErrUnauthenticated):
		return ListDataPolicyRevisions401JSONResponse{UnauthorizedJSONResponse: dataPolicyUnauthorized()}, nil
	case errors.Is(err, authz.ErrForbidden):
		return ListDataPolicyRevisions403JSONResponse{ForbiddenJSONResponse: forbiddenDataPolicyAdmin()}, nil
	}
	limit := 50
	if request.Params.Limit != nil {
		limit = min(max(*request.Params.Limit, 1), 200)
	}
	revisions, err := h.policies.ListRevisions(ctx, limit)
	if err != nil {
		return nil, err
	}
	published, err := h.policies.PublishedRevision(ctx)
	if err != nil && !errors.Is(err, datapolicy.ErrNotFound) {
		return nil, err
	}
	out := make([]DataPolicyRevision, 0, len(revisions))
	for _, rev := range revisions {
		out = append(out, revisionToPublic(rev, rev.ID == published.ID))
	}
	return ListDataPolicyRevisions200JSONResponse(out), nil
}

func (h *DataPolicyHandlers) RollbackDataPolicyRevision(ctx context.Context, request RollbackDataPolicyRevisionRequestObject) (RollbackDataPolicyRevisionResponseObject, error) {
	ev := audit.Event{Action: audit.DataPolicyRollback, ResourceType: "data_policy_revision", ResourceID: request.Id.String()}
	ctx = audit.WithAction(ctx, ev.Action)
	principal, err := h.globalAdmin(ctx)
	switch {
	case errors.Is(err, authz.ErrUnauthenticated):
		return RollbackDataPolicyRevision401JSONResponse{UnauthorizedJSONResponse: dataPolicyUnauthorized()}, nil
	case errors.Is(err, authz.ErrForbidden):
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return RollbackDataPolicyRevision403JSONResponse{ForbiddenJSONResponse: forbiddenDataPolicyAdmin()}, nil
	}
	if !h.settings.DataPolicy.Enabled || h.publisher == nil {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return RollbackDataPolicyRevision409JSONResponse{ConflictJSONResponse: dataPoliciesDisabled()}, nil
	}
	views, err := h.views(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "data policy rollback: views could not be read", "err", err)
		h.audited(ctx, ev.Failed(audit.ReasonInternal))
		return RollbackDataPolicyRevision503JSONResponse{ServiceUnavailableJSONResponse: ServiceUnavailableJSONResponse{
			Code: ErrorCodeServiceUnavailable, Message: "the catalog managers' views could not be read",
		}}, nil
	}
	rev, err := h.publisher.Rollback(ctx, request.Id, views, uuid.UUID(principal.UserID.Bytes), h.rollbackNames)
	if resp, ok := catalogTargetRefusal(err); ok {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return RollbackDataPolicyRevision422JSONResponse{DataPolicyRefusedJSONResponse: DataPolicyRefusedJSONResponse{
			Code: resp.Code, Message: resp.Message, Problems: []DataPolicyProblem{},
		}}, nil
	}
	if errors.Is(err, datapolicy.ErrNotFound) {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return RollbackDataPolicyRevision404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "revision not found"}}, nil
	}
	if refused, ok := publishRefusal(err); ok {
		h.audited(ctx, ev.Failed(audit.ReasonValidation))
		return RollbackDataPolicyRevision422JSONResponse{DataPolicyRefusedJSONResponse: refused}, nil
	}
	if errors.Is(err, datapolicy.ErrVersionConflict) {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return RollbackDataPolicyRevision409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConcurrentChange, Message: "another publish landed first; try again"}}, nil
	}
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonInternal))
		return nil, err
	}
	ev.Changes = map[string]audit.Change{"revision": {To: rev.ID.String()}}
	h.audited(ctx, ev.Allowed())
	return RollbackDataPolicyRevision201JSONResponse(revisionToPublic(rev, true)), nil
}

// rollbackNames gives a catalog a rollback restores its names today, under
// the rule publishing applies (catalogNames): a catalog now bound to an engine
// that does not enforce data policies refuses the rollback. A deleted catalog
// leaves the bundle.
func (h *DataPolicyHandlers) rollbackNames(ctx context.Context, catalogID uuid.UUID) ([]string, error) {
	cat, err := h.queries.GetCatalogByID(ctx, db.UUID{Bytes: catalogID, Valid: true})
	if errors.Is(err, db.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	conn, err := h.queries.GetCatalogConnectionByID(ctx, cat.ConnectionID)
	if err != nil {
		return nil, err
	}
	names, _, err := h.catalogNames(ctx, cat, conn)
	return names, err
}

func (h *DataPolicyHandlers) GetDataPolicyStatus(ctx context.Context, _ GetDataPolicyStatusRequestObject) (GetDataPolicyStatusResponseObject, error) {
	switch _, err := h.globalAdmin(ctx); {
	case errors.Is(err, authz.ErrUnauthenticated):
		return GetDataPolicyStatus401JSONResponse{UnauthorizedJSONResponse: dataPolicyUnauthorized()}, nil
	case errors.Is(err, authz.ErrForbidden):
		return GetDataPolicyStatus403JSONResponse{ForbiddenJSONResponse: forbiddenDataPolicyAdmin()}, nil
	}
	out := DataPolicyStatus{Enabled: h.settings.DataPolicy.Enabled, Engines: []DataPolicyEngineState{}}
	if digest := h.settings.DataPolicy.ExpectedDigest; digest != "" {
		out.ExpectedPolicyDigest = &digest
	}
	published, err := h.policies.PublishedRevision(ctx)
	switch {
	case err == nil:
		rev := revisionToPublic(published, true)
		out.Published = &rev
	case !errors.Is(err, datapolicy.ErrNotFound):
		return nil, err
	}
	engines, err := h.queries.ListEngines(ctx)
	if err != nil {
		return nil, err
	}
	statuses, err := h.policies.ListEngineStatuses(ctx)
	if err != nil {
		return nil, err
	}
	byEngine := map[uuid.UUID]datapolicy.EngineStatus{}
	for _, s := range statuses {
		byEngine[s.EngineID] = s
	}
	now := time.Now().UTC()
	leases := map[string]*time.Time{}
	for _, eng := range engines {
		if eng.Kind != "trino" {
			continue
		}
		id := uuid.UUID(eng.ID.Bytes)
		in := datapolicy.HealthInput{
			DeploymentEnabled: h.settings.DataPolicy.Enabled, EngineEnabled: eng.DataPolicies,
			ExpectedDigest: h.settings.DataPolicy.ExpectedDigest, StaleAfter: dataPolicyStatusStale, Now: now,
		}
		if s, ok := byEngine[id]; ok {
			in.Status = &s
			if in.ActiveNotAfter, err = h.revisionLease(ctx, leases, s.ActiveRevision); err != nil {
				return nil, err
			}
		}
		state := datapolicy.EngineHealth(in)
		item := DataPolicyEngineState{
			EngineId: openapi_types.UUID(id), EngineName: eng.Name, DataPolicies: eng.DataPolicies,
			Health: DataPolicyEngineStateHealth(state.Health), ReportedAt: state.ReportedAt,
		}
		if state.Reason != "" {
			item.Reason = &state.Reason
		}
		if state.ActiveRevision != "" {
			active := state.ActiveRevision
			item.ActiveRevision = &active
		}
		out.Engines = append(out.Engines, item)
	}
	return GetDataPolicyStatus200JSONResponse(out), nil
}

// revisionLease returns the lease end of the revision an engine reports
// active, or nil when the report names no revision Baleia stored; leases
// caches the lookups of one status request.
func (h *DataPolicyHandlers) revisionLease(ctx context.Context, leases map[string]*time.Time, active string) (*time.Time, error) {
	if lease, ok := leases[active]; ok {
		return lease, nil
	}
	id, err := uuid.Parse(active)
	if err != nil {
		leases[active] = nil
		return nil, nil //nolint:nilerr // a report may name no revision; health judges that on its own
	}
	rev, err := h.policies.GetRevision(ctx, id)
	if errors.Is(err, datapolicy.ErrNotFound) {
		leases[active] = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	leases[active] = &rev.NotAfter
	return &rev.NotAfter, nil
}

// ── conversions and responses ────────────────────────────────────────────────

func dataPolicyUnauthorized() UnauthorizedJSONResponse {
	return UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}
}

func forbiddenDataPolicies() ForbiddenJSONResponse {
	return ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the owner of the catalog's connection or an admin may manage its data policies"}
}

// forbiddenDataPolicyAdmin refuses the deployment-wide operations: revisions,
// rollback and status belong to global admins, not to catalog owners.
func forbiddenDataPolicyAdmin() ForbiddenJSONResponse {
	return ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only an admin may manage data policy revisions and read their status"}
}

func dataPolicyNotFound() NotFoundJSONResponse {
	return NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "data policy not found"}
}

func dataPoliciesDisabled() ConflictJSONResponse {
	return ConflictJSONResponse{Code: ErrorCodeDataPoliciesDisabled, Message: "data policies are off (BALEIA_DATA_POLICIES)"}
}

// catalogAccessResponse maps the catalog check to its response, or reports
// that the handler goes on.
func catalogAccessResponse[R any](err error, unauth, forbidden, notFound R) (R, bool) {
	var zero R
	switch {
	case errors.Is(err, authz.ErrUnauthenticated):
		return unauth, true
	case errors.Is(err, authz.ErrForbidden):
		return forbidden, true
	case errors.Is(err, authz.ErrNotFound):
		return notFound, true
	}
	return zero, false
}

// catalogTargetRefusal maps catalogNames' refusals to a 422.
func catalogTargetRefusal(err error) (UnprocessableEntityJSONResponse, bool) {
	switch {
	case errors.Is(err, errPoliciesUnenforced):
		return unprocessable(ErrorCodeDataPoliciesUnenforced, "every engine the catalog is bound to must enforce data policies, and one must"), true
	case errors.Is(err, errUnsupportedCatalog):
		return unprocessable(ErrorCodeDataPoliciesUnsupported, "data policies need a catalog whose views Baleia can read (Iceberg REST)"), true
	}
	return UnprocessableEntityJSONResponse{}, false
}

func publishRefusal(err error) (DataPolicyRefusedJSONResponse, bool) {
	var refused *datapolicy.RefusedError
	if !errors.As(err, &refused) {
		return DataPolicyRefusedJSONResponse{}, false
	}
	out := DataPolicyRefusedJSONResponse{Code: ErrorCodeDataPolicyInvalid, Message: "the data policies cannot be published", Problems: make([]DataPolicyProblem, 0, len(refused.Problems))}
	for _, p := range refused.Problems {
		problem := DataPolicyProblem{Code: DataPolicyProblemCode(p.Code)}
		if p.PolicyID != uuid.Nil {
			id := openapi_types.UUID(p.PolicyID)
			problem.PolicyId = &id
		}
		if p.Detail != "" {
			detail := p.Detail
			problem.Detail = &detail
		}
		out.Problems = append(out.Problems, problem)
	}
	return out, true
}

// validateDataPolicy normalizes p and maps its field errors to a 422.
func validateDataPolicy(p *datapolicy.Policy) (UnprocessableEntityJSONResponse, bool) {
	p.Normalize()
	err := p.Validate()
	var invalid *datapolicy.InvalidError
	if !errors.As(err, &invalid) {
		return UnprocessableEntityJSONResponse{}, false
	}
	fields := make([]FieldError, 0, len(invalid.Fields))
	for _, f := range invalid.Fields {
		fields = append(fields, FieldError{Field: f.Field, Code: FieldCode(f.Code)})
	}
	return unprocessableFields("the data policy is invalid", fields...), true
}

func dataPolicyFromRequest(b CreateDataPolicyRequest, catalogID uuid.UUID) datapolicy.Policy {
	p := datapolicy.Policy{
		Name: b.Name, Kind: datapolicy.Kind(b.Kind), Expression: b.Expression,
		Target:   datapolicy.Target{CatalogID: catalogID, Schema: b.Schema, Table: b.Table},
		Subjects: subjectsFromPublic(b.Subjects),
	}
	if b.Description != nil {
		p.Description = *b.Description
	}
	if b.Column != nil {
		p.Target.Column = *b.Column
	}
	if b.Mode != nil {
		p.Mode = datapolicy.Mode(*b.Mode)
	}
	if b.Priority != nil {
		p.Priority = *b.Priority
	}
	if b.Template != nil {
		p.Template = templateFromPublic(*b.Template)
	}
	if b.State != nil {
		p.State = datapolicy.State(*b.State)
	}
	return p
}

// The API spells the null template null_value: a bare null is not an enum
// value in OpenAPI.
func templateFromPublic(t DataPolicyTemplate) datapolicy.MaskTemplate {
	if t == DataPolicyTemplateNull {
		return datapolicy.MaskTemplateNull
	}
	return datapolicy.MaskTemplate(t)
}

func templateToPublic(t datapolicy.MaskTemplate) DataPolicyTemplate {
	if t == datapolicy.MaskTemplateNull {
		return DataPolicyTemplateNull
	}
	return DataPolicyTemplate(t)
}

func subjectsFromPublic(in []DataPolicySubject) []datapolicy.Subject {
	out := make([]datapolicy.Subject, 0, len(in))
	for _, s := range in {
		var sub datapolicy.Subject
		if s.User != nil {
			sub.User = s.User.String()
		}
		if s.Team != nil {
			sub.Team = s.Team.String()
		}
		if s.Group != nil {
			sub.Group = *s.Group
		}
		if s.ServiceAccount != nil {
			sub.ServiceAccount = s.ServiceAccount.String()
		}
		if s.Everyone != nil {
			sub.Everyone = *s.Everyone
		}
		if s.Except != nil {
			sub.Except = subjectsFromPublic(*s.Except)
		}
		out = append(out, sub)
	}
	return out
}

func subjectsToPublic(in []datapolicy.Subject) []DataPolicySubject {
	out := make([]DataPolicySubject, 0, len(in))
	for _, s := range in {
		var sub DataPolicySubject
		if id, err := uuid.Parse(s.User); err == nil {
			v := openapi_types.UUID(id)
			sub.User = &v
		}
		if id, err := uuid.Parse(s.Team); err == nil {
			v := openapi_types.UUID(id)
			sub.Team = &v
		}
		if id, err := uuid.Parse(s.ServiceAccount); err == nil {
			v := openapi_types.UUID(id)
			sub.ServiceAccount = &v
		}
		if s.Group != "" {
			g := s.Group
			sub.Group = &g
		}
		if s.Everyone {
			everyone := true
			sub.Everyone = &everyone
		}
		if len(s.Except) > 0 {
			except := subjectsToPublic(s.Except)
			sub.Except = &except
		}
		out = append(out, sub)
	}
	return out
}

func dataPolicyToPublic(p datapolicy.Policy) DataPolicy {
	actions := []DataPolicyAllowedAction{DataPolicyAllowedActionUpdate, DataPolicyAllowedActionDelete}
	out := DataPolicy{
		AllowedActions: &actions,
		Id:             openapi_types.UUID(p.ID), CatalogId: openapi_types.UUID(p.Target.CatalogID),
		Name: p.Name, Description: p.Description, Kind: DataPolicyKind(p.Kind),
		Schema: p.Target.Schema, Table: p.Target.Table, Priority: p.Priority,
		Subjects: subjectsToPublic(p.Subjects), Expression: p.Expression,
		State: DataPolicyState(p.State), Validation: DataPolicyValidation(p.Validation),
		Version: p.Version, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if p.Target.Column != "" {
		column := p.Target.Column
		out.Column = &column
	}
	if p.Mode != "" {
		mode := DataPolicyMode(p.Mode)
		out.Mode = &mode
	}
	if p.Compiled != "" {
		compiled := p.Compiled
		out.Compiled = &compiled
	}
	if p.Template != "" {
		template := templateToPublic(p.Template)
		out.Template = &template
	}
	if p.InvalidReason != "" {
		reason, detail := p.InvalidReason, p.InvalidDetail
		out.InvalidReason, out.InvalidDetail = &reason, &detail
	}
	if p.CreatedBy != uuid.Nil {
		by := openapi_types.UUID(p.CreatedBy)
		out.CreatedBy = &by
	}
	return out
}

func revisionToPublic(rev datapolicy.Revision, published bool) DataPolicyRevision {
	out := DataPolicyRevision{
		Id: openapi_types.UUID(rev.ID), Number: rev.Number, Reason: DataPolicyRevisionReason(rev.Reason),
		Digest: rev.Digest, NotAfter: rev.NotAfter, CreatedAt: rev.CreatedAt, Published: published,
	}
	if rev.SourceID != nil {
		source := openapi_types.UUID(*rev.SourceID)
		out.SourceId = &source
	}
	if rev.CreatedBy != nil {
		by := openapi_types.UUID(*rev.CreatedBy)
		out.CreatedBy = &by
	}
	return out
}
