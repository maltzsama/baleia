package repo

import (
	"encoding/json"
	"reflect"
	"testing"
)

// sameJSON reports whether got holds the same JSON data as want, whatever the
// spacing: PostgreSQL and MySQL store a normalized form of a JSON column
// (`{"a": 1}`), and MariaDB keeps the text it was given (`{"a":1}`).
func sameJSON(t *testing.T, got []byte, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Errorf("not JSON: %s (%v)", got, err)
		return false
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad expectation %q: %v", want, err)
	}
	return reflect.DeepEqual(g, w)
}
