package api

import (
	"context"
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/jackc/pgx/v5/pgtype"
)

// UpdateIdentityProvider — PATCH /admin/identity-providers/{id} (#285).
// Omitted config fields keep their stored value; an empty secret clears it.
// The provider type is immutable.
func (h *Handlers) UpdateIdentityProvider(ctx context.Context, request UpdateIdentityProviderRequestObject) (UpdateIdentityProviderResponseObject, error) {
	ev := audit.Event{Action: audit.IdentityProviderUpdate, ResourceType: "identity_provider", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	if auth.RoleFromContext(ctx) != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return UpdateIdentityProvider403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}
	if request.Body == nil {
		return UpdateIdentityProvider422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "request body is required")}, nil
	}

	ap, err := h.queries.GetAuthProviderByID(ctx, pgtype.UUID{Bytes: request.Id, Valid: true})
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return UpdateIdentityProvider404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "identity provider not found"}}, nil
	}

	name := ap.Name
	if request.Body.Name != nil {
		name = strings.TrimSpace(*request.Body.Name)
		if name == "" {
			return UpdateIdentityProvider422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "name must not be empty")}, nil
		}
	}
	enabled := ap.Enabled
	if request.Body.Enabled != nil {
		enabled = *request.Body.Enabled
	}

	sealed := ap.ConfigEnc
	switch ap.Type {
	case "oidc":
		if request.Body.Ldap != nil {
			return UpdateIdentityProvider422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "ldap configuration does not belong to an oidc provider")}, nil
		}
		if request.Body.Oidc != nil {
			current, err := auth.OpenOIDCConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
			if err != nil {
				return nil, err
			}
			merged := mergeOIDCConfig(current, request.Body.Oidc)
			if err := merged.Validate(); err != nil {
				return UpdateIdentityProvider422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", err.Error())}, nil
			}
			sealed, err = auth.SealOIDCConfig(h.keyring, idString(ap.ID), merged)
			if err != nil {
				return nil, err
			}
		}
	case "ldap":
		if request.Body.Oidc != nil {
			return UpdateIdentityProvider422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "oidc configuration does not belong to an ldap provider")}, nil
		}
		if request.Body.Ldap != nil {
			current, err := identityldap.OpenConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
			if err != nil {
				return nil, err
			}
			merged := mergeLDAPConfig(current, request.Body.Ldap)
			if err := merged.Validate(); err != nil {
				return UpdateIdentityProvider422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", err.Error())}, nil
			}
			sealed, err = identityldap.SealConfig(h.keyring, idString(ap.ID), merged)
			if err != nil {
				return nil, err
			}
		}
	default:
		return nil, errors.New("identity provider has an unknown type")
	}

	ap, err = h.queries.UpdateAuthProvider(ctx, db.UpdateAuthProviderParams{
		ID: ap.ID, Name: name, ConfigEnc: sealed, Enabled: enabled,
	})
	if err != nil {
		return nil, err
	}

	detail, err := h.identityProviderDetail(ap)
	if err != nil {
		return nil, err
	}
	h.audited(ctx, withID(ev, ap.ID).Allowed())
	return UpdateIdentityProvider200JSONResponse(detail), nil
}

// DeleteIdentityProvider — DELETE /admin/identity-providers/{id} (#285).
// Linked users make the delete a 409 unless suspend_users=true; in that case
// they are suspended and detached, never orphaned.
func (h *Handlers) DeleteIdentityProvider(ctx context.Context, request DeleteIdentityProviderRequestObject) (DeleteIdentityProviderResponseObject, error) {
	ev := audit.Event{Action: audit.IdentityProviderDelete, ResourceType: "identity_provider", ResourceID: idString(pgtype.UUID{Bytes: request.Id, Valid: true})}
	if auth.RoleFromContext(ctx) != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return DeleteIdentityProvider403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}

	ap, err := h.queries.GetAuthProviderByID(ctx, pgtype.UUID{Bytes: request.Id, Valid: true})
	if err != nil {
		h.audited(ctx, ev.Failed(audit.ReasonNotFound))
		return DeleteIdentityProvider404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "identity provider not found"}}, nil
	}

	linked, err := h.queries.CountUsersByAuthProvider(ctx, ap.ID)
	if err != nil {
		return nil, err
	}
	suspend := request.Params.SuspendUsers != nil && *request.Params.SuspendUsers
	if linked > 0 && !suspend {
		h.audited(ctx, ev.Failed(audit.ReasonConflict))
		return DeleteIdentityProvider409JSONResponse{ConflictJSONResponse: ConflictJSONResponse{
			Code:    "provider_in_use",
			Message: "users are still linked to this provider; pass suspend_users=true to suspend and detach them",
		}}, nil
	}

	err = h.inTx(ctx, func(q db.Querier) error {
		if suspend && linked > 0 {
			if err := q.RevokeRefreshTokensByAuthProvider(ctx, ap.ID); err != nil {
				return err
			}
			if err := q.DeleteAPIKeysByAuthProvider(ctx, ap.ID); err != nil {
				return err
			}
			if err := q.SuspendUsersByAuthProvider(ctx, ap.ID); err != nil {
				return err
			}
		}
		return q.DeleteAuthProvider(ctx, ap.ID)
	})
	if err != nil {
		return nil, err
	}

	h.audited(ctx, withID(ev, ap.ID).Allowed())
	return DeleteIdentityProvider204Response{}, nil
}

// TestIdentityProvider — POST /admin/identity-providers/{id}/test (#286).
func (h *Handlers) TestIdentityProvider(ctx context.Context, request TestIdentityProviderRequestObject) (TestIdentityProviderResponseObject, error) {
	if auth.RoleFromContext(ctx) != "admin" {
		return TestIdentityProvider403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}
	ap, err := h.queries.GetAuthProviderByID(ctx, pgtype.UUID{Bytes: request.Id, Valid: true})
	if err != nil {
		return TestIdentityProvider404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{Code: "not_found", Message: "identity provider not found"}}, nil
	}

	var cfg providerConfig
	switch ap.Type {
	case "oidc":
		oidcCfg, err := auth.OpenOIDCConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
		if err != nil {
			return nil, err
		}
		cfg.oidc = &oidcCfg
	case "ldap":
		ldapCfg, err := identityldap.OpenConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
		if err != nil {
			return nil, err
		}
		cfg.ldap = &ldapCfg
	default:
		return nil, errors.New("identity provider has an unknown type")
	}

	username := ""
	if request.Params.TestUsername != nil {
		username = *request.Params.TestUsername
	}
	return TestIdentityProvider200JSONResponse(h.probeIdentityProvider(ctx, cfg, username)), nil
}

// TestIdentityProviderTransient — POST /admin/identity-providers/test (#286).
// It validates and probes an unsaved configuration and writes nothing.
func (h *Handlers) TestIdentityProviderTransient(ctx context.Context, request TestIdentityProviderTransientRequestObject) (TestIdentityProviderTransientResponseObject, error) {
	if auth.RoleFromContext(ctx) != "admin" {
		return TestIdentityProviderTransient403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{Code: "forbidden", Message: "admin only"}}, nil
	}
	body := request.Body
	if body == nil {
		return TestIdentityProviderTransient422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", "request body is required")}, nil
	}

	cfg, err := parseProviderConfigParts(body.Type, body.Oidc, body.Ldap)
	if err != nil {
		return TestIdentityProviderTransient422JSONResponse{UnprocessableEntityJSONResponse: unprocessable("validation_error", err.Error())}, nil
	}
	username := ""
	if body.TestUsername != nil {
		username = *body.TestUsername
	}
	return TestIdentityProviderTransient200JSONResponse(h.probeIdentityProvider(ctx, cfg, username)), nil
}

// mergeOIDCConfig applies the present fields of an update to the stored config.
// A nil secret keeps the stored value; a pointer to "" clears it.
func mergeOIDCConfig(current auth.OIDCProviderConfig, upd *UpdateOIDCConfig) auth.OIDCProviderConfig {
	out := current
	if upd.IssuerUrl != nil {
		out.IssuerURL = *upd.IssuerUrl
	}
	if upd.ClientId != nil {
		out.ClientID = *upd.ClientId
	}
	if upd.ClientSecret != nil {
		out.ClientSecret = *upd.ClientSecret
	}
	if upd.AutoCreate != nil {
		out.AutoCreate = *upd.AutoCreate
	}
	if upd.AllowedEmailDomains != nil {
		out.AllowedEmailDomains = *upd.AllowedEmailDomains
	}
	if upd.RoleMapping != nil {
		out.RoleMapping = roleMappings(*upd.RoleMapping)
	}
	if upd.TeamMapping != nil {
		out.TeamMapping = teamMappings(*upd.TeamMapping)
	}
	if upd.DefaultRole != nil {
		out.DefaultRole = string(*upd.DefaultRole)
	}
	if upd.Claims != nil {
		out.Claims = mergeClaims(out.Claims, *upd.Claims)
	}
	return out
}

func mergeClaims(current auth.OIDCClaimsConfig, upd OIDCClaimsConfig) auth.OIDCClaimsConfig {
	out := current
	if upd.Subject != nil {
		out.Subject = *upd.Subject
	}
	if upd.Email != nil {
		out.Email = *upd.Email
	}
	if upd.Name != nil {
		out.Name = *upd.Name
	}
	if upd.Groups != nil {
		out.Groups = *upd.Groups
	}
	return out
}

// mergeLDAPConfig applies the present fields of an update to the stored config.
func mergeLDAPConfig(current identityldap.Config, upd *UpdateLDAPConfig) identityldap.Config {
	out := current
	if upd.Url != nil {
		out.URL = *upd.Url
	}
	if upd.StartTls != nil {
		out.StartTLS = *upd.StartTls
	}
	if upd.BindDn != nil {
		out.BindDN = *upd.BindDn
	}
	if upd.BindPassword != nil {
		out.BindPassword = *upd.BindPassword
	}
	if upd.BaseDn != nil {
		out.BaseDN = *upd.BaseDn
	}
	if upd.UserFilter != nil {
		out.UserFilter = *upd.UserFilter
	}
	if upd.GroupAttribute != nil {
		out.GroupAttribute = *upd.GroupAttribute
	}
	if upd.EmailAttribute != nil {
		out.EmailAttribute = *upd.EmailAttribute
	}
	if upd.NameAttribute != nil {
		out.NameAttribute = *upd.NameAttribute
	}
	if upd.NestedGroups != nil {
		out.NestedGroups = *upd.NestedGroups
	}
	if upd.RequiredGroup != nil {
		out.RequiredGroup = *upd.RequiredGroup
	}
	if upd.TimeoutSeconds != nil {
		out.TimeoutSeconds = *upd.TimeoutSeconds
	}
	if upd.RoleMapping != nil {
		out.RoleMapping = roleMappings(*upd.RoleMapping)
	}
	if upd.TeamMapping != nil {
		out.TeamMapping = teamMappings(*upd.TeamMapping)
	}
	if upd.DefaultRole != nil {
		out.DefaultRole = string(*upd.DefaultRole)
	}
	return out.Normalize()
}

func roleMappings(in []IdentityRoleMapping) []identity.RoleMapping {
	out := make([]identity.RoleMapping, 0, len(in))
	for _, m := range in {
		out = append(out, identity.RoleMapping{Group: m.Group, Role: string(m.Role)})
	}
	return out
}

func teamMappings(in []IdentityTeamMapping) []identity.TeamMapping {
	out := make([]identity.TeamMapping, 0, len(in))
	for _, m := range in {
		out = append(out, identity.TeamMapping{Group: m.Group, TeamID: m.TeamId, TeamRole: string(m.TeamRole)})
	}
	return out
}
