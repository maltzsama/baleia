package oracle

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"regexp"
	"strconv"

	"github.com/galileostudio/baleia/internal/queryengine"
	network "github.com/sijms/go-ora/v2/network"
)

// mapError turns a driver error into a typed engine error. The ORA number stays
// in Native for logs and metrics.
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
	var oe *network.OracleError
	if errors.As(err, &oe) {
		code := causeOf(oe.ErrCode, oe.ErrMsg)
		return queryengine.NewError(oraCode(code), Kind, strconv.Itoa(code), oe.ErrMsg)
	}
	if errors.Is(err, sql.ErrConnDone) {
		return queryengine.NewError(queryengine.CodeUnavailable, Kind, "", err.Error())
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

// oraCode maps an ORA number to an engine error code.
func oraCode(n int) queryengine.ErrorCode {
	switch n {
	case 900, 903, 906, 907, 908, 909, 910, 911, 917, 918, 920, 923, 928, 933, 935, 936:
		return queryengine.CodeSyntax
	case 904, 942, 4043:
		return queryengine.CodeNotFound
	case 1017, 1031:
		return queryengine.CodePermission
	case 1013:
		return queryengine.CodeCancelled
	case 60:
		return queryengine.CodeConflict
	case 1555, 4030:
		return queryengine.CodeResource
	case 3113, 3114, 12170, 12514, 12541, 12545:
		return queryengine.CodeUnavailable
	}
	return queryengine.CodeInternal
}

// isNotRunning reports ORA answers for cancelling a session that is gone;
// cancelling a finished statement is not an error.
func isNotRunning(err error) bool {
	var oe *network.OracleError
	if !errors.As(err, &oe) {
		return false
	}
	return oe.ErrCode == 30 || oe.ErrCode == 1013
}

// oraNumber finds the ORA codes in an Oracle message.
var oraNumber = regexp.MustCompile(`ORA-(\d{5})`)

// causeOf returns the error behind ORA-00604 ("error occurred at recursive
// SQL level n"), which only wraps the next ORA code in the message: a cancel
// that lands while Oracle parses the statement arrives as 604 around 1013.
func causeOf(code int, msg string) int {
	if code != 604 {
		return code
	}
	for _, m := range oraNumber.FindAllStringSubmatch(msg, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n != 604 {
			return n
		}
	}
	return code
}
