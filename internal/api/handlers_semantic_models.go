package api

// Semantic model v2 (#91): read the models of a workspace and compile an
// explore query to SQL. The model editor's write endpoints land in the next
// commit; the compiler and storage are already in place.

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/semantic"
	"github.com/galileostudio/baleia/internal/semantic/compiler"
	"github.com/galileostudio/baleia/internal/semantic/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ListSemanticModels — GET /workspaces/{id}/semantic/models
func (h *SemanticHandlers) ListSemanticModels(ctx context.Context, request ListSemanticModelsRequestObject) (ListSemanticModelsResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return ListSemanticModels404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	spaceID := pgtype.UUID{}
	if request.Params.SpaceId != nil {
		spaceID = pgtype.UUID{Bytes: *request.Params.SpaceId, Valid: true}
	}
	rows, err := h.modelDefs.List(ctx, ws, userID, spaceID)
	if err != nil {
		return nil, err
	}
	out := make([]SemanticModel, 0, len(rows))
	for _, m := range rows {
		pub := dbSemanticModelToPublic(m, h.semanticModelVersion(ctx, ws, m.ID))
		acts := h.semanticModelActions(ctx, ws, m.SpaceID)
		pub.AllowedActions = &acts
		out = append(out, pub)
	}
	return ListSemanticModels200JSONResponse(out), nil
}

// GetSemanticModel — GET /workspaces/{id}/semantic/models/{model_id}
func (h *SemanticHandlers) GetSemanticModel(ctx context.Context, request GetSemanticModelRequestObject) (GetSemanticModelResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return GetSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	model, err := h.modelDefs.Get(ctx, ws, pgtype.UUID{Bytes: request.ModelId, Valid: true})
	if errors.Is(err, models.ErrNotFound) {
		return GetSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("model not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if se, err := h.contentSpace(ctx, ws, model.SpaceID, authz.SpaceViewer); err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return GetSemanticModel401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return GetSemanticModel403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to read this space"}}, nil
		}
		return GetSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("model not found")}, nil
	}
	out := dbSemanticModelToPublic(model, h.semanticModelVersion(ctx, ws, model.ID))
	acts := h.semanticModelActions(ctx, ws, model.SpaceID)
	out.AllowedActions = &acts
	return GetSemanticModel200JSONResponse(out), nil
}

// CompileExploreQuery — POST /workspaces/{id}/semantic/explore/compile
func (h *SemanticHandlers) CompileExploreQuery(ctx context.Context, request CompileExploreQueryRequestObject) (CompileExploreQueryResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return CompileExploreQuery404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if request.Body == nil {
		return nil, errors.New("semantic explore: missing body")
	}
	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	rows, err := h.modelDefs.List(ctx, ws, userID, pgtype.UUID{})
	if err != nil {
		return nil, err
	}
	modelSet := make(map[string]semantic.Model, len(rows))
	var baseRow *db.SemanticModel
	for i := range rows {
		m, err := models.ParseStored(rows[i].Definition)
		if err != nil {
			return CompileExploreQuery422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, "model "+rows[i].Slug+": "+err.Error())}, nil
		}
		// The row is authoritative: a definition written through the API carries
		// neither the slug (renames leave it stale) nor the binding context.
		m.Slug = rows[i].Slug
		m.CatalogID = uuid.UUID(rows[i].CatalogID.Bytes).String()
		if rows[i].BindingID.Valid {
			m.BindingID = uuid.UUID(rows[i].BindingID.Bytes).String()
		}
		modelSet[m.Slug] = m
		if m.Slug == request.Body.Query.Model {
			baseRow = &rows[i]
		}
	}
	if baseRow == nil {
		return CompileExploreQuery404JSONResponse{NotFoundJSONResponse: metadataNotFound("model not found")}, nil
	}
	q := exploreQueryFromRequest(request.Body.Query)

	bindingID := baseRow.BindingID
	if request.Body.BindingId != nil {
		bindingID = pgtype.UUID{Bytes: *request.Body.BindingId, Valid: true}
	}
	target, engine, err := h.semanticTarget(ctx, ws, baseRow.CatalogID, bindingID)
	if err != nil {
		if te, ok := asTargetError(err); ok {
			if te.notFound {
				return CompileExploreQuery404JSONResponse{NotFoundJSONResponse: metadataNotFound(te.message)}, nil
			}
			return CompileExploreQuery422JSONResponse{UnprocessableEntityJSONResponse: te.unprocessable()}, nil
		}
		return nil, err
	}
	res, err := compiler.Compile(q, modelSet, engine.Dialect(), compiler.Options{Catalog: target.EngineCatalog})
	if err != nil {
		// The query is valid but needs something the compiler or the engine's
		// dialect cannot express (a missing primitive, a fan-out it cannot
		// resolve): a distinct code, so the UI tells it apart from a mistake.
		if errors.Is(err, compiler.ErrUnsupported) || errors.Is(err, dialect.ErrUnsupported) {
			return CompileExploreQuery422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeNotSupported, err.Error())}, nil
		}
		if errors.Is(err, semantic.ErrInvalidExplore) || errors.Is(err, dialect.ErrInvalid) {
			return CompileExploreQuery422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, err.Error())}, nil
		}
		return nil, err
	}
	notes := res.Notes
	return CompileExploreQuery200JSONResponse(ExploreCompileResult{
		Sql:     res.SQL,
		Notes:   &notes,
		Dialect: engine.Dialect().Name(),
	}), nil
}

// semanticModelVersion returns the latest content version, or 0 when the model
// has never been versioned (e.g. seeded outside the content service).
func (h *core) semanticModelVersion(ctx context.Context, ws, id pgtype.UUID) int32 {
	v, err := h.queries.LatestContentVersion(ctx, db.LatestContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticModel, EntityID: id,
	})
	if err != nil {
		return 0
	}
	return v.Version
}

func (h *core) semanticModelActions(ctx context.Context, ws, spaceID pgtype.UUID) []string {
	acts := []string{"view"}
	if se, err := h.contentSpace(ctx, ws, spaceID, authz.SpaceEditor); err == nil && se == nil {
		acts = append(acts, "update", "delete")
	}
	if se, err := h.contentSpace(ctx, ws, spaceID, authz.SpaceManager); err == nil && se == nil {
		acts = append(acts, "certify")
	}
	return acts
}

func dbSemanticModelToPublic(m db.SemanticModel, version int32) SemanticModel {
	out := SemanticModel{
		Id:            openapi_types.UUID(m.ID.Bytes),
		WorkspaceId:   openapi_types.UUID(m.WorkspaceID.Bytes),
		Slug:          m.Slug,
		Label:         m.Label,
		Description:   &m.Description,
		Certified:     m.Certified,
		CatalogId:     openapi_types.UUID(m.CatalogID.Bytes),
		FormatVersion: int(m.FormatVersion),
		Version:       int(version),
		UpdatedAt:     pgTimestampToTime(m.UpdatedAt),
	}
	var def map[string]any
	if err := json.Unmarshal(m.Definition, &def); err == nil && def != nil {
		out.Definition = def
	} else {
		out.Definition = map[string]any{}
	}
	if m.SpaceID.Valid {
		v := openapi_types.UUID(m.SpaceID.Bytes)
		out.SpaceId = &v
	}
	if m.BindingID.Valid {
		v := openapi_types.UUID(m.BindingID.Bytes)
		out.BindingId = &v
	}
	if m.OwnerUserID.Valid {
		v := openapi_types.UUID(m.OwnerUserID.Bytes)
		out.OwnerUserId = &v
	}
	if m.UpdatedBy.Valid {
		v := openapi_types.UUID(m.UpdatedBy.Bytes)
		out.UpdatedBy = &v
	}
	if m.CertifiedBy.Valid {
		v := openapi_types.UUID(m.CertifiedBy.Bytes)
		out.CertifiedBy = &v
	}
	if m.CertifiedAt.Valid {
		v := pgTimestampToTime(m.CertifiedAt)
		out.CertifiedAt = &v
	}
	return out
}

// exploreQueryFromRequest converts the OpenAPI shape into the compiler input.
func exploreQueryFromRequest(in ExploreQuery) semantic.ExploreQuery {
	q := semantic.ExploreQuery{
		Model:     in.Model,
		TimeGrain: derefString(in.TimeGrain),
		Limit:     derefInt(in.Limit),
	}
	q.Dimensions = derefStrings(in.Dimensions)
	q.Metrics = derefStrings(in.Metrics)
	if in.Filters != nil {
		for _, f := range *in.Filters {
			q.Filters = append(q.Filters, semantic.ExploreFilter{
				Field:  f.Field,
				Op:     semantic.FilterOp(f.Op),
				Values: derefAny(f.Values),
			})
		}
	}
	if in.Sorts != nil {
		for _, s := range *in.Sorts {
			q.Sorts = append(q.Sorts, semantic.Sort{Field: s.Field, Desc: derefBool(s.Desc)})
		}
	}
	return q
}

func derefStrings(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

func derefAny(p *[]any) []any {
	if p == nil {
		return nil
	}
	return *p
}

// CreateSemanticModel — POST /workspaces/{id}/semantic/models
func (h *SemanticHandlers) CreateSemanticModel(ctx context.Context, request CreateSemanticModelRequestObject) (CreateSemanticModelResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return CreateSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if request.Body == nil {
		return nil, errors.New("semantic model: missing body")
	}
	catalogID := pgtype.UUID{Bytes: request.Body.CatalogId, Valid: true}
	nf, err = h.semanticCatalog(ctx, ws, request.Body.CatalogId)
	if err != nil {
		return nil, err
	}
	if nf {
		return CreateSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}
	f := models.Fields{
		Slug:      request.Body.Slug,
		Label:     request.Body.Label,
		CatalogID: catalogID,
		BindingID: emptyUUID(request.Body.BindingId),
		OwnerID:   emptyUUID(request.Body.OwnerUserId),
	}
	if request.Body.Description != nil {
		f.Description = *request.Body.Description
	}
	if request.Body.Certified != nil {
		f.Certified = *request.Body.Certified
	}
	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	prepared, err := h.modelDefs.PrepareNew(ctx, ws, userID, f, request.Body.Definition)
	var (
		invalidDef   *models.InvalidDefinitionError
		invalidField *models.InvalidFieldError
	)
	switch {
	case errors.As(err, &invalidField):
		return CreateSemanticModel422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalidField.Msg)}, nil
	case errors.As(err, &invalidDef):
		return CreateSemanticModel422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, invalidDef.Error())}, nil
	case err != nil:
		return nil, err
	}

	var space db.Space
	if request.Body.SpaceId != nil {
		var se *spaceErr
		space, se, err = h.spaceForWrite(ctx, ws, request.Body.SpaceId, authz.SpaceEditor)
		if err != nil {
			if errors.Is(err, authz.ErrUnauthenticated) {
				return CreateSemanticModel401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
			}
			return nil, err
		}
		if se != nil {
			if se.status == 403 {
				return CreateSemanticModel403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
			}
			return CreateSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
		}
	} else {
		space, err = h.queries.GetDefaultSpace(ctx, ws)
		if err != nil {
			return nil, err
		}
		if se, err := h.contentSpace(ctx, ws, space.ID, authz.SpaceEditor); err != nil {
			if errors.Is(err, authz.ErrUnauthenticated) {
				return CreateSemanticModel401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
			}
			return nil, err
		} else if se != nil {
			if se.status == 403 {
				return CreateSemanticModel403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
			}
			return CreateSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
		}
	}
	isManager := false
	if se, err := h.contentSpace(ctx, ws, space.ID, authz.SpaceManager); err == nil && se == nil {
		isManager = true
	}
	model, err := h.modelDefs.Create(ctx, userID, ws, space.ID, prepared, isManager)
	switch {
	case errors.Is(err, models.ErrCertifyForbidden):
		return CreateSemanticModel403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: err.Error()}}, nil
	case errors.Is(err, models.ErrSlugTaken):
		return CreateSemanticModel409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: err.Error()}}, nil
	case err != nil:
		return nil, err
	}
	out := dbSemanticModelToPublic(model, 1)
	acts := h.semanticModelActions(ctx, ws, model.SpaceID)
	out.AllowedActions = &acts
	return CreateSemanticModel201JSONResponse(out), nil
}

// UpdateSemanticModel — PATCH /workspaces/{id}/semantic/models/{model_id}
func (h *SemanticHandlers) UpdateSemanticModel(ctx context.Context, request UpdateSemanticModelRequestObject) (UpdateSemanticModelResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return UpdateSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	if request.Body == nil {
		return nil, errors.New("semantic model: missing body")
	}
	modelID := pgtype.UUID{Bytes: request.ModelId, Valid: true}
	current, err := h.modelDefs.Get(ctx, ws, modelID)
	if errors.Is(err, models.ErrNotFound) {
		return UpdateSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("model not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if se, err := h.contentSpace(ctx, ws, current.SpaceID, authz.SpaceEditor); err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return UpdateSemanticModel401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return UpdateSemanticModel403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
		}
		return UpdateSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("model not found")}, nil
	}
	isManager := false
	if se, err := h.contentSpace(ctx, ws, current.SpaceID, authz.SpaceManager); err == nil && se == nil {
		isManager = true
	}
	body := request.Body
	change := models.Change{
		Slug: body.Slug, Label: body.Label, Description: body.Description, Certified: body.Certified,
		ChangesDefinition: body.Definition != nil,
	}
	if body.CatalogId != nil {
		change.CatalogID = &pgtype.UUID{Bytes: *body.CatalogId, Valid: true}
	}
	if body.BindingId != nil {
		change.BindingID = &pgtype.UUID{Bytes: *body.BindingId, Valid: true}
	}
	if body.OwnerUserId != nil {
		change.OwnerID = &pgtype.UUID{Bytes: *body.OwnerUserId, Valid: true}
	}
	fields, err := models.MergeFields(current, change, isManager)
	var invalidField *models.InvalidFieldError
	switch {
	case errors.Is(err, models.ErrCertifyForbidden):
		return UpdateSemanticModel403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: err.Error()}}, nil
	case errors.As(err, &invalidField):
		return UpdateSemanticModel422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, invalidField.Msg)}, nil
	case err != nil:
		return nil, err
	}
	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	nf, err = h.semanticCatalog(ctx, ws, openapi_types.UUID(fields.CatalogID.Bytes))
	if err != nil {
		return nil, err
	}
	if nf {
		return UpdateSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("catalog not found")}, nil
	}
	prepared, err := h.modelDefs.PrepareUpdate(ctx, ws, userID, current, fields, body.Definition)
	var invalidDef *models.InvalidDefinitionError
	if errors.As(err, &invalidDef) {
		return UpdateSemanticModel422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, invalidDef.Error())}, nil
	}
	if err != nil {
		return nil, err
	}

	var expected *int32
	if body.ExpectedVersion != nil {
		v := int32(*body.ExpectedVersion)
		expected = &v
	}
	updated, err := h.modelDefs.Update(ctx, userID, current, prepared, expected)
	if errors.Is(err, models.ErrSlugTaken) {
		return UpdateSemanticModel409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeConflict, Message: err.Error()}}, nil
	}
	if err != nil {
		if c, ok := h.versionConflictResponse(ctx, err); ok {
			return UpdateSemanticModel409JSONResponse{ConflictJSONResponse: c}, nil
		}
		return nil, err
	}
	out := dbSemanticModelToPublic(updated, h.semanticModelVersion(ctx, ws, modelID))
	acts := h.semanticModelActions(ctx, ws, updated.SpaceID)
	out.AllowedActions = &acts
	return UpdateSemanticModel200JSONResponse(out), nil
}

// DeleteSemanticModel — DELETE /workspaces/{id}/semantic/models/{model_id}
func (h *SemanticHandlers) DeleteSemanticModel(ctx context.Context, request DeleteSemanticModelRequestObject) (DeleteSemanticModelResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return DeleteSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	modelID := pgtype.UUID{Bytes: request.ModelId, Valid: true}
	current, err := h.modelDefs.Get(ctx, ws, modelID)
	if errors.Is(err, models.ErrNotFound) {
		return DeleteSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("model not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if se, err := h.contentSpace(ctx, ws, current.SpaceID, authz.SpaceEditor); err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) {
			return DeleteSemanticModel401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return DeleteSemanticModel403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
		}
		return DeleteSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("model not found")}, nil
	}
	actor, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if err := h.modelDefs.Delete(ctx, actor, ws, modelID); err != nil {
		if errors.Is(err, content.ErrNotFound) {
			return DeleteSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("model not found")}, nil
		}
		return nil, err
	}
	return DeleteSemanticModel204Response{}, nil
}

// ── model versions (#34) ────────────────────────────────────────────────────────

func (h *core) modelSpaceForHistory(ctx context.Context, ws, id pgtype.UUID) (pgtype.UUID, error) {
	m, err := h.queries.GetSemanticModel(ctx, db.GetSemanticModelParams{WorkspaceID: ws, ID: id})
	if err == nil {
		return m.SpaceID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	v, err := h.queries.LatestContentVersion(ctx, db.LatestContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticModel, EntityID: id,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return v.SpaceID, nil
}

// ListSemanticModelVersions — GET /workspaces/{id}/semantic/models/{model_id}/versions
func (h *SemanticHandlers) ListSemanticModelVersions(ctx context.Context, request ListSemanticModelVersionsRequestObject) (ListSemanticModelVersionsResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	id := pgtype.UUID{Bytes: request.ModelId, Valid: true}
	spaceID, err := h.modelSpaceForHistory(ctx, ws, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ListSemanticModelVersions404JSONResponse{NotFoundJSONResponse: metadataNotFound("model not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	se, err := h.contentSpace(ctx, ws, spaceID, authz.SpaceViewer)
	if err != nil {
		if unauthorized(err) {
			return ListSemanticModelVersions401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return ListSemanticModelVersions403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to read this space"}}, nil
		}
		return ListSemanticModelVersions404JSONResponse{NotFoundJSONResponse: metadataNotFound("model not found")}, nil
	}
	lim, off := versionPage(request.Params.Limit, request.Params.Offset)
	rows, err := h.queries.ListContentVersions(ctx, db.ListContentVersionsParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticModel, EntityID: id, Lim: lim, Off: off,
	})
	if err != nil {
		return nil, err
	}
	out := make(ListSemanticModelVersions200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, contentVersionToPublic(r.Version, r.ChangeKind, r.AuthorID, r.AuthorName, r.RestoredFrom, r.CreatedAt))
	}
	return out, nil
}

// GetSemanticModelVersion — GET /workspaces/{id}/semantic/models/{model_id}/versions/{version}
func (h *SemanticHandlers) GetSemanticModelVersion(ctx context.Context, request GetSemanticModelVersionRequestObject) (GetSemanticModelVersionResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	id := pgtype.UUID{Bytes: request.ModelId, Valid: true}
	v, err := h.queries.GetContentVersion(ctx, db.GetContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticModel, EntityID: id, Version: int32(request.Version),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return GetSemanticModelVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	se, err := h.contentSpace(ctx, ws, v.SpaceID, authz.SpaceViewer)
	if err != nil {
		if unauthorized(err) {
			return GetSemanticModelVersion401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	}
	if se != nil {
		if se.status == 403 {
			return GetSemanticModelVersion403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to read this space"}}, nil
		}
		return GetSemanticModelVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	detail, err := contentVersionDetail(v.Version, v.ChangeKind, v.AuthorID, v.AuthorName, v.RestoredFrom, v.CreatedAt, v.Snapshot)
	if err != nil {
		return nil, err
	}
	return GetSemanticModelVersion200JSONResponse(detail), nil
}

// RestoreSemanticModelVersion — POST /workspaces/{id}/semantic/models/{model_id}/versions/{version}/restore
func (h *SemanticHandlers) RestoreSemanticModelVersion(ctx context.Context, request RestoreSemanticModelVersionRequestObject) (RestoreSemanticModelVersionResponseObject, error) {
	ws := workspaceIDFromRequest(request.Id)
	id := pgtype.UUID{Bytes: request.ModelId, Valid: true}
	v, err := h.queries.GetContentVersion(ctx, db.GetContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticModel, EntityID: id, Version: int32(request.Version),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RestoreSemanticModelVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	if err != nil {
		return nil, err
	}
	if se, err := h.contentSpace(ctx, ws, v.SpaceID, authz.SpaceEditor); err != nil {
		if unauthorized(err) {
			return RestoreSemanticModelVersion401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return RestoreSemanticModelVersion403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
		}
		return RestoreSemanticModelVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	}
	var spaceID *pgtype.UUID
	if request.Body != nil && request.Body.SpaceId != nil {
		dest := pgtype.UUID{Bytes: *request.Body.SpaceId, Valid: true}
		if se, err := h.contentSpace(ctx, ws, dest, authz.SpaceEditor); err != nil {
			return nil, err
		} else if se != nil {
			if se.status == 403 {
				return RestoreSemanticModelVersion403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit the destination space"}}, nil
			}
			return RestoreSemanticModelVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("destination space not found")}, nil
		}
		spaceID = &dest
	}
	snap, err := content.ParseModelSnapshot(v.Snapshot)
	if err != nil {
		return RestoreSemanticModelVersion422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, err.Error())}, nil
	}
	def, err := models.ParseStored(snap.Definition)
	if err != nil {
		return RestoreSemanticModelVersion422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, err.Error())}, nil
	}
	def.CatalogID = snap.CatalogID.String()
	def.Slug = snap.Slug
	actor, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	visible, err := h.modelDefs.Visible(ctx, ws, actor)
	if err != nil {
		return nil, err
	}
	// The model being restored is not a join target for itself: drop both the
	// snapshot slug and the live row's current slug.
	delete(visible, def.Slug)
	if cur, cerr := h.queries.GetSemanticModel(ctx, db.GetSemanticModelParams{WorkspaceID: ws, ID: id}); cerr == nil {
		delete(visible, cur.Slug)
	}
	if err := def.Validate(visible); err != nil {
		return RestoreSemanticModelVersion422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, err.Error())}, nil
	}
	var newSlug *string
	if request.Body != nil {
		newSlug = request.Body.NewSlug
	}
	model, err := h.contentService().RestoreSemanticModel(ctx, actor, ws, id, int32(request.Version), newSlug, spaceID)
	switch {
	case errors.Is(err, content.ErrNameConflict):
		return RestoreSemanticModelVersion409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeNameConflict, Message: "another model already uses this slug"}}, nil
	case errors.Is(err, content.ErrSpaceMissing):
		return RestoreSemanticModelVersion409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{Code: ErrorCodeSpaceMissing, Message: "the version's space no longer exists; pass space_id"}}, nil
	case errors.Is(err, content.ErrNotFound):
		return RestoreSemanticModelVersion404JSONResponse{NotFoundJSONResponse: metadataNotFound("version not found")}, nil
	case err != nil:
		return nil, err
	}
	out := dbSemanticModelToPublic(model, h.semanticModelVersion(ctx, ws, model.ID))
	acts := h.semanticModelActions(ctx, ws, model.SpaceID)
	out.AllowedActions = &acts
	return RestoreSemanticModelVersion200JSONResponse(out), nil
}
