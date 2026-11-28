// Package kafka is the Apache Kafka provider (#1064): a Kafka cluster exposed
// as a federated, read-only source through Trino's Kafka connector. Baleia
// runs no Kafka client: it neither consumes nor produces, and reads no
// metadata from the brokers. Topics reach the metadata snapshot by
// introspecting the engine that hosts a binding (docs/adr/0002), as for a
// Hive Metastore.
//
// The connection holds the brokers and the security protocol; each catalog
// holds its topic set (connections.CatalogConfigurer). Without table
// description files, which this provider does not ship, a topic is a table of
// the connector's internal columns (_key, _message, _timestamp, …).
//
// Trino 484 Kafka connector: https://trino.io/docs/484/connector/kafka.html
package kafka

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"

	"github.com/maltzsama/baleia/internal/connections"
)

// Config and catalog keys.
const (
	KeyNodes            = "nodes"
	KeySecurityProtocol = "security_protocol"
	KeyTopics           = "topics"
	KeySchema           = "schema"
)

// Security protocols Trino's Kafka connector accepts without client
// configuration files on its nodes.
const (
	ProtocolPlaintext = "PLAINTEXT"
	ProtocolSSL       = "SSL"
)

// DefaultSchema is the schema of a catalog's topics when it names none.
const DefaultSchema = "default"

var (
	// topicRe is a topic Trino can address as an unqualified table name:
	// lowercase, since Trino folds identifiers, and without a dot, which the
	// connector reads as a schema qualifier.
	topicRe  = regexp.MustCompile(`^[a-z0-9_][a-z0-9_-]{0,248}$`)
	schemaRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

// Provider is the Kafka provider.
type Provider struct {
	dialer *connections.Dialer
}

// New returns the Kafka provider, dialing through d.
func New(d *connections.Dialer) *Provider { return &Provider{dialer: d} }

func (*Provider) Kind() connections.Kind     { return connections.KindKafka }
func (*Provider) Label() string              { return "Apache Kafka" }
func (*Provider) Family() connections.Family { return connections.FamilyDatabase }

// Roles is empty: Kafka is only a source to bind into a federated engine.
func (*Provider) Roles() []connections.Role { return nil }

func (*Provider) EngineKind() string { return "" }

// Meta implements connections.Provider (ADR 0021). The connector reads
// topics; it writes only through table description files, which this
// provider does not ship, so every write capability is unsupported.
func (*Provider) Meta() connections.TypeMeta {
	modes := []connections.AccessMode{connections.AccessModeFederated}
	caps := connections.DefaultCapabilities(true)
	for _, c := range []connections.Capability{
		connections.CapabilityListSchemas, connections.CapabilityListTables, connections.CapabilityRead,
	} {
		caps[c] = connections.CapabilitySupported
	}
	for _, c := range []connections.Capability{
		connections.CapabilityInsert, connections.CapabilityUpdate, connections.CapabilityDelete,
		connections.CapabilityCreateTable, connections.CapabilityDropTable, connections.CapabilityAlterTable,
		connections.CapabilityTransactions, connections.CapabilityViews,
	} {
		caps[c] = connections.CapabilityUnsupported
	}
	return connections.TypeMeta{
		Description:         "Apache Kafka topics, read as tables through Trino.",
		Category:            connections.CategoryStreaming,
		AccessModes:         modes,
		TrinoCatalogTypes:   []string{"kafka"},
		Capabilities:        connections.Capabilities{connections.AccessModeFederated: caps},
		VersionRequirements: connections.VersionRequirements{MinEngineVersion: "3.3", MinTrinoVersion: "484"},
	}
}

// Status is supported: the configuration, the catalog mapping and the
// engine's runtime behavior (bind, read, refused writes, reload at the
// coordinator's boot) are validated on Trino 484 by the minikube e2e suite
// (ADR 0021 D.9).
func (*Provider) Status() connections.Status {
	return connections.Status{State: connections.StateSupported}
}

func (*Provider) ConfigSchema() []connections.Field {
	return []connections.Field{
		{
			Key: KeyNodes, Label: "Bootstrap brokers", Type: connections.FieldText, Required: true,
			Help: "Comma-separated host:port list, e.g. kafka-0:9092,kafka-1:9092. Every Trino node must reach every broker.",
		},
		{
			Key: KeySecurityProtocol, Label: "Security protocol", Type: connections.FieldSelect,
			Options: []string{ProtocolPlaintext, ProtocolSSL}, Default: ProtocolPlaintext,
			Help: "SSL checks the brokers against the Trino JVM's default trust store.",
		},
	}
}

func (p *Provider) Validate(cfg connections.Config) (connections.Config, error) {
	out, err := connections.ValidateSchema(p.ConfigSchema(), cfg)
	if err != nil {
		return out, err
	}
	nodes, err := Nodes(out.Value(KeyNodes))
	if err != nil {
		return connections.Config{}, &connections.ValidationError{Fields: []connections.FieldError{{Key: KeyNodes, Message: err.Error()}}}
	}
	out.Values[KeyNodes] = strings.Join(nodes, ",")
	return out, nil
}

// CatalogSchema is the topic set of one catalog.
func (*Provider) CatalogSchema() []connections.Field {
	return []connections.Field{
		{
			Key: KeyTopics, Label: "Topics", Type: connections.FieldText, Required: true,
			Help: "Comma-separated topics to read as tables. Each topic is a table of the connector's internal columns (_key, _message, _timestamp, …).",
		},
		{
			Key: KeySchema, Label: "Schema", Type: connections.FieldText, Default: DefaultSchema,
			Help: "Schema the topics are listed under.",
		},
	}
}

// ValidateCatalog normalizes a catalog's topic set: topics trimmed,
// deduplicated and sorted.
func (p *Provider) ValidateCatalog(values map[string]string) (map[string]string, error) {
	out, err := connections.ValidateCatalogSchema(p.CatalogSchema(), values)
	if err != nil {
		return nil, err
	}
	var errs []connections.FieldError
	topics, err := Topics(out[KeyTopics])
	if err != nil {
		errs = append(errs, connections.FieldError{Key: KeyTopics, Message: err.Error()})
	}
	if !schemaRe.MatchString(out[KeySchema]) {
		errs = append(errs, connections.FieldError{Key: KeySchema, Message: "must match " + schemaRe.String()})
	}
	if len(errs) > 0 {
		return nil, &connections.ValidationError{Fields: errs}
	}
	out[KeyTopics] = strings.Join(topics, ",")
	return out, nil
}

// Nodes parses a comma-separated broker list into host:port addresses, in
// the order given, without duplicates.
func Nodes(raw string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		node := strings.TrimSpace(part)
		if node == "" {
			continue
		}
		host, port, err := net.SplitHostPort(node)
		if err != nil || host == "" || strings.ContainsAny(host, "/@ ") {
			return nil, fmt.Errorf("%q is not a host:port address", node)
		}
		if err := connections.Port(port); err != nil {
			return nil, fmt.Errorf("%q: %w", node, err)
		}
		addr := net.JoinHostPort(host, port)
		if !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least one broker is required")
	}
	return out, nil
}

// Topics parses a comma-separated topic list, sorted and without duplicates.
func Topics(raw string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		topic := strings.TrimSpace(part)
		if topic == "" {
			continue
		}
		if !topicRe.MatchString(topic) {
			return nil, fmt.Errorf("%q: a topic must be lowercase letters, digits, _ or -, without dots", topic)
		}
		if !seen[topic] {
			seen[topic] = true
			out = append(out, topic)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least one topic is required")
	}
	slices.Sort(out)
	return out, nil
}

// Ping checks TCP reachability of every broker through the dialer. It speaks
// no Kafka protocol; a bind check proves that the engine reaches the brokers.
func (p *Provider) Ping(ctx context.Context, cfg connections.Config) error {
	nodes, err := Nodes(cfg.Value(KeyNodes))
	if err != nil {
		return err
	}
	for _, addr := range nodes {
		conn, err := p.dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("broker %s: %w", addr, err)
		}
		if err := conn.Close(); err != nil {
			return fmt.Errorf("broker %s: %w", addr, err)
		}
	}
	return nil
}

// OpenMetadata is unsupported: topics are read through the engine that hosts
// a binding, never from the brokers.
func (*Provider) OpenMetadata(context.Context, connections.Config) (connections.MetadataReader, error) {
	return nil, connections.ErrUnsupported
}
