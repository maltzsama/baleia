package clickhouse

import (
	"context"
	"errors"
	"net"
	"strconv"

	"github.com/galileostudio/baleia/internal/queryengine"
)

// ClickHouse exception codes (ErrorCodes.cpp). The engine maps the ones a
// session can hit; anything else is internal.
const (
	errSyntax            = 62  // SYNTAX_ERROR
	errUnknownTable      = 60  // UNKNOWN_TABLE
	errUnknownDatabase   = 81  // UNKNOWN_DATABASE
	errUnknownIdentifier = 47  // UNKNOWN_IDENTIFIER
	errBadArguments      = 36  // BAD_ARGUMENTS
	errTimeout           = 159 // TIMEOUT_EXCEEDED
	errTooSlow           = 160 // TOO_SLOW
	errReadonly          = 164 // READONLY
	errSocketTimeout     = 209 // SOCKET_TIMEOUT
	errNetwork           = 210 // NETWORK_ERROR
	errMemoryLimit       = 241 // MEMORY_LIMIT_EXCEEDED
	errAllConnTries      = 279 // ALL_CONNECTION_TRIES_FAILED
	errCancelled         = 394 // QUERY_WAS_CANCELLED
	errAccessDenied      = 497 // ACCESS_DENIED
	errAuthFailed        = 516 // AUTHENTICATION_FAILED
)

// serverError builds a typed error from an X-ClickHouse-Exception-Code.
func serverError(code int) error {
	return queryengine.NewError(exceptionCode(code), Kind, strconv.Itoa(code), "clickhouse error (code "+strconv.Itoa(code)+")")
}

// exceptionCode maps a ClickHouse exception code to an engine error code.
func exceptionCode(code int) queryengine.ErrorCode {
	switch code {
	case errSyntax:
		return queryengine.CodeSyntax
	case errUnknownTable, errUnknownDatabase, errUnknownIdentifier, errBadArguments:
		return queryengine.CodeNotFound
	case errAccessDenied, errReadonly, errAuthFailed:
		return queryengine.CodePermission
	case errTimeout, errSocketTimeout:
		return queryengine.CodeTimeout
	case errCancelled:
		return queryengine.CodeCancelled
	case errTooSlow, errMemoryLimit:
		return queryengine.CodeResource
	case errNetwork, errAllConnTries:
		return queryengine.CodeUnavailable
	}
	return queryengine.CodeInternal
}

// mapError turns a transport or context error into a typed engine error.
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
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return queryengine.NewError(queryengine.CodeTimeout, Kind, "", err.Error())
		}
		return queryengine.NewError(queryengine.CodeUnavailable, Kind, "", err.Error())
	}
	return queryengine.NewError(queryengine.CodeUnavailable, Kind, "", err.Error())
}
