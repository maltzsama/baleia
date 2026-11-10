package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/galileostudio/baleia/internal/db"
)

// biFileResponse writes the data source with its download filename (#713).
type biFileResponse struct {
	filename string
	body     string
}

func (r biFileResponse) VisitGetWorkspaceBIFileResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte(r.body))
	return err
}

// GetWorkspaceBIFile — GET /workspaces/{id}/bi/{tool} (#713). The file points
// the BI tool at the BI gateway (ADR 0017) with the workspace's engine catalog
// and, optionally, schema and table. It carries no credentials: the tool signs
// in with a workspace-bound BI key. Only Trino targets produce a file, because
// the gateway relays Trino's protocol and nothing else.
func (h *WorkspaceHandlers) GetWorkspaceBIFile(ctx context.Context, request GetWorkspaceBIFileRequestObject) (GetWorkspaceBIFileResponseObject, error) {
	if err := requireWorkspaceAccess(ctx, h.access, request.Id); errors.Is(err, ErrWorkspaceNotFound) {
		return GetWorkspaceBIFile404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "workspace not found"}}, nil
	} else if err != nil {
		return nil, err
	}
	gateway := h.settings.BIGateway
	if !gateway.Enabled {
		return GetWorkspaceBIFile422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(
			ErrorCodeValidationError, "the BI gateway is not enabled",
		)}, nil
	}
	wsID := workspaceIDFromRequest(request.Id)
	ws, err := h.access.Workspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	if !ws.BIEnabled {
		return GetWorkspaceBIFile403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodePermissionDenied, Message: "BI is disabled for this workspace"}}, nil
	}
	if store := h.biSettings(); store != nil {
		setting, err := store.GetBIGatewaySetting(ctx)
		if err != nil && !errors.Is(err, db.ErrNoRows) {
			return nil, err
		}
		if err == nil && !setting.Enabled {
			return GetWorkspaceBIFile403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodePermissionDenied, Message: "the BI gateway is disabled"}}, nil
		}
	}
	target, ok, err := h.biFileTarget(ctx, wsID, request)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetWorkspaceBIFile404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: ErrorCodeNotFound, Message: "target not found"}}, nil
	}
	if target.EngineKind != "trino" {
		return GetWorkspaceBIFile422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
			"the BI gateway serves Trino targets only", FieldError{Field: "engine_id", Code: FieldCodeNotAllowed},
		)}, nil
	}
	external, err := url.Parse(gateway.URL)
	if err != nil || external.Host == "" || (external.Path != "" && external.Path != "/") {
		// The gateway serves Trino's protocol at the server root and the file
		// can only carry a host and port, so a path prefix cannot be expressed.
		return GetWorkspaceBIFile422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(
			ErrorCodeValidationError, "the BI gateway public URL must be an origin with no path",
		)}, nil
	}
	port := external.Port()
	if port == "" {
		if external.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	var schema, table string
	if request.Params.Schema != nil {
		schema = *request.Params.Schema
	}
	if request.Params.Table != nil {
		table = *request.Params.Table
	}
	body, filename, ok := biFile(request.Tool, external.Hostname(), port, target.EngineCatalogName, schema, table)
	if !ok {
		return GetWorkspaceBIFile422JSONResponse{UnprocessableEntityJSONResponse: unprocessableFields(
			"tool must be tableau or powerbi", FieldError{Field: "tool", Code: FieldCodeNotAllowed},
		)}, nil
	}
	return biFileResponse{filename: filename, body: body}, nil
}

// biFileTarget resolves the workspace target the file points at: an enabled
// binding on the requested engine whose engine catalog is the requested one.
// ok is false when the workspace has no such target, so the caller answers 404
// instead of revealing whether the catalog exists.
func (h *WorkspaceHandlers) biFileTarget(ctx context.Context, wsID db.UUID, request GetWorkspaceBIFileRequestObject) (db.ListWorkspaceTargetsRow, bool, error) {
	rows, err := h.access.Targets(ctx, wsID)
	if err != nil {
		return db.ListWorkspaceTargetsRow{}, false, err
	}
	engineID := db.UUID{Bytes: request.Params.EngineId, Valid: true}
	for _, r := range rows {
		if r.EngineID.Bytes != engineID.Bytes || !usable(r) {
			continue
		}
		if r.EngineCatalogName == request.Params.Catalog {
			return r, true, nil
		}
	}
	return db.ListWorkspaceTargetsRow{}, false, nil
}

// biFile renders the Tableau (.tds) or Power BI (.pbids) data source pointing
// at the gateway. It holds no credentials (ADR 0017). The gateway, not the
// file, decides the workspace scope and the read-only session.
func biFile(tool, host, port, catalog, schema, table string) (string, string, bool) {
	name := table
	if name == "" {
		name = catalog
	}
	switch tool {
	case "tableau":
		esc := html.EscapeString
		conn := fmt.Sprintf("<connection class='trino' server='%s' port='%s' catalog='%s'",
			esc(host), esc(port), esc(catalog))
		if schema != "" {
			conn += fmt.Sprintf(" schema='%s'", esc(schema))
		}
		conn += " />"
		relation := ""
		if table != "" {
			qualified := table
			if schema != "" {
				qualified = schema + "." + table
			}
			relation = fmt.Sprintf("  <relation name='%s' table='%s' type='table' />\n", esc(table), esc(qualified))
		}
		body := fmt.Sprintf("<?xml version='1.0' encoding='utf-8' ?>\n"+
			"<datasource formatted-name='%s' inline='true' version='18.1'>\n"+
			"  %s\n"+
			"%s"+
			"</datasource>\n",
			esc(name), conn, relation)
		return body, biFilename(name, "tds"), true
	case "powerbi":
		doc := map[string]any{
			"version": "0.1",
			"connections": []any{map[string]any{
				"details": map[string]any{
					"protocol": "trino",
					"address":  map[string]any{"host": host, "port": port},
					"database": catalog,
					"schema":   schema,
				},
				"options": map[string]any{},
				"mode":    "DirectQuery",
			}},
		}
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return "", "", false
		}
		return string(raw) + "\n", biFilename(name, "pbids"), true
	default:
		return "", "", false
	}
}

// biFilename keeps the download name to letters, digits and a few separators,
// so a table name can never inject a header or a path.
func biFilename(name, extension string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, name)
	clean = strings.Trim(clean, "-.")
	if clean == "" {
		clean = "datasource"
	}
	return clean + "." + extension
}
