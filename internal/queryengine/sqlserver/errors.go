package sqlserver

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"

	"github.com/galileostudio/baleia/internal/queryengine"
	mssql "github.com/microsoft/go-mssqldb"
)

// mapError turns a driver error into a typed engine error. The SQL Server error
// number stays in Native for logs and metrics.
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
	var me mssql.Error
	if errors.As(err, &me) {
		return queryengine.NewError(tsqlCode(me.Number), Kind, strconv.Itoa(int(me.Number)), me.Message)
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

// tsqlCode maps a SQL Server error number to an engine error code.
func tsqlCode(n int32) queryengine.ErrorCode {
	switch n {
	case 102, 105, 156, 170:
		return queryengine.CodeSyntax
	case 207, 208, 911, 916, 2812:
		return queryengine.CodeNotFound
	case 229, 230, 262, 297, 4060, 18456, 18461:
		return queryengine.CodePermission
	case 1205:
		return queryengine.CodeConflict
	case 1222:
		return queryengine.CodeTimeout
	case 40613, 18452:
		return queryengine.CodeUnavailable
	}
	return queryengine.CodeInternal
}

// isNotRunning reports SQL Server's answer for KILL of a session that is gone;
// killing a finished statement is not an error.
func isNotRunning(err error) bool {
	var me mssql.Error
	return errors.As(err, &me) && me.Number == 6106
}
