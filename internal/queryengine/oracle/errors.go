package oracle

import (
	"context"
	"database/sql"
	"errors"
	"net"
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
		return queryengine.NewError(oraCode(oe.ErrCode), Kind, strconv.Itoa(oe.ErrCode), oe.ErrMsg)
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
