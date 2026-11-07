package connections

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// ErrDeniedNetwork means the destination resolved only to addresses the
// dialer refuses. The error never carries the resolved address.
var ErrDeniedNetwork = errors.New("connection_denied_network")

// Dialer is the only way providers and engines reach user-supplied hosts
// (docs/adr/0011). It resolves the host, drops denied addresses and dials a
// resolved IP, so there is no second lookup and no DNS-rebinding window.
//
// The dialer carries no network policy of its own: what is denied or allowed
// is decided by the operator through BALEIA_CONNECTIONS_DENY_CIDRS and
// BALEIA_CONNECTIONS_ALLOW_CIDRS (DialerOptions). The Helm chart ships safe
// defaults as values, never as code, so a deployment can reach its own
// internal networks.
type Dialer struct {
	// Deny is always refused; it wins over Allow.
	Deny []netip.Prefix
	// Allow, when non-empty, is the only space that may be dialed.
	Allow []netip.Prefix
	// Timeout bounds each connection attempt.
	Timeout time.Duration
	// LookupNetIP resolves host names; nil uses net.DefaultResolver.
	LookupNetIP func(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DialerOptions is the operator configuration of the dialer.
type DialerOptions struct {
	DenyCIDRs  []string // always refused, in addition to nothing else
	AllowCIDRs []string // if set, only these may be dialed
}

// NewDialer builds the dialer from operator options.
func NewDialer(o DialerOptions) (*Dialer, error) {
	d := &Dialer{Timeout: 10 * time.Second}
	var err error
	if d.Deny, err = parsePrefixes(o.DenyCIDRs); err != nil {
		return nil, fmt.Errorf("deny cidrs: %w", err)
	}
	if d.Allow, err = parsePrefixes(o.AllowCIDRs); err != nil {
		return nil, fmt.Errorf("allow cidrs: %w", err)
	}
	return d, nil
}

func parsePrefixes(in []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(in))
	for _, s := range in {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// Permitted reports whether the dialer may connect to addr.
func (d *Dialer) Permitted(addr netip.Addr) bool {
	// Prefix.Contains never matches an address with an IPv6 zone, so "::1%lo"
	// would slip past every deny prefix. Check the address without it.
	addr = addr.Unmap().WithZone("")
	for _, p := range d.Deny {
		if p.Contains(addr) {
			return false
		}
	}
	if len(d.Allow) == 0 {
		return true
	}
	// An explicit allow list is the only space that may be dialed.
	for _, p := range d.Allow {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// DialContext has the signature of net.Dialer.DialContext and is the hook
// every driver is given.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		addrs = []netip.Addr{ip}
	} else {
		lookup := d.LookupNetIP
		if lookup == nil {
			lookup = net.DefaultResolver.LookupNetIP
		}
		if addrs, err = lookup(ctx, ipNetwork(network), host); err != nil {
			return nil, err
		}
	}

	nd := net.Dialer{Timeout: d.Timeout}
	var lastErr error
	for _, a := range addrs {
		if !d.Permitted(a) {
			continue
		}
		conn, err := nd.DialContext(ctx, network, net.JoinHostPort(a.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%w: %s is not an allowed destination", ErrDeniedNetwork, host)
}

func ipNetwork(network string) string {
	switch network {
	case "tcp4", "udp4":
		return "ip4"
	case "tcp6", "udp6":
		return "ip6"
	}
	return "ip"
}

// HTTPClient returns a client whose every connection, including redirects,
// goes through the dialer. Environment proxies are ignored: a proxy would make
// the connection on the dialer's behalf.
func (d *Dialer) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           d.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}
