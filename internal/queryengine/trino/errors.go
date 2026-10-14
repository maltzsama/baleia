package trino

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/galileostudio/baleia/internal/queryengine"
	trinoclient "github.com/galileostudio/baleia/internal/trino"
)

// errorNames maps Trino errorName values to engine error codes (#25).
var errorNames = map[string]queryengine.ErrorCode{
	"SYNTAX_ERROR":            queryengine.CodeSyntax,
	"TYPE_MISMATCH":           queryengine.CodeSyntax,
	"TABLE_NOT_FOUND":         queryengine.CodeNotFound,
	"SCHEMA_NOT_FOUND":        queryengine.CodeNotFound,
	"CATALOG_NOT_FOUND":       queryengine.CodeNotFound,
	"COLUMN_NOT_FOUND":        queryengine.CodeNotFound,
	"FUNCTION_NOT_FOUND":      queryengine.CodeNotFound,
	"PERMISSION_DENIED":       queryengine.CodePermission,
	"EXCEEDED_TIME_LIMIT":     queryengine.CodeTimeout,
	"EXCEEDED_CPU_LIMIT":      queryengine.CodeTimeout,
	"USER_CANCELED":           queryengine.CodeCancelled,
	"ADMINISTRATIVELY_KILLED": queryengine.CodeCancelled,
	"QUERY_QUEUE_FULL":        queryengine.CodeResource,
	"SERVER_STARTING_UP":      queryengine.CodeUnavailable,
	"SERVER_SHUTTING_DOWN":    queryengine.CodeUnavailable,
}

// mapError turns a client error into a queryengine error. The raw errorName
// stays in Native for logs and metrics.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return queryengine.NewError(queryengine.CodeCancelled, Kind, "", "statement cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return queryengine.NewError(queryengine.CodeTimeout, Kind, "", "statement timed out")
	}
	var te *trinoclient.TransportError
	if errors.As(err, &te) {
		if te.Status == http.StatusUnauthorized || te.Status == http.StatusForbidden {
			return queryengine.NewError(queryengine.CodePermission, Kind, fmt.Sprintf("HTTP %d", te.Status), te.Error())
		}
		return queryengine.NewError(queryengine.CodeUnavailable, Kind, "", te.Error())
	}
	var qe *trinoclient.QueryError
	if !errors.As(err, &qe) {
		return queryengine.NewError(queryengine.CodeInternal, Kind, "", err.Error())
	}
	return queryengine.NewError(codeFor(qe.ErrorName, qe.ErrorType), Kind, qe.ErrorName, qe.Message)
}

// codeFor maps by errorName first, then by family, then by errorType.
func codeFor(name, typ string) queryengine.ErrorCode {
	if c, ok := errorNames[name]; ok {
		return c
	}
	switch {
	case strings.HasPrefix(name, "EXCEEDED_") && strings.HasSuffix(name, "MEMORY_LIMIT"):
		return queryengine.CodeResource
	case strings.HasPrefix(name, "INVALID_") && typ == "USER_ERROR":
		return queryengine.CodeSyntax
	}
	switch typ {
	case "INSUFFICIENT_RESOURCES":
		return queryengine.CodeResource
	case "USER_ERROR":
		return queryengine.CodeSyntax
	}
	return queryengine.CodeInternal
}
