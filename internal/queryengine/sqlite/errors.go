package sqlite

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/galileostudio/baleia/internal/queryengine"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// mapError turns a modernc.org/sqlite error into a typed engine error. The
// extended result code stays in Native for logs and metrics.
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
	var se *sqlite.Error
	if errors.As(err, &se) {
		code := se.Code()
		return queryengine.NewError(errorCode(code, se.Error()), Kind, strconv.Itoa(code), se.Error())
	}
	return queryengine.NewError(queryengine.CodeInternal, Kind, "", err.Error())
}

// errorCode maps a (possibly extended) SQLite result code to an engine code.
// SQLITE_ERROR is the one ambiguity: at prepare time it is a syntax error,
// except when SQLite reports a missing object.
func errorCode(code int, msg string) queryengine.ErrorCode {
	switch code & 0xff {
	case sqlite3.SQLITE_ERROR:
		if isMissing(msg) {
			return queryengine.CodeNotFound
		}
		return queryengine.CodeSyntax
	case sqlite3.SQLITE_PERM, sqlite3.SQLITE_READONLY, sqlite3.SQLITE_AUTH:
		return queryengine.CodePermission
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED, sqlite3.SQLITE_CONSTRAINT:
		return queryengine.CodeConflict
	case sqlite3.SQLITE_INTERRUPT:
		return queryengine.CodeCancelled
	case sqlite3.SQLITE_TOOBIG, sqlite3.SQLITE_NOMEM, sqlite3.SQLITE_FULL:
		return queryengine.CodeResource
	case sqlite3.SQLITE_CANTOPEN, sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_CORRUPT,
		sqlite3.SQLITE_IOERR, sqlite3.SQLITE_PROTOCOL, sqlite3.SQLITE_SCHEMA:
		return queryengine.CodeUnavailable
	}
	return queryengine.CodeInternal
}

// isMissing reports SQLite's error text for an object that does not exist.
// SQLite reports these only as SQLITE_ERROR, so the message is matched; a test
// guards the wording per driver version.
func isMissing(msg string) bool {
	return strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "no such column") ||
		strings.Contains(msg, "no such index") ||
		strings.Contains(msg, "no such view")
}
