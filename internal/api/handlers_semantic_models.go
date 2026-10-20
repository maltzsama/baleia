package api

// Semantic model v2 (#91): read the models of a workspace and compile an
// explore query to SQL. The model editor's write endpoints land in the next
// commit; the compiler and storage are already in place.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/semantic"
	"github.com/galileostudio/baleia/internal/semantic/compiler"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ListSemanticModels — GET /workspaces/{id}/semantic/models
func (h *Handlers) ListSemanticModels(ctx context.Context, request ListSemanticModelsRequestObject) (ListSemanticModelsResponseObject, error) {
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
	rows, err := h.queries.ListVisibleSemanticModels(ctx, db.ListVisibleSemanticModelsParams{
		WorkspaceID: ws, UserID: userID, SpaceID: spaceID,
	})
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
func (h *Handlers) GetSemanticModel(ctx context.Context, request GetSemanticModelRequestObject) (GetSemanticModelResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return GetSemanticModel404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	model, err := h.queries.GetSemanticModel(ctx, db.GetSemanticModelParams{
		WorkspaceID: ws, ID: pgtype.UUID{Bytes: request.ModelId, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
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
func (h *Handlers) CompileExploreQuery(ctx context.Context, request CompileExploreQueryRequestObject) (CompileExploreQueryResponseObject, error) {
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
	rows, err := h.queries.ListVisibleSemanticModels(ctx, db.ListVisibleSemanticModelsParams{
		WorkspaceID: ws, UserID: userID,
	})
	if err != nil {
		return nil, err
	}
	models := make(map[string]semantic.Model, len(rows))
	var baseRow *db.SemanticModel
	for i := range rows {
		m, err := parseSemanticModel(rows[i].Definition)
		if err != nil {
			return CompileExploreQuery422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidDefinition, "model "+rows[i].Slug+": "+err.Error())}, nil
		}
		// The row is authoritative for the binding context.
		m.CatalogID = uuid.UUID(rows[i].CatalogID.Bytes).String()
		if rows[i].BindingID.Valid {
			m.BindingID = uuid.UUID(rows[i].BindingID.Bytes).String()
		}
		models[m.Slug] = m
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
	res, err := compiler.Compile(q, models, engine.Dialect(), compiler.Options{Catalog: target.EngineCatalog})
	if err != nil {
		if errors.Is(err, semantic.ErrInvalidExplore) || errors.Is(err, compiler.ErrUnsupported) {
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
func (h *Handlers) semanticModelVersion(ctx context.Context, ws, id pgtype.UUID) int32 {
	v, err := h.queries.LatestContentVersion(ctx, db.LatestContentVersionParams{
		WorkspaceID: ws, EntityType: content.EntitySemanticModel, EntityID: id,
	})
	if err != nil {
		return 0
	}
	return v.Version
}

func (h *Handlers) semanticModelActions(ctx context.Context, ws, spaceID pgtype.UUID) []string {
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
	var def map[string]interface{}
	if err := json.Unmarshal(m.Definition, &def); err == nil && def != nil {
		out.Definition = def
	} else {
		out.Definition = map[string]interface{}{}
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

// parseSemanticModel decodes the stored definition and rejects unknown fields.
func parseSemanticModel(raw []byte) (semantic.Model, error) {
	var m semantic.Model
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return semantic.Model{}, err
	}
	return m, nil
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

func derefAny(p *[]interface{}) []any {
	if p == nil {
		return nil
	}
	return *p
}
