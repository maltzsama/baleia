package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sort"
	"strings"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/db"
)

// errConfigInvalid wraps every request-config failure that answers 422.
var errConfigInvalid = errors.New("invalid connection config")

// configError is a 422 answer: a code and the invalid fields.
type configError struct {
	code   ErrorCode
	fields []connections.FieldError
}

func (e *configError) Error() string { return string(e.code) }
func (e *configError) Unwrap() error { return errConfigInvalid }

func (e *configError) response() UnprocessableEntityJSONResponse {
	messages := map[ErrorCode]string{
		"provider_unsupported": "no provider is available for this connection type",
		"engine_unsupported":   "this connection cannot back an engine",
		"engine_unreachable":   "Baleia could not reach the engine",
	}
	out := UnprocessableEntityJSONResponse{Code: e.code, Message: "invalid connection config"}
	if m, ok := messages[e.code]; ok {
		out.Message = m
	}
	for _, f := range e.fields {
		field, msg := f.Key, f.Message
		out.Errors = append(out.Errors, struct {
			Field   *string `json:"field,omitempty"`
			Message *string `json:"message,omitempty"`
		}{Field: &field, Message: &msg})
	}
	if out.Errors == nil {
		out.Errors = []struct {
			Field   *string `json:"field,omitempty"`
			Message *string `json:"message,omitempty"`
		}{}
	}
	return out
}

// provider returns the provider of a kind the API may create connections for.
func (h *core) provider(kind string) (connections.Provider, error) {
	p, err := h.connections.Get(connections.Kind(kind))
	if err != nil {
		return nil, &configError{code: ErrorCodeProviderUnsupported, fields: []connections.FieldError{{Key: "type", Message: fmt.Sprintf("%q has no provider", kind)}}}
	}
	// Only implemented types are creatable (ADR 0021 CR-12): planned,
	// unsupported and not-applicable types are declared in the registry with an
	// explicit status, but creation is rejected here.
	if st := p.Status(); st.State != connections.StateSupported && st.State != connections.StateExperimental {
		msg := st.Reason
		if msg == "" {
			msg = fmt.Sprintf("%q is %s and cannot be created", kind, st.State)
		}
		return nil, &configError{code: ErrorCodeProviderUnsupported, fields: []connections.FieldError{{Key: "type", Message: msg}}}
	}
	return p, nil
}

// accessMode resolves the data access mode of a new connection against the
// provider's declared modes (ADR 0021 D.2, CR-02): required when the type
// declares two, auto-selected when it declares one, and nil for engine-family
// types, which are not a data access path.
func accessMode(p connections.Provider, requested string) (*string, error) {
	modes := p.Meta().AccessModes
	if len(modes) == 0 {
		if requested != "" {
			return nil, &configError{code: ErrorCodeValidationError, fields: []connections.FieldError{{Key: "access_mode", Message: "this connection type has no data access mode"}}}
		}
		return nil, nil
	}
	if requested != "" {
		for _, m := range modes {
			if requested == string(m) {
				mode := requested
				return &mode, nil
			}
		}
		return nil, &configError{code: ErrorCodeValidationError, fields: []connections.FieldError{{Key: "access_mode", Message: "must be " + accessModeList(modes)}}}
	}
	if len(modes) > 1 {
		return nil, &configError{code: ErrorCodeValidationError, fields: []connections.FieldError{{Key: "access_mode", Message: "required; choose " + accessModeList(modes)}}}
	}
	mode := string(modes[0])
	return &mode, nil
}

func accessModeList(modes []connections.AccessMode) string {
	names := make([]string, len(modes))
	for i, m := range modes {
		names[i] = string(m)
	}
	return strings.Join(names, " or ")
}

// validate runs the provider's validation and maps its failure to a 422.
func validate(p connections.Provider, cfg connections.Config) (connections.Config, error) {
	out, err := p.Validate(cfg)
	var ve *connections.ValidationError
	if errors.As(err, &ve) {
		return connections.Config{}, &configError{code: ErrorCodeValidationError, fields: ve.Fields}
	}
	return out, err
}

// asConfigError maps a request-shape failure (a malformed JDBC URL) to a 422.
func asConfigError(err error) error {
	var ve *connections.ValidationError
	if errors.As(err, &ve) {
		return &configError{code: ErrorCodeValidationError, fields: ve.Fields}
	}
	return err
}

// createConfig builds and validates the config of a create request.
func (h *core) createConfig(id db.UUID, body *CreateCatalogConnectionRequest) (connections.Config, error) {
	p, err := h.provider(body.Type)
	if err != nil {
		return connections.Config{}, err
	}
	var rawURL string
	if body.Url != nil {
		rawURL = *body.Url
	}
	var values map[string]string
	if body.Config != nil {
		values = *body.Config
	}
	creds, err := legacyCredentials(body, maps.Clone(h.settings.Execution.PolarisDefaultCredentials))
	if err != nil {
		return connections.Config{}, err
	}
	cfg, err := providers.FromRequest(h.connections, p.Kind(), id, rawURL, values, creds)
	if err != nil {
		return connections.Config{}, asConfigError(err)
	}
	return validate(p, cfg)
}

// legacyCredentials returns the credentials object of a create request as a
// map: the typed field for the type, else the generic one, else (polaris) the
// POLARIS_CLIENT_ID / POLARIS_CLIENT_SECRET defaults.
func legacyCredentials(body *CreateCatalogConnectionRequest, polarisDefault map[string]string) (map[string]any, error) {
	v := createCredentials(body, polarisDefault)
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// updateConfig merges a PATCH into the stored config and validates the result.
func (h *core) updateConfig(conn db.CatalogConnection, body UpdateCatalogConnectionRequest) (connections.Config, error) {
	p, err := h.provider(conn.Type)
	if err != nil {
		return connections.Config{}, err
	}
	plain, err := catalogs.OpenCredentials(conn, h.keyring)
	if err != nil {
		return connections.Config{}, err
	}
	cur, err := providers.FromStored(h.connections, p.Kind(), conn.ID, conn.Url, conn.Config, plain)
	if err != nil {
		return connections.Config{}, err
	}
	var values map[string]string
	if body.Config != nil {
		values = *body.Config
	}
	var creds map[string]any
	if body.Credentials != nil {
		creds = *body.Credentials
	}
	merged, err := providers.Merge(h.connections, cur, body.Url, values, creds)
	if err != nil {
		return connections.Config{}, asConfigError(err)
	}
	return validate(p, merged)
}

// storedColumns is a validated config as catalog_connections columns: config
// JSON, the url the Trino projection still reads, and the sealed secrets (nil
// when there are none, which keeps any stored value on update).
func storedColumns(h *core, cfg connections.Config) (config []byte, url string, credsEnc *string, err error) {
	config, err = json.Marshal(cfg.Values)
	if err != nil {
		return nil, "", nil, err
	}
	if len(cfg.Secrets) > 0 {
		credsEnc, err = encodeCredentials(h.keyring, cfg.Secrets, cfg.ConnectionID)
		if err != nil {
			return nil, "", nil, err
		}
	}
	return config, providers.LegacyURL(cfg), credsEnc, nil
}

// publicConfig returns the stored non-secret values for responses.
func publicConfig(logger *slog.Logger, raw []byte) map[string]string {
	out := map[string]string{}
	decodeStored(logger, raw, &out, "connections.config")
	return out
}

// ListConnectionProviders returns every provider with its config schema.
// Static metadata: any authenticated caller may read it.
func (h *CatalogHandlers) ListConnectionProviders(ctx context.Context, _ ListConnectionProvidersRequestObject) (ListConnectionProvidersResponseObject, error) {
	all := h.connections.All()
	out := make([]ConnectionProvider, 0, len(all))
	for _, p := range all {
		out = append(out, providerToPublic(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return ListConnectionProviders200JSONResponse(out), nil
}

func providerToPublic(p connections.Provider) ConnectionProvider {
	meta := p.Meta()
	cp := ConnectionProvider{
		Kind:         string(p.Kind()),
		Label:        p.Label(),
		Family:       ConnectionProviderFamily(p.Family()),
		Roles:        []ConnectionProviderRoles{},
		ConfigSchema: []ConnectionProviderField{},
	}
	if meta.Description != "" {
		d := meta.Description
		cp.Description = &d
	}
	if meta.Category != "" {
		c := ConnectionProviderCategory(meta.Category)
		cp.Category = &c
	}
	if len(meta.AccessModes) > 0 {
		modes := make([]ConnectionProviderAccessModes, 0, len(meta.AccessModes))
		for _, m := range meta.AccessModes {
			modes = append(modes, ConnectionProviderAccessModes(m))
		}
		cp.AccessModes = &modes
	}
	if len(meta.TrinoCatalogTypes) > 0 {
		types := append([]string(nil), meta.TrinoCatalogTypes...)
		cp.TrinoCatalogTypes = &types
	}
	if len(meta.Capabilities) > 0 {
		caps := make(map[string]map[string]ConnectionProviderCapabilities, len(meta.Capabilities))
		for mode, declared := range meta.Capabilities {
			inner := make(map[string]ConnectionProviderCapabilities, len(declared))
			for capability, state := range declared {
				inner[string(capability)] = ConnectionProviderCapabilities(state)
			}
			caps[string(mode)] = inner
		}
		cp.Capabilities = &caps
	}
	if meta.VersionRequirements.MinEngineVersion != "" || meta.VersionRequirements.MinTrinoVersion != "" {
		vr := &struct {
			MinEngineVersion *string `json:"min_engine_version,omitempty"`
			MinTrinoVersion  *string `json:"min_trino_version,omitempty"`
		}{}
		if v := meta.VersionRequirements.MinEngineVersion; v != "" {
			vr.MinEngineVersion = &v
		}
		if v := meta.VersionRequirements.MinTrinoVersion; v != "" {
			vr.MinTrinoVersion = &v
		}
		cp.VersionRequirements = vr
	}
	for _, r := range p.Roles() {
		cp.Roles = append(cp.Roles, ConnectionProviderRoles(r))
	}
	if ek := p.EngineKind(); ek != "" {
		cp.EngineKind = &ek
	}
	st := p.Status()
	cp.Status.State = ConnectionProviderStatusState(st.State)
	if st.Reason != "" {
		reason := st.Reason
		cp.Status.Reason = &reason
	}
	for _, f := range p.ConfigSchema() {
		pf := ConnectionProviderField{
			Key:      f.Key,
			Label:    f.Label,
			Type:     ConnectionProviderFieldType(f.Type),
			Required: f.Required,
			Secret:   f.Secret(),
		}
		if f.Default != "" {
			d := f.Default
			pf.Default = &d
		}
		if f.Help != "" {
			help := f.Help
			pf.Help = &help
		}
		if len(f.Options) > 0 {
			opts := append([]string(nil), f.Options...)
			pf.Options = &opts
		}
		cp.ConfigSchema = append(cp.ConfigSchema, pf)
	}
	return cp
}
