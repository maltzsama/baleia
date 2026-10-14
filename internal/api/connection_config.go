package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// errConfigInvalid wraps every request-config failure that answers 422.
var errConfigInvalid = errors.New("invalid connection config")

// configError is a 422 answer: a code and the invalid fields.
type configError struct {
	code   string
	fields []connections.FieldError
}

func (e *configError) Error() string { return e.code }
func (e *configError) Unwrap() error { return errConfigInvalid }

func (e *configError) response() UnprocessableEntityJSONResponse {
	out := UnprocessableEntityJSONResponse{Code: e.code, Message: "invalid connection config"}
	if e.code == "provider_unsupported" {
		out.Message = "no provider is available for this connection type"
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
func (h *Handlers) provider(kind string) (connections.Provider, error) {
	p, err := h.connections.Get(connections.Kind(kind))
	if err != nil {
		return nil, &configError{code: "provider_unsupported", fields: []connections.FieldError{{Key: "type", Message: fmt.Sprintf("%q has no provider", kind)}}}
	}
	if st := p.Status(); st.State == connections.StateUnsupported {
		return nil, &configError{code: "provider_unsupported", fields: []connections.FieldError{{Key: "type", Message: st.Reason}}}
	}
	return p, nil
}

// validate runs the provider's validation and maps its failure to a 422.
func validate(p connections.Provider, cfg connections.Config) (connections.Config, error) {
	out, err := p.Validate(cfg)
	var ve *connections.ValidationError
	if errors.As(err, &ve) {
		return connections.Config{}, &configError{code: "validation_error", fields: ve.Fields}
	}
	return out, err
}

// asConfigError maps a request-shape failure (a malformed JDBC URL) to a 422.
func asConfigError(err error) error {
	var ve *connections.ValidationError
	if errors.As(err, &ve) {
		return &configError{code: "validation_error", fields: ve.Fields}
	}
	return err
}

// createConfig builds and validates the config of a create request.
func (h *Handlers) createConfig(id pgtype.UUID, body *CreateCatalogConnectionRequest) (connections.Config, error) {
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
	creds, err := legacyCredentials(body)
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
func legacyCredentials(body *CreateCatalogConnectionRequest) (map[string]any, error) {
	v := createCredentials(body)
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
func (h *Handlers) updateConfig(conn db.CatalogConnection, body UpdateCatalogConnectionRequest) (connections.Config, error) {
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
func storedColumns(h *Handlers, cfg connections.Config) (config []byte, url string, credsEnc *string, err error) {
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
func publicConfig(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// ListConnectionProviders returns every provider with its config schema.
// Static metadata: any authenticated caller may read it.
func (h *Handlers) ListConnectionProviders(ctx context.Context, _ ListConnectionProvidersRequestObject) (ListConnectionProvidersResponseObject, error) {
	all := h.connections.All()
	out := make([]ConnectionProvider, 0, len(all))
	for _, p := range all {
		out = append(out, providerToPublic(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return ListConnectionProviders200JSONResponse(out), nil
}

func providerToPublic(p connections.Provider) ConnectionProvider {
	cp := ConnectionProvider{
		Kind:         string(p.Kind()),
		Label:        p.Label(),
		Family:       ConnectionProviderFamily(p.Family()),
		Roles:        []ConnectionProviderRoles{},
		ConfigSchema: []ConnectionProviderField{},
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
