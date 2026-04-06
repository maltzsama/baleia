package api

import (
	"context"
	"encoding/json"
	"time"
	"log/slog"
	
	catalog_connections "github.com/galileostudio/baleia/internal/catalog_connections"


	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *Handlers) ListCatalogConnections(ctx context.Context, request ListCatalogConnectionsRequestObject) (ListCatalogConnectionsResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return ListCatalogConnections403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	conns, err := h.queries.ListCatalogConnections(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]CatalogConnection, 0, len(conns))
	for _, c := range conns {
		result = append(result, dbCatalogConnectionToPublic(c))
	}
	return ListCatalogConnections200JSONResponse(result), nil
}

func (h *Handlers) CreateCatalogConnection(ctx context.Context, request CreateCatalogConnectionRequestObject) (CreateCatalogConnectionResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return CreateCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var credsEnc *string
	switch string(request.Body.Type) {
	case "polaris":
		if request.Body.PolarisCredentials != nil {
			b, err := json.Marshal(request.Body.PolarisCredentials)
			if err != nil { return nil, err }
			s := string(b); credsEnc = &s
		}
	case "nessie":
		if request.Body.NessieCredentials != nil {
			b, err := json.Marshal(request.Body.NessieCredentials)
			if err != nil { return nil, err }
			s := string(b); credsEnc = &s
		}
	case "glue":
		if request.Body.GlueCredentials != nil {
			b, err := json.Marshal(request.Body.GlueCredentials)
			if err != nil { return nil, err }
			s := string(b); credsEnc = &s
		}
	case "hive":
		if request.Body.HiveCredentials != nil {
			b, err := json.Marshal(request.Body.HiveCredentials)
			if err != nil { return nil, err }
			s := string(b); credsEnc = &s
		}
	case "iceberg":
		if request.Body.IcebergCredentials != nil {
			b, err := json.Marshal(request.Body.IcebergCredentials)
			if err != nil { return nil, err }
			s := string(b); credsEnc = &s
		}
	}

	defaultReadonly := true
	if request.Body.DefaultReadonly != nil {
		defaultReadonly = *request.Body.DefaultReadonly
	}

	conn, err := h.queries.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		Name:            request.Body.Name,
		Type:            string(request.Body.Type),
		Url:             request.Body.Url,
		CredentialsEnc:  credsEnc,
		DefaultReadonly: defaultReadonly,
	})
	if err != nil {
		slog.Error("CreateCatalogConnection failed", "err", err)
		return nil, err
	}

	return CreateCatalogConnection201JSONResponse(dbCatalogConnectionToPublic(conn)), nil
}

func (h *Handlers) GetCatalogConnection(ctx context.Context, request GetCatalogConnectionRequestObject) (GetCatalogConnectionResponseObject, error) {
	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	conn, err := h.queries.GetCatalogConnectionByID(ctx, uid)
	if err != nil {
		return GetCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
	}

	return GetCatalogConnection200JSONResponse(dbCatalogConnectionToPublic(conn)), nil
}

func (h *Handlers) UpdateCatalogConnection(ctx context.Context, request UpdateCatalogConnectionRequestObject) (UpdateCatalogConnectionResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return UpdateCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	current, err := h.queries.GetCatalogConnectionByID(ctx, uid)
	if err != nil {
		return UpdateCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
	}

	name := current.Name
	url := current.Url
	credsEnc := current.CredentialsEnc
	defaultReadonly := current.DefaultReadonly

	if request.Body.Name != nil {
		name = *request.Body.Name
	}
	if request.Body.Url != nil {
		url = *request.Body.Url
	}
	if request.Body.Credentials != nil {
		b, err := json.Marshal(request.Body.Credentials)
		if err != nil {
			return nil, err
		}
		s := string(b)
		credsEnc = &s
	}
	if request.Body.DefaultReadonly != nil {
		defaultReadonly = *request.Body.DefaultReadonly
	}

	conn, err := h.queries.UpdateCatalogConnection(ctx, db.UpdateCatalogConnectionParams{
		ID:              uid,
		Name:            name,
		Url:             url,
		CredentialsEnc:  credsEnc,
		DefaultReadonly: defaultReadonly,
	})
	if err != nil {
		return nil, err
	}

	return UpdateCatalogConnection200JSONResponse(dbCatalogConnectionToPublic(conn)), nil
}

func (h *Handlers) DeleteCatalogConnection(ctx context.Context, request DeleteCatalogConnectionRequestObject) (DeleteCatalogConnectionResponseObject, error) {
	role := auth.RoleFromContext(ctx)
	if role != "admin" {
		return DeleteCatalogConnection403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	var uid pgtype.UUID
	uid.Bytes = request.Id
	uid.Valid = true

	_, err := h.queries.GetCatalogConnectionByID(ctx, uid)
	if err != nil {
		return DeleteCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
	}

	if err := h.queries.DeleteCatalogConnection(ctx, uid); err != nil {
		return nil, err
	}

	return DeleteCatalogConnection204Response{}, nil
}

func (h *Handlers) TestCatalogConnection(ctx context.Context, request TestCatalogConnectionRequestObject) (TestCatalogConnectionResponseObject, error) {
    var uid pgtype.UUID
    uid.Bytes = request.Id
    uid.Valid = true

    conn, err := h.queries.GetCatalogConnectionByID(ctx, uid)
    if err != nil {
        return TestCatalogConnection404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "catalog connection not found"}}, nil
    }

    start := time.Now()
    client, err := catalog_connections.NewClient(conn.Type, conn.Url, conn.CredentialsEnc)
    if err != nil {
        errMsg := err.Error()
        return TestCatalogConnection200JSONResponse{
            Success:   false,
            LatencyMs: 0,
            Error:     &errMsg,
        }, nil
    }

    testErr := client.Ping(ctx)
    latencyMs := int(time.Since(start).Milliseconds())

    if testErr != nil {
        errMsg := testErr.Error()
        return TestCatalogConnection200JSONResponse{
            Success:   false,
            LatencyMs: latencyMs,
            Error:     &errMsg,
        }, nil
    }

    v := conn.Type
    return TestCatalogConnection200JSONResponse{
        Success:   true,
        LatencyMs: latencyMs,
        Version:   &v,
    }, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func dbCatalogConnectionToPublic(c db.CatalogConnection) CatalogConnection {
	id := openapi_types.UUID(c.ID.Bytes)
	return CatalogConnection{
		Id:              id,
		Name:            c.Name,
		Type:            CatalogConnectionType(c.Type),
		Url:             c.Url,
		DefaultReadonly: c.DefaultReadonly,
		CreatedAt:       pgTimestampToTime(c.CreatedAt),
		UpdatedAt:       func() *time.Time { t := pgTimestampToTime(c.UpdatedAt); return &t }(),
	}
}
