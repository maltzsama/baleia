package api

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
)

// biGatewayPublicURLReady reports whether raw is an http(s) origin the BI files
// can point users at: a scheme, a host and no path. The files carry only a host
// and port, so a path prefix cannot be expressed.
func biGatewayPublicURLReady(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// biReadiness is the configuration-only view of the gateway shared by the
// global and workspace UIs. It never carries the HMAC secret or the internal
// listener address, only stable booleans and blocker codes.
type biReadiness struct {
	ListenerConfigured bool
	SecretConfigured   bool
	PublicURLReady     bool
	PublicURL          string
	SwitchEnabled      bool
	// Blockers explain why the gateway is not ready, in a stable order.
	Blockers []BIGatewayBlocker
}

// configured reports whether the gateway can serve requests at all.
func (r biReadiness) configured() bool {
	return r.ListenerConfigured && r.SecretConfigured && r.PublicURLReady
}

// state is ready when configured and enabled, disabled when configured but the
// global switch is off, and blocked otherwise.
func (r biReadiness) state() BIGatewayReadinessState {
	switch {
	case !r.configured():
		return BIGatewayReadinessStateBlocked
	case !r.SwitchEnabled:
		return BIGatewayReadinessStateDisabled
	default:
		return BIGatewayReadinessStateReady
	}
}

// toAPI renders the shared readiness without the admin-only health and limits.
// It strips any userinfo from the public URL so a misconfiguration such as
// https://operator:secret@host never echoes the credential.
func (r biReadiness) toAPI() BIGatewayReadiness {
	var publicURL *string
	if r.PublicURL != "" {
		if parsed, err := url.Parse(r.PublicURL); err == nil {
			parsed.User = nil
			s := parsed.String()
			publicURL = &s
		}
	}
	blocked := make([]BIGatewayBlocker, len(r.Blockers))
	copy(blocked, r.Blockers)
	return BIGatewayReadiness{
		Configured:         r.configured(),
		ListenerConfigured: r.ListenerConfigured,
		SecretConfigured:   r.SecretConfigured,
		PublicUrlReady:     r.PublicURLReady,
		PublicUrl:          publicURL,
		Enabled:            r.SwitchEnabled,
		State:              r.state(),
		BlockedBy:          blocked,
	}
}

// biGatewayReadiness reads the global switch and resolves the readiness
// blockers (ADR 0017). A missing singleton is enabled.
func (h *core) biGatewayReadiness(ctx context.Context) (biReadiness, error) {
	g := h.settings.BIGateway
	r := biReadiness{
		ListenerConfigured: g.ListenerConfigured,
		SecretConfigured:   g.SecretConfigured,
		PublicURLReady:     g.URLReady,
		PublicURL:          g.URL,
	}
	if !r.ListenerConfigured {
		r.Blockers = append(r.Blockers, BIGatewayBlockerListenerNotConfigured)
	}
	if !r.SecretConfigured {
		r.Blockers = append(r.Blockers, BIGatewayBlockerSecretNotConfigured)
	}
	switch {
	case g.URL == "":
		r.Blockers = append(r.Blockers, BIGatewayBlockerPublicUrlNotConfigured)
	case !r.PublicURLReady:
		r.Blockers = append(r.Blockers, BIGatewayBlockerPublicUrlInvalid)
	}
	enabled, err := h.biGatewaySwitchEnabled(ctx)
	if err != nil {
		return r, err
	}
	r.SwitchEnabled = enabled
	if !enabled {
		r.Blockers = append(r.Blockers, BIGatewayBlockerGatewayDisabled)
	}
	return r, nil
}

// biGatewaySwitchEnabled reads the global switch; an absent singleton is
// enabled, matching the request path.
func (h *core) biGatewaySwitchEnabled(ctx context.Context) (bool, error) {
	store := h.biSettings()
	if store == nil {
		return true, nil
	}
	setting, err := store.GetBIGatewaySetting(ctx)
	if errors.Is(err, db.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return setting.Enabled, nil
}

// biGatewayStatus is the admin view: readiness plus health and concurrency
// limits. The health status is the readiness of the in-process listener, never
// a network probe of another host.
func (h *core) biGatewayStatus(ctx context.Context) (BIGatewayStatus, error) {
	r, err := h.biGatewayReadiness(ctx)
	if err != nil {
		return BIGatewayStatus{}, err
	}
	health := BIGatewayHealth{Status: BIGatewayHealthStatusOk, CheckedAt: time.Now().UTC()}
	if !r.ListenerConfigured {
		health.Status = BIGatewayHealthStatusUnknown
	}
	concurrency := BIGatewayConcurrency{
		MaxPerKey:       h.settings.BIGateway.MaxPerKey,
		MaxPerWorkspace: h.settings.BIGateway.MaxPerWorkspace,
	}
	if running, err := h.countRunningBI(ctx); err == nil {
		n := int(running)
		concurrency.Running = &n
	}
	api := r.toAPI()
	return BIGatewayStatus{
		Configured:         api.Configured,
		ListenerConfigured: api.ListenerConfigured,
		SecretConfigured:   api.SecretConfigured,
		PublicUrlReady:     api.PublicUrlReady,
		PublicUrl:          api.PublicUrl,
		Enabled:            api.Enabled,
		State:              BIGatewayStatusState(api.State),
		BlockedBy:          api.BlockedBy,
		Health:             health,
		Concurrency:        concurrency,
	}, nil
}

// biRunningCounter counts the deployment's running BI statements. It is a
// private interface so the readiness status does not widen db.Querier.
type biRunningCounter interface {
	CountRunningBIExecutions(ctx context.Context) (int64, error)
}

// countRunningBI reads the running BI executions, or reports an error when no
// store is available (the caller omits the count then).
func (h *core) countRunningBI(ctx context.Context) (int64, error) {
	if counter, ok := h.queries.(biRunningCounter); ok {
		return counter.CountRunningBIExecutions(ctx)
	}
	if h.store != nil {
		return repo.New(h.store).CountRunningBIExecutions(ctx)
	}
	return 0, errors.New("no store")
}
