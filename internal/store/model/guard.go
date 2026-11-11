package model

// guard is the value of a guard column: true when the rule it backs applies to
// the row, NULL when it does not. A unique key over a guard column enforces
// "at most one row where the rule applies" on every engine, because NULLs never
// collide, so no partial index (which MySQL and MariaDB lack) is needed.
func guard(applies bool) *bool {
	if !applies {
		return nil
	}
	t := true
	return &t
}
