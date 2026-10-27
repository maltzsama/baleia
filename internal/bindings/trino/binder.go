package trino

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	trinoclient "github.com/galileostudio/baleia/internal/trino"
	"github.com/jackc/pgx/v5"
)

// RegistryStore is the plugin contract's registry (docs/adr/0003):
// trino_catalog_registry rows carry REAL property values, which the plugin
// reads to load catalogs and resolve @baleia-secret placeholders.
type RegistryStore interface {
	UpsertTrinoCatalogRegistry(ctx context.Context, arg db.UpsertTrinoCatalogRegistryParams) error
	SoftDeleteTrinoCatalogRegistry(ctx context.Context, arg db.SoftDeleteTrinoCatalogRegistryParams) error
	GetTrinoCatalogRegistryState(ctx context.Context, arg db.GetTrinoCatalogRegistryStateParams) (db.GetTrinoCatalogRegistryStateRow, error)
}

// Binder is the Trino binder: registry row plus CREATE CATALOG, run as the
// admin identity (the only one the catalog guard lets manage catalogs,
// docs/adr/0005).
type Binder struct {
	logger *slog.Logger
	admin  *trinoclient.Client
	store  RegistryStore
	// registry seals catalog-origin secret values before they are written to
	// trino_catalog_registry (Phase B of #31 / D4). Nil keeps Phase A: rows
	// carry plaintext.
	registry *crypto.Keyring
}

var _ bindings.Binder = (*Binder)(nil)

// New is built at the wiring sites.
func New(logger *slog.Logger, admin *trinoclient.Client, store RegistryStore) *Binder {
	return &Binder{logger: cmp.Or(logger, slog.New(slog.DiscardHandler)), admin: admin, store: store}
}

// WithRegistryKeyring enables Phase B encryption of registry values. A nil
// keyring is a no-op.
func (b *Binder) WithRegistryKeyring(kr *crypto.Keyring) *Binder {
	return &Binder{admin: b.admin, store: b.store, registry: kr}
}

// WithStore is the binder writing the registry through store, e.g. a
// transaction that also writes the catalog row.
func (b *Binder) WithStore(store RegistryStore) *Binder {
	return &Binder{admin: b.admin, store: store, registry: b.registry}
}

func (*Binder) EngineKind() string { return "trino" }

func (*Binder) Supports() map[connections.Kind][]string {
	return map[connections.Kind][]string{
		connections.KindPolaris:       {"iceberg"},
		connections.KindNessie:        {"iceberg"},
		connections.KindIcebergREST:   {"iceberg"},
		connections.KindHiveMetastore: {"iceberg", "hive", "delta_lake"},
		connections.KindGlue:          {"iceberg", "hive", "delta_lake"},
		connections.KindLakekeeper:    {"iceberg"},
		connections.KindUnity:         {"iceberg"},
		connections.KindPostgreSQL:    {"postgresql"},
		connections.KindMySQL:         {"mysql"},
		connections.KindMariaDB:       {"mariadb"},
		connections.KindSQLServer:     {"sqlserver"},
		connections.KindOracle:        {"oracle"},
	}
}

func (*Binder) ProtectedKeyPatterns() []string { return slices.Clone(protectedPatterns) }
func (*Binder) ReservedNames() []string        { return slices.Clone(reservedNames) }

// ReadOnlySources lists the bindable kinds whose bindings are read-only:
// Baleia does not support writing through them. Unity Catalog is read-oriented
// (#32). The API exposes it so the UI can warn.
func (*Binder) ReadOnlySources() map[connections.Kind]bool {
	return map[connections.Kind]bool{connections.KindUnity: true}
}

// Project builds the registry properties (REAL values) of a binding, the keys
// the DDL text replaces with placeholders, and the plugin's catalog_version.
func (b *Binder) Project(spec bindings.BindingSpec, src connections.Config) (bindings.Projection, error) {
	if !nameRe.MatchString(spec.EngineCatalog) {
		return bindings.Projection{}, fmt.Errorf("%w: catalog name %q must match %s", bindings.ErrInvalid, spec.EngineCatalog, nameRe)
	}
	if slices.Contains(reservedNames, spec.EngineCatalog) {
		return bindings.Projection{}, fmt.Errorf("%w: %q", bindings.ErrReservedName, spec.EngineCatalog)
	}
	connector, err := bindings.Connector(b, src.Kind, spec.Connector)
	if err != nil {
		return bindings.Projection{}, err
	}
	p, err := b.project(spec, src, connector)
	if err != nil {
		return bindings.Projection{}, err
	}
	if err := mergeExtra(&p, spec.ExtraProps); err != nil {
		return bindings.Projection{}, err
	}
	// The version hashes the values Baleia resolved, not the external
	// references the properties carry: the plugin resolves them the same way and
	// hashes the resolved map (#31, ADR 0003).
	versionProps := p.props
	if len(p.refs) > 0 {
		versionProps = make(map[string]string, len(p.props))
		for k, v := range p.props {
			versionProps[k] = v
		}
		for k, v := range p.refs {
			versionProps[k] = v
		}
	}
	return bindings.Projection{
		EngineCatalog: spec.EngineCatalog,
		Connector:     connector,
		Properties:    p.props,
		SecretKeys:    p.secrets,
		Version:       Version(spec.EngineCatalog, connector, versionProps),
	}, nil
}

// registryEnvelopePrefix marks a registry property sealed at rest (Phase B of
// #31 / D4). The value has the form @baleia-enc[v1:<key-id>:<b64>], which the
// plugin's SecretResolver decrypts at boot as another secret origin.
const registryEnvelopePrefix = "@baleia-enc["

// sealedProperties returns the properties to persist, sealing the value of
// every secret key when a registry keyring is configured. It never mutates p
// (the DDL path still needs the plaintext), and leaves external references and
// already-sealed values untouched. The catalog_version is unaffected: Project
// hashes the resolved values before this runs, and the plugin hashes the
// decrypted values it loads.
func (b *Binder) sealedProperties(p bindings.Projection) (map[string]string, error) {
	if b.registry == nil || len(p.SecretKeys) == 0 {
		return p.Properties, nil
	}
	props := make(map[string]string, len(p.Properties))
	for k, v := range p.Properties {
		props[k] = v
	}
	for _, key := range p.SecretKeys {
		v, ok := props[key]
		if !ok || v == "" || strings.HasPrefix(v, "@baleia-secret[") || strings.HasPrefix(v, registryEnvelopePrefix) {
			continue
		}
		sealed, err := b.registry.Seal([]byte(v), "")
		if err != nil {
			return nil, fmt.Errorf("seal registry property %q: %w", key, err)
		}
		props[key] = registryEnvelopePrefix + sealed + "]"
	}
	return props, nil
}

// Stage writes the registry row; Apply sends CREATE CATALOG. Create is both.
// Callers that write the catalog row in the same transaction Stage through
// WithStore(tx) and Apply after commit: the plugin resolves placeholders from
// an enabled row, so the row always exists before the DDL.
func (b *Binder) Stage(ctx context.Context, eng bindings.EngineRef, p bindings.Projection) error {
	props, err := b.sealedProperties(p)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(props)
	if err != nil {
		return fmt.Errorf("marshal properties: %w", err)
	}
	version := p.Version
	if err := b.store.UpsertTrinoCatalogRegistry(ctx, db.UpsertTrinoCatalogRegistryParams{
		ClusterID:      eng.ClusterID,
		CatalogName:    p.EngineCatalog,
		ConnectorName:  p.Connector,
		Column4:        raw,
		CatalogVersion: &version,
	}); err != nil {
		return fmt.Errorf("upsert registry: %w", err)
	}
	return nil
}

// Apply sends CREATE CATALOG with @baleia-secret placeholders for the secret
// keys. It never writes the registry afterwards: the plugin's write is the
// confirmation (Confirm).
func (b *Binder) Apply(ctx context.Context, p bindings.Projection) error {
	err := b.admin.CreateCatalog(ctx, trinoclient.CatalogSpec{
		Name:       p.EngineCatalog,
		Connector:  p.Connector,
		Properties: p.Properties,
		SecretKeys: p.SecretKeys,
	})
	if errors.Is(err, trinoclient.ErrCatalogExists) {
		return fmt.Errorf("%w: %w", bindings.ErrCatalogExists, err)
	}
	return err
}

func (b *Binder) Create(ctx context.Context, eng bindings.EngineRef, p bindings.Projection) error {
	if err := b.Stage(ctx, eng, p); err != nil {
		return err
	}
	return b.Apply(ctx, p)
}

// Replace re-creates the catalog with a new projection. The order is
// load-bearing: the plugin soft-disables the row on DROP CATALOG, so the row
// is upserted (re-enabled, new properties) between DROP and CREATE.
func (b *Binder) Replace(ctx context.Context, eng bindings.EngineRef, p bindings.Projection) error {
	if err := b.admin.DropCatalog(ctx, p.EngineCatalog); err != nil {
		b.logger.WarnContext(ctx, "drop catalog before re-create", "catalog", p.EngineCatalog, "err", err)
	}
	return b.Create(ctx, eng, p)
}

// Rename creates the catalog under its new name, then drops the old one.
func (b *Binder) Rename(ctx context.Context, eng bindings.EngineRef, oldName string, p bindings.Projection) error {
	if err := b.Create(ctx, eng, p); err != nil {
		return err
	}
	return b.Drop(ctx, eng, oldName)
}

// Drop disables the registry row, then drops the catalog. Unstage and
// Remove are its two halves, for callers that disable the row in a
// transaction.
func (b *Binder) Drop(ctx context.Context, eng bindings.EngineRef, name string) error {
	if err := b.Unstage(ctx, eng, name); err != nil {
		return err
	}
	return b.Remove(ctx, name)
}

// Unstage disables the registry row.
func (b *Binder) Unstage(ctx context.Context, eng bindings.EngineRef, name string) error {
	if err := b.store.SoftDeleteTrinoCatalogRegistry(ctx, db.SoftDeleteTrinoCatalogRegistryParams{ClusterID: eng.ClusterID, CatalogName: name}); err != nil {
		return fmt.Errorf("disable registry row: %w", err)
	}
	return nil
}

// Remove sends DROP CATALOG.
func (b *Binder) Remove(ctx context.Context, name string) error {
	if err := b.admin.DropCatalog(ctx, name); err != nil {
		return fmt.Errorf("drop catalog: %w", err)
	}
	return nil
}

// Confirm: the plugin rewrites the row (updated_by = 'trino') once it has
// loaded exactly this version.
func (b *Binder) Confirm(ctx context.Context, eng bindings.EngineRef, p bindings.Projection) (bindings.Confirmation, error) {
	row, err := b.store.GetTrinoCatalogRegistryState(ctx, db.GetTrinoCatalogRegistryStateParams{ClusterID: eng.ClusterID, CatalogName: p.EngineCatalog})
	if errors.Is(err, pgx.ErrNoRows) {
		return bindings.Confirmation{Reason: "not_registered"}, nil
	}
	if err != nil {
		return bindings.Confirmation{}, err
	}
	switch {
	case !row.Enabled:
		return bindings.Confirmation{Reason: "disabled"}, nil
	case row.CatalogVersion == nil || *row.CatalogVersion != p.Version:
		return bindings.Confirmation{Reason: "version_mismatch"}, nil
	case row.UpdatedBy != "trino":
		return bindings.Confirmation{Reason: "plugin_not_confirmed"}, nil
	}
	return bindings.Confirmation{Confirmed: true}, nil
}

// Observe lists the catalogs Trino has loaded with their connectors.
func (b *Binder) Observe(ctx context.Context, _ bindings.EngineRef) ([]bindings.Observed, error) {
	res, err := b.admin.Execute(ctx, "SELECT catalog_name, connector_name FROM system.metadata.catalogs ORDER BY catalog_name", "", "")
	if err != nil {
		return nil, err
	}
	out := make([]bindings.Observed, 0, len(res.Data))
	for _, row := range res.Data {
		if len(row) < 2 {
			continue
		}
		name, _ := row[0].(string)
		connector, _ := row[1].(string)
		out = append(out, bindings.Observed{EngineCatalog: name, Present: true, Connector: connector})
	}
	return out, nil
}

// ListCatalogs is the names Trino has loaded (the drift report).
func (b *Binder) ListCatalogs(ctx context.Context) ([]string, error) {
	return b.admin.ListCatalogs(ctx)
}
