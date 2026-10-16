package mysql

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"

	"github.com/galileostudio/baleia/internal/queryengine"
	driver "github.com/go-sql-driver/mysql"
)

// mapError turns a driver error into a typed engine error. The MySQL error
// number stays in Native for logs and metrics.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return queryengine.NewError(queryengine.CodeCancelled, KindMySQL, "", "statement cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return queryengine.NewError(queryengine.CodeTimeout, KindMySQL, "", "statement timed out")
	}
	var me *driver.MySQLError
	if errors.As(err, &me) {
		return queryengine.NewError(mysqlCode(me.Number), KindMySQL, strconv.Itoa(int(me.Number)), me.Message)
	}
	if errors.Is(err, driver.ErrInvalidConn) || errors.Is(err, sql.ErrConnDone) {
		return queryengine.NewError(queryengine.CodeUnavailable, KindMySQL, "", err.Error())
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return queryengine.NewError(queryengine.CodeTimeout, KindMySQL, "", err.Error())
		}
		return queryengine.NewError(queryengine.CodeUnavailable, KindMySQL, "", err.Error())
	}
	return queryengine.NewError(queryengine.CodeInternal, KindMySQL, "", err.Error())
}

// mysqlCode maps a MySQL error number to an engine error code.
func mysqlCode(n uint16) queryengine.ErrorCode {
	switch n {
	case 1055, 1059, 1064, 1066, 1149:
		return queryengine.CodeSyntax
	case 1049, 1051, 1054, 1109, 1146:
		return queryengine.CodeNotFound
	case 1044, 1045, 1142, 1143:
		return queryengine.CodePermission
	case 1317:
		return queryengine.CodeCancelled
	case 1969, 3024:
		return queryengine.CodeTimeout
	case 1205, 1213:
		return queryengine.CodeConflict
	case 2002, 2003, 2006, 2013:
		return queryengine.CodeUnavailable
	}
	return queryengine.CodeInternal
}

// isUnknownThread reports MySQL's answer for KILL QUERY of a connection that is
// gone (ER_NO_SUCH_THREAD); killing a finished statement is not an error.
func isUnknownThread(err error) bool {
	var me *driver.MySQLError
	return errors.As(err, &me) && me.Number == 1094
}
