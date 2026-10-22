package api

import (
	"testing"

	"github.com/galileostudio/baleia/internal/identity"
)

// TestMappingPreview: the endpoint returns exactly what login-time mapping
// derives, teams sorted by id.
func TestMappingPreview(t *testing.T) {
	h := testAPI(handlerFields{})
	body := &IdentityMappingPreviewRequest{
		Groups: []string{"baleia-admins", "team-eng", "team-ops"},
		RoleMapping: &[]IdentityRoleMapping{
			{Group: "baleia-admins", Role: IdentityRoleMappingRole(identity.RoleAdmin)},
		},
		TeamMapping: &[]IdentityTeamMapping{
			{Group: "team-eng", TeamId: "team-2", TeamRole: IdentityTeamMappingTeamRole(identity.TeamRoleMember)},
			{Group: "team-ops", TeamId: "team-1", TeamRole: IdentityTeamMappingTeamRole(identity.TeamRoleOwner)},
		},
	}

	got, err := h.PreviewIdentityMapping(idpAdminCtx(), PreviewIdentityMappingRequestObject{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	res, ok := got.(PreviewIdentityMapping200JSONResponse)
	if !ok {
		t.Fatalf("preview = %T, want 200", got)
	}
	if res.Role != IdentityMappingPreviewRole(identity.RoleAdmin) {
		t.Errorf("role = %q, want admin", res.Role)
	}
	if len(res.Teams) != 2 || res.Teams[0].TeamId != "team-1" || res.Teams[1].TeamId != "team-2" {
		t.Errorf("teams = %+v, want team-1 then team-2", res.Teams)
	}

	// Parity with the pure mapping login uses.
	want, err := identity.MapIdentity(identity.MappingInput{
		Groups:      body.Groups,
		RoleMapping: roleMappings(*body.RoleMapping),
		TeamMapping: teamMappings(*body.TeamMapping),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Role) != want.Role || len(res.Teams) != len(want.Teams) {
		t.Fatalf("preview = %+v, want %+v", res, want)
	}
}

// TestMappingPreview_NonAdmin403: only administrators may preview.
func TestMappingPreview_NonAdmin403(t *testing.T) {
	h := testAPI(handlerFields{})
	got, err := h.PreviewIdentityMapping(idpMemberCtx(), PreviewIdentityMappingRequestObject{
		Body: &IdentityMappingPreviewRequest{Groups: []string{"g"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(PreviewIdentityMapping403JSONResponse); !ok {
		t.Fatalf("preview = %T, want 403", got)
	}
}

// TestMappingPreview_Validation422: a malformed rule is a 422, never a guess.
func TestMappingPreview_Validation422(t *testing.T) {
	h := testAPI(handlerFields{})
	got, err := h.PreviewIdentityMapping(idpAdminCtx(), PreviewIdentityMappingRequestObject{
		Body: &IdentityMappingPreviewRequest{
			Groups:      []string{"g"},
			RoleMapping: &[]IdentityRoleMapping{{Group: "", Role: IdentityRoleMappingRole("member")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(PreviewIdentityMapping422JSONResponse); !ok {
		t.Fatalf("preview = %T, want 422", got)
	}
}
