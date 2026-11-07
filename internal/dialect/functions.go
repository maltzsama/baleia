package dialect

import "strings"

// noCall lists the SQL special forms the editor writes without parentheses.
var noCall = map[string]bool{
	"current_catalog": true, "current_date": true, "current_path": true,
	"current_role": true, "current_schema": true, "current_time": true,
	"current_timestamp": true, "current_user": true, "localtime": true,
	"localtimestamp": true, "session_user": true, "system_user": true,
	"user": true,
}

// Callable reports whether a function in the dialect takes parentheses; the
// special forms (current_date) do not. MySQL/MariaDB expose USER(),
// SESSION_USER() and SYSTEM_USER() as callable functions.
func Callable(dialectName, name string) bool {
	key := strings.ToLower(name)
	if !noCall[key] {
		return true
	}
	if (dialectName == MySQL || dialectName == MariaDB) &&
		(key == "user" || key == "session_user" || key == "system_user") {
		return true
	}
	return false
}
