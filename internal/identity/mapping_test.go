package identity

import (
	"reflect"
	"testing"
)

func TestMapIdentity_SingleRoleGroup(t *testing.T) {
	t.Parallel()
	got, err := MapIdentity(MappingInput{
		Groups:      []string{"baleia-admins"},
		RoleMapping: []RoleMapping{{Group: "baleia-admins", Role: RoleAdmin}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleAdmin {
		t.Errorf("role = %q, want %q", got.Role, RoleAdmin)
	}
	if len(got.Teams) != 0 {
		t.Errorf("teams = %v, want none", got.Teams)
	}
}

func TestMapIdentity_MultipleRoleGroups(t *testing.T) {
	t.Parallel()
	// The most privileged role wins regardless of rule order.
	for _, mappings := range [][]RoleMapping{
		{{Group: "members", Role: RoleMember}, {Group: "admins", Role: RoleAdmin}},
		{{Group: "admins", Role: RoleAdmin}, {Group: "members", Role: RoleMember}},
	} {
		got, err := MapIdentity(MappingInput{
			Groups:      []string{"admins", "members"},
			RoleMapping: mappings,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Role != RoleAdmin {
			t.Errorf("role = %q, want %q", got.Role, RoleAdmin)
		}
	}
}

func TestMapIdentity_DefaultRole(t *testing.T) {
	t.Parallel()
	got, err := MapIdentity(MappingInput{
		Groups:      []string{"unmapped"},
		RoleMapping: []RoleMapping{{Group: "admins", Role: RoleAdmin}},
		DefaultRole: RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleAdmin {
		t.Errorf("role = %q, want the default %q", got.Role, RoleAdmin)
	}

	got, err = MapIdentity(MappingInput{Groups: []string{"unmapped"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleMember {
		t.Errorf("role = %q, want member when no default is configured", got.Role)
	}
}

func TestMapIdentity_TeamMapping(t *testing.T) {
	t.Parallel()
	got, err := MapIdentity(MappingInput{
		Groups: []string{"data-eng"},
		TeamMapping: []TeamMapping{
			{Group: "data-eng", TeamID: "team-b", TeamRole: TeamRoleMember},
			{Group: "data-eng", TeamID: "team-a", TeamRole: TeamRoleOwner},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []TeamAssignment{
		{TeamID: "team-a", TeamRole: TeamRoleOwner},
		{TeamID: "team-b", TeamRole: TeamRoleMember},
	}
	if !reflect.DeepEqual(got.Teams, want) {
		t.Errorf("teams = %v, want %v", got.Teams, want)
	}
}

func TestMapIdentity_TeamRoleMostPrivilegedWins(t *testing.T) {
	t.Parallel()
	got, err := MapIdentity(MappingInput{
		Groups: []string{"a", "b"},
		TeamMapping: []TeamMapping{
			{Group: "a", TeamID: "team", TeamRole: TeamRoleMember},
			{Group: "b", TeamID: "team", TeamRole: TeamRoleOwner},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []TeamAssignment{{TeamID: "team", TeamRole: TeamRoleOwner}}
	if !reflect.DeepEqual(got.Teams, want) {
		t.Errorf("teams = %v, want %v", got.Teams, want)
	}
}

func TestMapIdentity_InvalidRole(t *testing.T) {
	t.Parallel()
	cases := []MappingInput{
		{RoleMapping: []RoleMapping{{Group: "g", Role: "superuser"}}},
		{DefaultRole: "superuser"},
		{RoleMapping: []RoleMapping{{Group: "", Role: RoleAdmin}}},
		{TeamMapping: []TeamMapping{{Group: "g", TeamID: "", TeamRole: TeamRoleMember}}},
		{TeamMapping: []TeamMapping{{Group: "g", TeamID: "t", TeamRole: "superuser"}}},
		{TeamMapping: []TeamMapping{{Group: "", TeamID: "t", TeamRole: TeamRoleMember}}},
	}
	for i, in := range cases {
		if _, err := MapIdentity(in); err == nil {
			t.Errorf("case %d: MapIdentity accepted a malformed mapping", i)
		}
	}
}

func TestMapIdentity_Deterministic(t *testing.T) {
	t.Parallel()
	in := MappingInput{
		Groups: []string{"a", "b", "c"},
		TeamMapping: []TeamMapping{
			{Group: "a", TeamID: "team-c", TeamRole: TeamRoleMember},
			{Group: "b", TeamID: "team-a", TeamRole: TeamRoleOwner},
			{Group: "c", TeamID: "team-b", TeamRole: TeamRoleMember},
		},
	}
	first, err := MapIdentity(in)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := MapIdentity(in)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d = %v, want stable %v", i, got, first)
		}
	}
}
