package kafka

import (
	"context"
	"errors"
	"maps"
	"net"
	"slices"
	"testing"

	"github.com/maltzsama/baleia/internal/connections"
)

func newProvider(t *testing.T, o connections.DialerOptions) *Provider {
	t.Helper()
	d, err := connections.NewDialer(o)
	if err != nil {
		t.Fatal(err)
	}
	return New(d)
}

func invalidKeys(err error) []string {
	var ve *connections.ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	keys := make([]string, 0, len(ve.Fields))
	for _, f := range ve.Fields {
		keys = append(keys, f.Key)
	}
	return keys
}

func TestProvider_Meta(t *testing.T) {
	t.Parallel()
	p := newProvider(t, connections.DialerOptions{})
	meta := p.Meta()
	if !slices.Equal(meta.AccessModes, []connections.AccessMode{connections.AccessModeFederated}) {
		t.Errorf("access modes = %v, want federated only", meta.AccessModes)
	}
	if !slices.Equal(meta.TrinoCatalogTypes, []string{"kafka"}) {
		t.Errorf("trino catalog types = %v", meta.TrinoCatalogTypes)
	}
	caps := meta.Capabilities[connections.AccessModeFederated]
	for c, want := range map[connections.Capability]connections.CapabilityState{
		connections.CapabilityRead:        connections.CapabilitySupported,
		connections.CapabilityListTables:  connections.CapabilitySupported,
		connections.CapabilityInsert:      connections.CapabilityUnsupported,
		connections.CapabilityDelete:      connections.CapabilityUnsupported,
		connections.CapabilityCreateTable: connections.CapabilityUnsupported,
	} {
		if caps[c] != want {
			t.Errorf("capability %s = %q, want %q", c, caps[c], want)
		}
	}
	if !connections.TrinoBindable(p) {
		t.Error("kafka must be bindable into Trino")
	}
	for _, f := range p.CatalogSchema() {
		if f.Secret() {
			t.Errorf("catalog field %q is secret", f.Key)
		}
	}
}

func TestProvider_Validate(t *testing.T) {
	t.Parallel()
	p := newProvider(t, connections.DialerOptions{})
	tests := []struct {
		name     string
		values   map[string]string
		expected map[string]string
		invalid  []string
	}{
		{
			name:     "brokers normalized and plaintext by default",
			values:   map[string]string{KeyNodes: " kafka-0:9092 , kafka-1:9092,kafka-0:9092 "},
			expected: map[string]string{KeyNodes: "kafka-0:9092,kafka-1:9092", KeySecurityProtocol: ProtocolPlaintext},
		},
		{
			name:     "ipv6 broker and ssl",
			values:   map[string]string{KeyNodes: "[::1]:9093", KeySecurityProtocol: ProtocolSSL},
			expected: map[string]string{KeyNodes: "[::1]:9093", KeySecurityProtocol: ProtocolSSL},
		},
		{name: "no broker", values: map[string]string{KeyNodes: " , "}, invalid: []string{KeyNodes}},
		{name: "missing port", values: map[string]string{KeyNodes: "kafka-0"}, invalid: []string{KeyNodes}},
		{name: "bad port", values: map[string]string{KeyNodes: "kafka-0:0"}, invalid: []string{KeyNodes}},
		{name: "url instead of address", values: map[string]string{KeyNodes: "kafka://kafka-0:9092"}, invalid: []string{KeyNodes}},
		{
			name:    "a protocol that needs client files",
			values:  map[string]string{KeyNodes: "kafka-0:9092", KeySecurityProtocol: "SASL_SSL"},
			invalid: []string{KeySecurityProtocol},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := p.Validate(connections.Config{Kind: connections.KindKafka, Values: tt.values})
			if tt.invalid != nil {
				if got := invalidKeys(err); !slices.Equal(got, tt.invalid) {
					t.Fatalf("invalid = %v (err %v), want %v", got, err, tt.invalid)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(cfg.Values, tt.expected) {
				t.Errorf("values = %v, want %v", cfg.Values, tt.expected)
			}
		})
	}
}

func TestProvider_ValidateCatalog(t *testing.T) {
	t.Parallel()
	p := newProvider(t, connections.DialerOptions{})
	tests := []struct {
		name     string
		values   map[string]string
		expected map[string]string
		invalid  []string
	}{
		{
			name:     "topics sorted and deduplicated, default schema",
			values:   map[string]string{KeyTopics: " payments, orders ,payments,click-events "},
			expected: map[string]string{KeyTopics: "click-events,orders,payments", KeySchema: DefaultSchema},
		},
		{
			name:     "own schema",
			values:   map[string]string{KeyTopics: "orders", KeySchema: "events"},
			expected: map[string]string{KeyTopics: "orders", KeySchema: "events"},
		},
		{name: "no topic", values: map[string]string{KeyTopics: " , "}, invalid: []string{KeyTopics}},
		{name: "missing topics", values: map[string]string{}, invalid: []string{KeyTopics}},
		{name: "uppercase topic", values: map[string]string{KeyTopics: "Orders"}, invalid: []string{KeyTopics}},
		{name: "dotted topic", values: map[string]string{KeyTopics: "app.orders"}, invalid: []string{KeyTopics}},
		{name: "bad schema", values: map[string]string{KeyTopics: "orders", KeySchema: "Events"}, invalid: []string{KeySchema}},
		{name: "unknown value", values: map[string]string{KeyTopics: "orders", "kafka.nodes": "x:1"}, invalid: []string{"kafka.nodes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := p.ValidateCatalog(tt.values)
			if tt.invalid != nil {
				if keys := invalidKeys(err); !slices.Equal(keys, tt.invalid) {
					t.Fatalf("invalid = %v (err %v), want %v", keys, err, tt.invalid)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, tt.expected) {
				t.Errorf("values = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestProvider_Ping(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	open := newProvider(t, connections.DialerOptions{})
	cfg := connections.Config{Kind: connections.KindKafka, Values: map[string]string{KeyNodes: ln.Addr().String()}}
	if err := open.Ping(context.Background(), cfg); err != nil {
		t.Fatalf("ping a listening broker: %v", err)
	}

	denied := newProvider(t, connections.DialerOptions{DenyCIDRs: []string{"127.0.0.0/8"}})
	if err := denied.Ping(context.Background(), cfg); !errors.Is(err, connections.ErrDeniedNetwork) {
		t.Fatalf("ping a denied broker: err = %v, want ErrDeniedNetwork", err)
	}
}

func TestProvider_OpenMetadataUnsupported(t *testing.T) {
	t.Parallel()
	p := newProvider(t, connections.DialerOptions{})
	if _, err := p.OpenMetadata(context.Background(), connections.Config{}); !errors.Is(err, connections.ErrUnsupported) {
		t.Fatalf("OpenMetadata err = %v, want ErrUnsupported", err)
	}
}
