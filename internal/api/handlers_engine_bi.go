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

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/jackc/pgx/v5/pgtype"
)

// biFileResponse writes the data source with its download filename (#560).
type biFileResponse struct {
	filename string
	body     string
}

func (r biFileResponse) VisitGetEngineBIFileResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte(r.body))
	return err
}

// GetEngineBIFile — GET /engines/{id}/bi/{tool} (#560). The file carries no
// credentials: the user signs in from the BI tool, which connects directly to
// the engine (ADR 0001).
func (h *CatalogHandlers) GetEngineBIFile(ctx context.Context, request GetEngineBIFileRequestObject) (GetEngineBIFileResponseObject, error) {
	p, err := authz.PrincipalFromContext(ctx)
	if err != nil {
		return GetEngineBIFile401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	id := pgtype.UUID{Bytes: request.Id, Valid: true}
	eng, _, err := h.authz().Engine(ctx, p, id, authz.ActRead)
	switch {
	case errors.Is(err, authz.ErrNotFound):
		return GetEngineBIFile404JSONResponse{NotFoundJSONResponse: engineNotFound()}, nil
	case errors.Is(err, authz.ErrForbidden):
		return GetEngineBIFile403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: ErrorCodeForbidden, Message: "only the engine owner or a granted member may open its tables"}}, nil
	case err != nil:
		return nil, err
	}
	if eng.ExternalUrl == nil || strings.TrimSpace(*eng.ExternalUrl) == "" {
		return GetEngineBIFile422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "set the engine's external URL first")}, nil
	}
	external, err := url.Parse(strings.TrimSpace(*eng.ExternalUrl))
	if err != nil || external.Host == "" {
		return GetEngineBIFile422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "the engine's external URL is not valid")}, nil
	}
	port := external.Port()
	if port == "" {
		if external.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	schema := ""
	if request.Params.Schema != nil {
		schema = *request.Params.Schema
	}
	body, filename, ok := biFile(eng.Kind, request.Tool, external.Hostname(), port, request.Params.Catalog, schema, request.Params.Table)
	if !ok {
		return GetEngineBIFile422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "this engine kind cannot open in that BI tool")}, nil
	}
	return biFileResponse{filename: filename, body: body}, nil
}

// biFile renders the Tableau (.tds) or Power BI (.pbids) data source. It holds
// no credentials.
func biFile(kind, tool, host, port, catalog, schema, table string) (string, string, bool) {
	switch tool {
	case "tableau":
		class, ok := tableauClass(kind)
		if !ok {
			return "", "", false
		}
		esc := html.EscapeString
		conn := fmt.Sprintf("<connection class='%s' server='%s' port='%s'", esc(class), esc(host), esc(port))
		switch kind {
		case "trino":
			conn += fmt.Sprintf(" catalog='%s' schema='%s'", esc(catalog), esc(schema))
		case "postgresql", "sqlserver":
			conn += fmt.Sprintf(" dbname='%s' schema='%s'", esc(catalog), esc(schema))
		case "mysql":
			conn += fmt.Sprintf(" dbname='%s'", esc(catalog))
		}
		conn += " />"
		relation := table
		if schema != "" {
			relation = schema + "." + table
		}
		body := fmt.Sprintf("<?xml version='1.0' encoding='utf-8' ?>\n"+
			"<datasource formatted-name='%s' inline='true' version='18.1'>\n"+
			"  %s\n"+
			"  <relation name='%s' table='%s' type='table' />\n"+
			"</datasource>\n",
			esc(table), conn, esc(table), esc(relation))
		return body, table + ".tds", true
	case "powerbi":
		protocol, ok := powerBIProtocol(kind)
		if !ok {
			return "", "", false
		}
		doc := map[string]any{
			"version": "0.1",
			"connections": []any{map[string]any{
				"details": map[string]any{
					"protocol": protocol,
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
		return string(raw) + "\n", table + ".pbids", true
	default:
		return "", "", false
	}
}

// tableauClass maps an engine kind to Tableau's connector class.
func tableauClass(kind string) (string, bool) {
	switch kind {
	case "trino":
		return "trino", true
	case "postgresql":
		return "postgres", true
	case "mysql":
		return "mysql", true
	case "sqlserver":
		return "sqlserver", true
	default:
		return "", false
	}
}

// powerBIProtocol maps an engine kind to Power BI's connector protocol.
func powerBIProtocol(kind string) (string, bool) {
	switch kind {
	case "trino":
		return "trino", true
	case "postgresql":
		return "postgresql", true
	case "mysql":
		return "mysql", true
	case "sqlserver":
		return "sqlserver", true
	default:
		return "", false
	}
}
