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

func TestDialerDeniesMetadataIP(t *testing.T) {
	d, err := NewDialer(DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}

	for _, addr := range []string{
		"169.254.169.254:80",
		"127.0.0.1:5432",
		"[::1]:5432",
		"[::ffff:127.0.0.1]:5432",
		"[fd00:ec2::254]:80",
		"[::1%lo]:5432",
		"[fd00:ec2::254%eth0]:80",
		"[fe80::1%eth0]:80",
		"0.0.0.0:80",
	} {
		if _, err := d.DialContext(context.Background(), "tcp", addr); !errors.Is(err, ErrDeniedNetwork) {
			t.Errorf("%s: want ErrDeniedNetwork, got %v", addr, err)
		}
	}

	// A hostname that resolves to a denied address is denied too, and the
	// error does not echo the address.
	d.LookupNetIP = resolveTo("169.254.169.254")
	_, err = d.DialContext(context.Background(), "tcp", "metadata.example:80")
	if !errors.Is(err, ErrDeniedNetwork) {
		t.Fatalf("hostname: want ErrDeniedNetwork, got %v", err)
	}
	if strings.Contains(err.Error(), "169.254") {
		t.Errorf("error leaks the resolved address: %v", err)
	}
}

func TestDialerSkipsDeniedAddressesOfAHost(t *testing.T) {
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

	d, err := NewDialer(DialerOptions{AllowLoopback: true})
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
	d, err := NewDialer(DialerOptions{
		AllowLoopback: true,
		DenyCIDRs:     []string{"10.1.0.0/16"},
		AllowCIDRs:    []string{"10.0.0.0/8", "127.0.0.0/8"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"127.0.0.1":       true,  // loopback allowed in dev
		"169.254.169.254": false, // metadata stays denied
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
}

func TestHTTPClientUsesDialer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	d, err := NewDialer(DialerOptions{})
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
