package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/version"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Bundles (#34, ADR 0013): content-as-code. Export is deterministic; import
// upserts by (kind, slug), never deletes omitted items, supports dry_run and is
// all-or-nothing in apply.

// exportBundleResponse writes the exact bundle bytes (deterministic) with the
// download filename, bypassing the JSON encoder's re-encoding.
type exportBundleResponse struct {
	filename string
	body     []byte
}

func (r exportBundleResponse) VisitExportSpaceBundleResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r.body)
	return err
}

// importReportResponse returns the per-item report with 200 (dry_run) or 422
// (apply with errors, nothing written).
type importReportResponse struct {
	status int
	report content.ImportReport
}

func (r importReportResponse) VisitImportSpaceBundleResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(r.status)
	return json.NewEncoder(w).Encode(importReportToPublic(r.report))
}

// ExportSpaceBundle — GET /workspaces/{id}/spaces/{space_id}/export
func (h *SemanticHandlers) ExportSpaceBundle(ctx context.Context, request ExportSpaceBundleRequestObject) (ExportSpaceBundleResponseObject, error) {
	ws, spaceID := workspaceIDFromRequest(request.Id), spaceIDFromRequest(request.SpaceId)
	if se, err := h.contentSpace(ctx, ws, spaceID, authz.SpaceViewer); err != nil {
		if unauthorized(err) {
			return ExportSpaceBundle401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return ExportSpaceBundle403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "not allowed to read this space"}}, nil
		}
		return ExportSpaceBundle404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	}
	bundle, err := h.contentService().Export(ctx, ws, spaceID, version.Version)
	if err != nil {
		return nil, err
	}
	raw, err := bundle.Marshal()
	if err != nil {
		return nil, err
	}
	return exportBundleResponse{filename: bundle.Space.Slug + ".baleia.json", body: raw}, nil
}

// ImportSpaceBundle — POST /workspaces/{id}/spaces/{space_id}/import
func (h *SemanticHandlers) ImportSpaceBundle(ctx context.Context, request ImportSpaceBundleRequestObject) (ImportSpaceBundleResponseObject, error) {
	ws, spaceID := workspaceIDFromRequest(request.Id), spaceIDFromRequest(request.SpaceId)
	if se, err := h.contentSpace(ctx, ws, spaceID, authz.SpaceEditor); err != nil {
		if unauthorized(err) {
			return ImportSpaceBundle401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
		}
		return nil, err
	} else if se != nil {
		if se.status == 403 {
			return ImportSpaceBundle403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "you may not edit this space"}}, nil
		}
		return ImportSpaceBundle404JSONResponse{NotFoundJSONResponse: metadataNotFound("space not found")}, nil
	}
	isManager := false
	if se, err := h.contentSpace(ctx, ws, spaceID, authz.SpaceManager); err == nil && se == nil {
		isManager = true
	}
	if request.Body == nil {
		return ImportSpaceBundle422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidBundle, "missing bundle body")}, nil
	}
	raw, err := json.Marshal(request.Body)
	if err != nil {
		return ImportSpaceBundle422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidBundle, "the bundle is not valid JSON")}, nil
	}
	var bundle content.Bundle
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bundle); err != nil {
		return ImportSpaceBundle422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeInvalidBundle, err.Error())}, nil
	}

	mode := content.ImportDryRun
	if request.Params.Mode != nil && *request.Params.Mode == "apply" {
		mode = content.ImportApply
	}
	actor, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	report, err := h.contentService().Import(ctx, content.ImportOptions{
		WorkspaceID:        ws,
		SpaceID:            spaceID,
		Actor:              actor,
		Bundle:             bundle,
		Mode:               mode,
		AllowCertification: isManager,
		Dialect:            h.dialects.MustGet(dialect.Trino),
		ValidateMetric: func(catalogID pgtype.UUID, namespace []string, table string, definition json.RawMessage, bindingID pgtype.UUID) error {
			def, _, err := parseDefinitionRaw(definition)
			if err != nil {
				return err
			}
			return h.validateSemanticMetric(ctx, ws, catalogID, bindingID, actor, def, namespace, table, "metric")
		},
	})
	if errors.Is(err, content.ErrImportFailed) {
		return importReportResponse{status: http.StatusUnprocessableEntity, report: report}, nil
	}
	if err != nil {
		return nil, err
	}
	return importReportResponse{status: http.StatusOK, report: report}, nil
}

func importReportToPublic(r content.ImportReport) ImportReport {
	out := ImportReport{
		Mode:  ImportReportMode(r.Mode),
		Ok:    r.OK,
		Items: make([]ImportItem, 0, len(r.Items)),
	}
	for _, it := range r.Items {
		item := ImportItem{Kind: it.Kind, Slug: it.Slug, Action: ImportItemAction(it.Action)}
		if it.Title != "" {
			title := it.Title
			item.Title = &title
		}
		if it.MatchedID != "" {
			if matched, err := uuidFromString(it.MatchedID); err == nil {
				id := openapi_types.UUID(matched.Bytes)
				item.MatchedId = &id
			}
		}
		if it.Message != "" {
			msg := it.Message
			item.Message = &msg
		}
		if len(it.Warnings) > 0 {
			w := it.Warnings
			item.Warnings = &w
		}
		out.Items = append(out.Items, item)
	}
	return out
}
