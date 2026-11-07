package connections

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func resolveTo(addrs ...string) func(context.Context, string, string) ([]netip.Addr, error) {
	return func(context.Context, string, string) ([]netip.Addr, error) {
		out := make([]netip.Addr, 0, len(addrs))
		for _, a := range addrs {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

// TestDialerDeniesConfiguredCIDRs: the deny list is the operator's policy; the
// dialer has no opinion of its own. Loopback, metadata and private ranges are
// reachable unless the operator names them in DenyCIDRs.
func TestDialerDeniesConfiguredCIDRs(t *testing.T) {
	t.Parallel()
	d, err := NewDialer(DialerOptions{DenyCIDRs: []string{"169.254.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := d.DialContext(context.Background(), "tcp", "169.254.169.254:80"); !errors.Is(err, ErrDeniedNetwork) {
		t.Errorf("169.254.169.254: want ErrDeniedNetwork, got %v", err)
	}

	// A hostname that resolves to a denied address is denied too, and the
	// error does not echo the resolved address.
	d.LookupNetIP = resolveTo("169.254.169.254")
	_, err = d.DialContext(context.Background(), "tcp", "metadata.example:80")
	if !errors.Is(err, ErrDeniedNetwork) {
		t.Fatalf("hostname: want ErrDeniedNetwork, got %v", err)
	}
	if strings.Contains(err.Error(), "169.254") {
		t.Errorf("error leaks the resolved address: %v", err)
	}
}

// TestDialerDefaultsPermitEverything: with no deny or allow list the dialer
// permits every destination; the deployer decides the policy via config.
func TestDialerDefaultsPermitEverything(t *testing.T) {
	t.Parallel()
	d, err := NewDialer(DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{
		"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", // public
		"10.0.0.1", "172.16.0.1", "192.168.1.1", "100.64.0.1", "fc00::1", // private/internal
		"127.0.0.1", "::1", "169.254.169.254", "fe80::1", // loopback/link-local
	} {
		if !d.Permitted(netip.MustParseAddr(addr)) {
			t.Errorf("%s: permitted by default", addr)
		}
	}
}

// TestDialerAllowListRestricts: an explicit allow list is the only space that
// may be dialed.
func TestDialerAllowListRestricts(t *testing.T) {
	t.Parallel()
	d, err := NewDialer(DialerOptions{AllowCIDRs: []string{"10.0.0.0/8", "192.168.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"10.1.2.3", "192.168.1.1"} {
		if !d.Permitted(netip.MustParseAddr(addr)) {
			t.Errorf("%s: allow list did not open the range", addr)
		}
	}
	for _, addr := range []string{"172.16.0.1", "100.64.0.1", "8.8.8.8"} {
		if d.Permitted(netip.MustParseAddr(addr)) {
			t.Errorf("%s: address outside the allow list permitted", addr)
		}
	}
}

// TestDialerDenyWinsOverAllow: the deny list is refused even when the allow
// list names it.
func TestDialerDenyWinsOverAllow(t *testing.T) {
	t.Parallel()
	d, err := NewDialer(DialerOptions{AllowCIDRs: []string{"0.0.0.0/0", "::/0"}, DenyCIDRs: []string{"169.254.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Permitted(netip.MustParseAddr("169.254.169.254")) {
		t.Error("denied address permitted by the allow list")
	}
	if !d.Permitted(netip.MustParseAddr("8.8.8.8")) {
		t.Error("allowed address denied")
	}
}

func TestDialerSkipsDeniedAddressesOfAHost(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	d, err := NewDialer(DialerOptions{DenyCIDRs: []string{"169.254.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	d.LookupNetIP = resolveTo("169.254.169.254", "127.0.0.1")
	conn, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort("mixed.example", port))
	if err != nil {
		t.Fatalf("want the permitted address dialed, got %v", err)
	}
	conn.Close()
}

func TestDialerOptions(t *testing.T) {
	t.Parallel()
	d, err := NewDialer(DialerOptions{
		DenyCIDRs:  []string{"10.1.0.0/16"},
		AllowCIDRs: []string{"10.0.0.0/8", "127.0.0.0/8"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"127.0.0.1":       true,
		"169.254.169.254": false, // outside the allow list
		"10.2.3.4":        true,
		"10.1.3.4":        false, // deny wins over allow
		"192.168.1.1":     false, // outside the allow list
	}
	for a, want := range cases {
		if got := d.Permitted(netip.MustParseAddr(a)); got != want {
			t.Errorf("%s: permitted=%v, want %v", a, got, want)
		}
	}

	if _, err := NewDialer(DialerOptions{DenyCIDRs: []string{"nope"}}); err == nil {
		t.Error("invalid CIDR accepted")
	}
	if _, err := NewDialer(DialerOptions{AllowCIDRs: []string{"nope"}}); err == nil {
		t.Error("invalid allow CIDR accepted")
	}
}

func TestHTTPClientUsesDialer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	d, err := NewDialer(DialerOptions{DenyCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	resp, err := d.HTTPClient(0).Get(srv.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrDeniedNetwork) {
		t.Fatalf("want ErrDeniedNetwork, got %v", err)
	}
}
