// Package typemap renders a stored column type (native_type, in its source's
// type system) the way an engine's dialect spells it, for the binding a user
// browses through (#15, #18).
package typemap

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
