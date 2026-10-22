package engines

import (
	"slices"
	"testing"
)

func TestNewRegistry_ListsEveryKind(t *testing.T) {
	r, err := NewRegistry(Options{})
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"clickhouse", "flightsql", "mariadb", "mysql", "oracle", "postgresql", "sqlite", "sqlserver", "trino"}
	if got := r.Kinds(); !slices.Equal(got, expected) {
		t.Fatalf("kinds = %v, want %v", got, expected)
	}
}
