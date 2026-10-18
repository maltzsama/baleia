package api

import (
	"context"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/identity"
)

// PreviewIdentityMapping — POST /admin/identity-providers/mapping-preview
// (#264). It calls the same pure function as login, so the preview and the
// login-time mapping can never disagree. Nothing is persisted and no
// directory is contacted.
func (h *Handlers) PreviewIdentityMapping(ctx context.Context, request PreviewIdentityMappingRequestObject) (PreviewIdentityMappingResponseObject, error) {
	if auth.RoleFromContext(ctx) != "admin" {
		return PreviewIdentityMapping403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{
			Code: "forbidden", Message: "admin only",
		}}, nil
	}
	body := request.Body
	if body == nil {
		return PreviewIdentityMapping422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "request body is required")}, nil
	}

	input := identity.MappingInput{Groups: body.Groups}
	if body.RoleMapping != nil {
		input.RoleMapping = roleMappings(*body.RoleMapping)
	}
	if body.TeamMapping != nil {
		input.TeamMapping = teamMappings(*body.TeamMapping)
	}
	if body.DefaultRole != nil {
		input.DefaultRole = string(*body.DefaultRole)
	}

	mapped, err := identity.MapIdentity(input)
	if err != nil {
		return PreviewIdentityMapping422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", err.Error())}, nil
	}

	out := IdentityMappingPreview{
		Role:  IdentityMappingPreviewRole(mapped.Role),
		Teams: make([]IdentityTeamAssignment, 0, len(mapped.Teams)),
	}
	for _, t := range mapped.Teams {
		out.Teams = append(out.Teams, IdentityTeamAssignment{
			TeamId:   t.TeamID,
			TeamRole: IdentityTeamAssignmentTeamRole(t.TeamRole),
		})
	}
	return PreviewIdentityMapping200JSONResponse(out), nil
}
