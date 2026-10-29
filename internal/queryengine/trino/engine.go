// Package trino is the Trino adapter of the engine contract (#25). Trino has
// no connections: a session is client-side state (catalog, schema, session
// properties, prepared statements) replayed on every request and updated
// from the X-Trino-Set-* response headers.
package trino

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	trinoprovider "github.com/galileostudio/baleia/internal/connections/trino"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/queryengine"
	trinoclient "github.com/galileostudio/baleia/internal/trino"
)

// Kind is the registered engine kind.
const Kind = "trino"

// Default identities when the engine's connection config names none.
const (
	defaultServiceUser = "baleia"
	defaultAdminUser   = "baleia_admin"
)

// requestTimeout bounds one HTTP request (one page), not a statement.
const requestTimeout = 60 * time.Second

// Spec is the kind's entry for a queryengine.Registry (#364). Its engines
// log through logger; nil discards.
func Spec(dl queryengine.Dialect, logger *slog.Logger) queryengine.KindSpec {
	return queryengine.KindSpec{Name: Kind, Class: queryengine.ClassFederated, Factory: newEngine(dl, logger)}
}

// newEngine builds the engine from its connection config: url, and the
// service_user (fallback identity) and admin_user (kills queries by handle)
// that bootstrap keeps in step with the environment.
func newEngine(dl queryengine.Dialect, logger *slog.Logger) queryengine.Factory {
	return func(_ context.Context, row queryengine.EngineRow, cfg connections.Config, d *connections.Dialer) (queryengine.Engine, error) {
		url := cfg.Value("url")
		if url == "" {
			return nil, fmt.Errorf("trino engine %s: connection has no url", row.Name)
		}
		service := valueOr(cfg.Value("service_user"), defaultServiceUser)
		admin := valueOr(cfg.Value("admin_user"), defaultAdminUser)
		hc := obs.WrapHTTPClient(trinoprovider.HTTPClient(d, cfg, requestTimeout))
		return &Engine{
			logger:  cmp.Or(logger, slog.New(slog.DiscardHandler)),
			dialect: dl,
			id:      row.ID,
			client:  trinoclient.NewWithHTTPClient(url, service, hc),
			admin:   trinoclient.NewWithHTTPClient(url, admin, hc),
		}, nil
	}
}

// New builds an engine for a coordinator URL that speaks dl; tests and tools
// use it.
func New(id, url, serviceUser, adminUser string, d *connections.Dialer, dl queryengine.Dialect) *Engine {
	hc := obs.WrapHTTPClient(d.HTTPClient(requestTimeout))
	return &Engine{
		logger:  slog.New(slog.DiscardHandler),
		dialect: dl,
		id:      id,
		client:  trinoclient.NewWithHTTPClient(url, valueOr(serviceUser, defaultServiceUser), hc),
		admin:   trinoclient.NewWithHTTPClient(url, valueOr(adminUser, defaultAdminUser), hc),
	}
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Engine is a Trino coordinator.
type Engine struct {
	logger  *slog.Logger
	dialect queryengine.Dialect
	id      string
	client  *trinoclient.Client // runs statements; identity set per session
	admin   *trinoclient.Client // kills queries by handle; never runs user SQL
}

func (e *Engine) ID() string                   { return e.id }
func (e *Engine) Kind() string                 { return Kind }
func (e *Engine) Class() queryengine.Class     { return queryengine.ClassFederated }
func (e *Engine) Dialect() queryengine.Dialect { return e.dialect }
func (e *Engine) Close() error                 { return nil }

// Capabilities of Trino in v1.
func (e *Engine) Capabilities() queryengine.Capabilities {
	return queryengine.Capabilities{
		Dialect:            dialect.Trino,
		IdentityModes:      []string{"impersonation"},
		ReadOnly:           queryengine.ReadOnlyEnforced, // the catalog guard's read-only mode (#92)
		CancelScope:        queryengine.CancelAnyProcess,
		SupportsCancel:     true,
		Transactions:       false,
		MultipleResultSets: false,
		MultipleCatalogs:   true,
		NamespaceDepth:     1,
		SessionSchema:      true,
		SupportsExplain:    true,
		SupportsTimeTravel: true, // Iceberg tables
		SupportsCatalogDDL: true,
		Monitoring:         true,
		IcebergMaintenance: true,
		IdentifierCase:     "lower",
	}
}

// Version is the coordinator's node version.
func (e *Engine) Version(ctx context.Context) (string, error) {
	info, err := e.client.Info(ctx)
	if err != nil {
		return "", err
	}
	return info.NodeVersion.Version, nil
}

// Ping fails while the coordinator is down or still starting.
func (e *Engine) Ping(ctx context.Context) error {
	info, err := e.client.Info(ctx)
	if err != nil {
		return mapError(err)
	}
	if info.Starting {
		return queryengine.NewError(queryengine.CodeUnavailable, Kind, "SERVER_STARTING_UP", "trino is starting")
	}
	return nil
}

// Cancel kills a query by id with the admin identity, from any process. A
// query that already finished is not an error.
func (e *Engine) Cancel(ctx context.Context, h queryengine.QueryHandle) error {
	if h.QueryID == "" {
		return nil
	}
	err := e.admin.KillQuery(ctx, h.QueryID, "cancelled by baleia")
	var qe *trinoclient.QueryError
	if errors.As(err, &qe) && queryGone(qe) {
		return nil // already finished: not an error
	}
	if err != nil {
		return mapError(err)
	}
	return nil
}

func (e *Engine) OpenSession(_ context.Context, spec queryengine.SessionSpec) (queryengine.Session, error) {
	return newSession(e, spec), nil
}

func (e *Engine) Introspector() queryengine.Introspector { return introspector{e: e} }

// queryGone reports kill_query's answer for a query that is no longer
// running. Trino 483 reports it as NOT_SUPPORTED "Target query is not
// running: <id>"; NOT_FOUND covers an unknown id.
func queryGone(qe *trinoclient.QueryError) bool {
	return qe.ErrorName == "NOT_FOUND" ||
		(qe.ErrorName == "NOT_SUPPORTED" && strings.Contains(qe.Message, "is not running"))
}

// MaintenanceStatements renders an Iceberg maintenance run: optimize, then
// expire snapshots and remove orphan files older than retention (a Trino
// duration such as "7d"). An empty retention renders optimize only.
func (e *Engine) MaintenanceStatements(table []string, retention string) ([]string, error) {
	d := e.Dialect()
	name, err := d.QualifiedName(table...)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	optimize := fmt.Sprintf("ALTER TABLE %s EXECUTE optimize", name)
	if retention == "" {
		return []string{optimize}, nil
	}
	if err := trinoclient.ValidateRetention(retention); err != nil {
		return nil, fmt.Errorf("invalid retention: %w", err)
	}
	r := d.QuoteString(retention)
	return []string{
		optimize,
		fmt.Sprintf("ALTER TABLE %s EXECUTE expire_snapshots(retention_threshold => %s)", name, r),
		fmt.Sprintf("ALTER TABLE %s EXECUTE remove_orphan_files(retention_threshold => %s)", name, r),
	}, nil
}

// IcebergStatement renders one maintenance procedure with its arguments
// (#500). The partition filter is checked again here: a workflow input is
// not a trust boundary, and the statement runs as the engine's identity.
func (e *Engine) IcebergStatement(table []string, op queryengine.IcebergOperation) (string, error) {
	d := e.Dialect()
	name, err := d.QualifiedName(table...)
	if err != nil {
		return "", fmt.Errorf("invalid table name: %w", err)
	}
	var args []string
	switch op.Name {
	case "optimize":
		if op.FileSizeThreshold > 0 {
			args = append(args, "file_size_threshold => "+d.QuoteString(fmt.Sprintf("%dB", op.FileSizeThreshold)))
		}
		if len(op.SortedBy) > 0 {
			cols := make([]string, 0, len(op.SortedBy))
			for _, c := range op.SortedBy {
				cols = append(cols, d.QuoteString(c))
			}
			args = append(args, "sorted_by => ARRAY["+strings.Join(cols, ", ")+"]")
		}
		stmt := fmt.Sprintf("ALTER TABLE %s EXECUTE optimize%s", name, procArgs(args))
		var where []string
		if op.PartitionFilter != "" {
			if err := trinoclient.ValidatePartitionFilter(op.PartitionFilter); err != nil {
				return "", fmt.Errorf("invalid partition filter: %w", err)
			}
			where = append(where, "("+op.PartitionFilter+")")
		}
		if op.SkipFilesNewerThan > 0 {
			where = append(where, fmt.Sprintf(`"$file_modified_time" < current_timestamp - INTERVAL '%d' SECOND`,
				int64(op.SkipFilesNewerThan/time.Second)))
		}
		if len(where) > 0 {
			stmt += " WHERE " + strings.Join(where, " AND ")
		}
		return stmt, nil
	case "optimize_manifests":
		return fmt.Sprintf("ALTER TABLE %s EXECUTE optimize_manifests", name), nil
	case "expire_snapshots", "remove_orphan_files":
		if op.Retention <= 0 {
			return "", fmt.Errorf("%s needs a retention", op.Name)
		}
		args = append(args, "retention_threshold => "+d.QuoteString(trinoDuration(op.Retention)))
		if op.Name == "expire_snapshots" {
			if op.RetainLast > 0 {
				args = append(args, fmt.Sprintf("retain_last => %d", op.RetainLast))
			}
			if op.CleanExpiredMetadata != nil {
				args = append(args, fmt.Sprintf("clean_expired_metadata => %t", *op.CleanExpiredMetadata))
			}
		}
		return fmt.Sprintf("ALTER TABLE %s EXECUTE %s%s", name, op.Name, procArgs(args)), nil
	}
	return "", fmt.Errorf("unknown maintenance operation %q", op.Name)
}

// IcebergMetricsStatements count the table's data files below the target
// size and its manifests, through the $files and $manifests tables.
func (e *Engine) IcebergMetricsStatements(table []string, targetFileSize int64) (smallFiles, manifests string, err error) {
	if len(table) == 0 {
		return "", "", errors.New("invalid table name: empty")
	}
	d := e.Dialect()
	last := len(table) - 1
	meta := func(suffix string) (string, error) {
		parts := append(append([]string{}, table[:last]...), table[last]+suffix)
		return d.QualifiedName(parts...)
	}
	files, err := meta("$files")
	if err != nil {
		return "", "", fmt.Errorf("invalid table name: %w", err)
	}
	mans, err := meta("$manifests")
	if err != nil {
		return "", "", fmt.Errorf("invalid table name: %w", err)
	}
	// content 0 is a data file; delete files are counted from the snapshot.
	smallFiles = fmt.Sprintf("SELECT count(*) FROM %s WHERE content = 0 AND file_size_in_bytes < %d", files, targetFileSize)
	manifests = fmt.Sprintf("SELECT count(*) FROM %s", mans)
	return smallFiles, manifests, nil
}

func procArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return "(" + strings.Join(args, ", ") + ")"
}

// trinoDuration spells d in the largest whole unit Trino's retention
// accepts (days, hours or minutes), rounding a remainder up to a minute.
func trinoDuration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return fmt.Sprintf("%dm", (d+time.Minute-1)/time.Minute)
}

var _ queryengine.QueryResourcesReader = (*Engine)(nil)

// QueryResources reads the query's statistics from the coordinator as the
// admin identity, which sees every user's queries (#407).
func (e *Engine) QueryResources(ctx context.Context, queryID string) (queryengine.QueryResources, error) {
	qi, err := e.admin.GetQuery(ctx, queryID)
	if errors.Is(err, trinoclient.ErrQueryGone) {
		return queryengine.QueryResources{}, queryengine.ErrQueryUnknown
	}
	if err != nil {
		return queryengine.QueryResources{}, err
	}
	r := qi.Resources()
	return queryengine.QueryResources{
		PeakMemoryBytes: r.PeakMemoryBytes,
		ProcessedRows:   r.ProcessedRows,
		ProcessedBytes:  r.ProcessedBytes,
		OutputRows:      r.OutputRows,
		OutputBytes:     r.OutputBytes,
		QueuedMs:        r.QueuedMs,
		PlanningMs:      r.PlanningMs,
	}, nil
}
