package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Admin identity provider management (#259).
//
// The provider config is sealed with the keyring and bound to the provider id,
// so a config copied to another row cannot be opened (#278). Secrets are
// write-only: they are stored on create and never appear in a response, which
// reports presence through has_client_secret / has_bind_password instead.

// providerConfig is one validated provider configuration, ready to be sealed.
type providerConfig struct {
	oidc *auth.OIDCProviderConfig
	ldap *identityldap.Config
}

func (h *AuthHandlers) ListIdentityProviders(ctx context.Context, request ListIdentityProvidersRequestObject) (ListIdentityProvidersResponseObject, error) {
	if auth.RoleFromContext(ctx) != "admin" {
		return ListIdentityProviders403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{
			Code: ErrorCodeForbidden, Message: "admin only",
		}}, nil
	}

	all, err := h.providers.List(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]IdentityProvider, 0, len(all))
	for _, ap := range all {
		item, err := h.identityProviderToAdmin(ctx, ap)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return ListIdentityProviders200JSONResponse(out), nil
}

func (h *AuthHandlers) GetIdentityProvider(ctx context.Context, request GetIdentityProviderRequestObject) (GetIdentityProviderResponseObject, error) {
	if auth.RoleFromContext(ctx) != "admin" {
		return GetIdentityProvider403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{
			Code: ErrorCodeForbidden, Message: "admin only",
		}}, nil
	}

	ap, err := h.providers.Get(ctx, pgtype.UUID{Bytes: request.Id, Valid: true})
	if err != nil {
		return GetIdentityProvider404JSONResponse{NotFoundJSONResponse: NotFoundJSONResponse{
			Code: ErrorCodeNotFound, Message: "identity provider not found",
		}}, nil
	}

	detail, err := h.identityProviderDetail(ctx, ap)
	if err != nil {
		return nil, err
	}
	return GetIdentityProvider200JSONResponse(detail), nil
}

func (h *AuthHandlers) CreateIdentityProvider(ctx context.Context, request CreateIdentityProviderRequestObject) (CreateIdentityProviderResponseObject, error) {
	ev := audit.Event{Action: audit.IdentityProviderCreate, ResourceType: "identity_provider"}
	if auth.RoleFromContext(ctx) != "admin" {
		h.audited(ctx, ev.Denied(audit.ReasonForbidden))
		return CreateIdentityProvider403JSONResponse{ForbiddenJSONResponse: ForbiddenJSONResponse{
			Code: ErrorCodeForbidden, Message: "admin only",
		}}, nil
	}

	body := request.Body
	if body == nil {
		return CreateIdentityProvider422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "request body is required")}, nil
	}
	name := body.Name
	if len(name) == 0 {
		return CreateIdentityProvider422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, "name is required")}, nil
	}

	cfg, err := parseProviderConfig(body)
	if err != nil {
		return CreateIdentityProvider422JSONResponse{UnprocessableEntityJSONResponse: unprocessable(ErrorCodeValidationError, err.Error())}, nil
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	if h.keyring == nil {
		return nil, errNoKeyring
	}

	// The config is sealed with the id the database generates as AAD.
	ap, err := h.providers.Create(ctx, name, string(body.Type), enabled, func(id pgtype.UUID) (string, error) {
		return sealProviderConfig(h.keyring, id, cfg)
	})
	if err != nil {
		return nil, err
	}

	detail, err := h.identityProviderDetail(ctx, ap)
	if err != nil {
		return nil, err
	}
	h.audited(ctx, withID(ev, ap.ID).Allowed())
	return CreateIdentityProvider201JSONResponse(detail), nil
}

// parseProviderConfig maps and validates the type-specific config. It runs
// before any write so an invalid provider never creates a row.
func parseProviderConfig(body *CreateIdentityProviderRequest) (providerConfig, error) {
	return parseProviderConfigParts(body.Type, body.Oidc, body.Ldap)
}

// parseProviderConfigParts is shared by create, transient test and update so a
// configuration is validated identically everywhere.
func parseProviderConfigParts(typ IdentityProviderType, oidc *OIDCConfig, ldap *LDAPConfig) (providerConfig, error) {
	switch typ {
	case IdentityProviderTypeOidc:
		if oidc == nil {
			return providerConfig{}, errors.New("oidc configuration is required")
		}
		if ldap != nil {
			return providerConfig{}, errors.New("ldap configuration does not belong to an oidc provider")
		}
		raw, err := json.Marshal(oidc)
		if err != nil {
			return providerConfig{}, err
		}
		var cfg auth.OIDCProviderConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return providerConfig{}, err
		}
		if err := cfg.Validate(); err != nil {
			return providerConfig{}, err
		}
		return providerConfig{oidc: &cfg}, nil

	case IdentityProviderTypeLdap:
		if ldap == nil {
			return providerConfig{}, errors.New("ldap configuration is required")
		}
		if oidc != nil {
			return providerConfig{}, errors.New("oidc configuration does not belong to an ldap provider")
		}
		raw, err := json.Marshal(ldap)
		if err != nil {
			return providerConfig{}, err
		}
		cfg, err := identityldap.Decode(raw)
		if err != nil {
			return providerConfig{}, err
		}
		return providerConfig{ldap: &cfg}, nil

	default:
		return providerConfig{}, fmt.Errorf("unknown provider type %q", typ)
	}
}

// sealProviderConfig encrypts a validated config bound to the provider id.
func sealProviderConfig(kr *crypto.Keyring, id pgtype.UUID, cfg providerConfig) (string, error) {
	switch {
	case cfg.oidc != nil:
		return auth.SealOIDCConfig(kr, idString(id), *cfg.oidc)
	case cfg.ldap != nil:
		return identityldap.SealConfig(kr, idString(id), *cfg.ldap)
	default:
		return "", errors.New("empty identity provider configuration")
	}
}

// identityProviderToAdmin builds the redacted list item. It decrypts the
// config only to report whether a secret is stored.
func (h *AuthHandlers) identityProviderToAdmin(ctx context.Context, ap db.AuthProvider) (IdentityProvider, error) {
	out := IdentityProvider{
		Id:        openapi_types.UUID(ap.ID.Bytes),
		Name:      ap.Name,
		Type:      IdentityProviderType(ap.Type),
		Enabled:   ap.Enabled,
		CreatedAt: pgTimestampToTime(ap.CreatedAt),
		UpdatedAt: pgTimestampToTimePtr(ap.UpdatedAt),
	}
	switch ap.Type {
	case "oidc":
		cfg, err := auth.OpenOIDCConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
		if err != nil {
			return out, err
		}
		out.HasClientSecret = cfg.ClientSecret != ""
	case "ldap":
		cfg, err := identityldap.OpenConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
		if err != nil {
			return out, err
		}
		out.HasBindPassword = cfg.BindPassword != ""
	}
	linked, err := h.providers.LinkedUsers(ctx, ap.ID)
	if err != nil {
		return out, err
	}
	out.LinkedUsers = int(linked)
	return out, nil
}

// identityProviderDetail builds the redacted detail. The stored config is
// marshalled and unmarshalled into the admin shape, whose secret field does not
// exist, so the secret is dropped by construction.
func (h *AuthHandlers) identityProviderDetail(ctx context.Context, ap db.AuthProvider) (IdentityProviderDetail, error) {
	item, err := h.identityProviderToAdmin(ctx, ap)
	if err != nil {
		return IdentityProviderDetail{}, err
	}
	out := IdentityProviderDetail{
		Id:              item.Id,
		Name:            item.Name,
		Type:            item.Type,
		Enabled:         item.Enabled,
		HasClientSecret: item.HasClientSecret,
		HasBindPassword: item.HasBindPassword,
		LinkedUsers:     item.LinkedUsers,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
	switch ap.Type {
	case "oidc":
		cfg, err := auth.OpenOIDCConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
		if err != nil {
			return out, err
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return out, err
		}
		var admin OIDCConfigAdmin
		if err := json.Unmarshal(raw, &admin); err != nil {
			return out, err
		}
		out.Oidc = &admin
	case "ldap":
		cfg, err := identityldap.OpenConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
		if err != nil {
			return out, err
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return out, err
		}
		var admin LDAPConfigAdmin
		if err := json.Unmarshal(raw, &admin); err != nil {
			return out, err
		}
		out.Ldap = &admin
	}
	return out, nil
}
