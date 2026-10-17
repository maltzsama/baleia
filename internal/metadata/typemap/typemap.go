// Package typemap renders a stored column type (native_type, in its source's
// type system) the way an engine's dialect spells it, for the binding a user
// browses through (#15, #18).
package typemap

import "strings"

// mappers maps (type system, engine dialect) to a renderer.
var mappers = map[[2]string]func(string) (string, bool){
	{"iceberg", "trino"}: IcebergToTrino,
}

// Display renders native, a type of typeSystem, in dialect. The same type
// system as the dialect is the identity. ok is false when no mapping exists
// or the type is not understood; callers then show native as is.
func Display(typeSystem, dialect, native string) (string, bool) {
	if typeSystem == dialect {
		return native, true
	}
	m, ok := mappers[[2]string{typeSystem, dialect}]
	if !ok {
		return "", false
	}
	return m(native)
}

// Logical maps a source type to the portable semantic-layer vocabulary (#115).
// The frontend filters measure columns by this, never by parsing native types.
func Logical(typeSystem, native string) string {
	t := strings.ToLower(strings.TrimSpace(native))
	// Drop modifiers: numeric(10,2) -> numeric, timestamp(6) with time zone -> ...
	base := t
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = strings.TrimSpace(base[:i])
	}
	base = strings.ReplaceAll(base, " unsigned", "")
	switch {
	case base == "bool" || base == "boolean" || (typeSystem == "mysql" && base == "tinyint" && strings.HasPrefix(t, "tinyint(1)")):
		return "boolean"
	case base == "date":
		return "date"
	case strings.Contains(t, "timestamp") && (strings.Contains(t, "with time zone") || strings.Contains(t, "tz")):
		return "timestamptz"
	case strings.Contains(t, "timestamp") || base == "datetime":
		return "timestamp"
	case base == "json" || base == "jsonb":
		return "json"
	case strings.Contains(base, "int") || base == "long" || base == "smallint" || base == "tinyint" || base == "int2" || base == "int4" || base == "int8":
		return "int64"
	case base == "numeric" || base == "decimal" || base == "number" || base == "dec":
		return "decimal"
	case base == "real" || base == "float" || base == "double" || base == "float4" || base == "float8" || base == "double precision":
		return "float64"
	case base == "binary" || base == "varbinary" || base == "blob" || base == "bytea":
		return "binary"
	case strings.Contains(base, "char") || strings.Contains(base, "text") || base == "string" || base == "clob" || base == "varchar" || base == "nvarchar":
		return "string"
	default:
		return "other"
	}
}
