package api

import (
	"slices"
	"testing"
)

func TestSpaceActions_FromEffectiveRole(t *testing.T) {
	cases := []struct {
		name      string
		role      string
		isDefault bool
		want      []string
	}{
		{"viewer", "viewer", false, []string{"view"}},
		{"editor", "editor", false, []string{"view", "edit_content"}},
		{"manager", "manager", false, []string{"view", "edit_content", "manage", "delete"}},
		{"default space cannot be deleted", "manager", true, []string{"view", "edit_content", "manage"}},
		{"no role", "", false, []string{"view"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := spaceActions(tc.role, tc.isDefault); !slices.Equal(got, tc.want) {
				t.Fatalf("spaceActions(%q, %v) = %v, want %v", tc.role, tc.isDefault, got, tc.want)
			}
		})
	}
}
