package postgresql

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5/pgconn"
)

// mapError turns a pgx error into a typed engine error. The SQLSTATE stays in
// Native for logs and metrics.
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
	var pge *pgconn.PgError
	if errors.As(err, &pge) {
		return queryengine.NewError(sqlState(pge.Code), Kind, pge.Code, pge.Message)
	}
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) {
		return queryengine.NewError(queryengine.CodeUnavailable, Kind, "", ce.Error())
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return queryengine.NewError(queryengine.CodeTimeout, Kind, "", err.Error())
		}
		return queryengine.NewError(queryengine.CodeUnavailable, Kind, "", err.Error())
	}
	return queryengine.NewError(queryengine.CodeInternal, Kind, "", err.Error())
}

// sqlState maps a SQLSTATE to an engine error code.
func sqlState(code string) queryengine.ErrorCode {
	switch code {
	case "42601", "42P02", "42602", "42804", "42805", "42809":
		return queryengine.CodeSyntax
	case "42P01", "42703", "3D000", "3F000", "42883", "42704":
		return queryengine.CodeNotFound
	case "42501", "25006", "28000", "28P01", "28P02":
		return queryengine.CodePermission
	case "57014":
		return queryengine.CodeCancelled
	case "40001", "40P01":
		return queryengine.CodeConflict
	case "57000", "57P01", "57P02", "57P03", "08000", "08001", "08003", "08004", "08006", "08007", "08P01":
		return queryengine.CodeUnavailable
	}
	switch {
	case strings.HasPrefix(code, "42"):
		return queryengine.CodeSyntax
	case strings.HasPrefix(code, "53"):
		return queryengine.CodeResource
	case strings.HasPrefix(code, "08"):
		return queryengine.CodeUnavailable
	}
	return queryengine.CodeInternal
}
