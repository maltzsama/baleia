package queryengine

import (
	"context"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/dialect"
)

// Class says whether an engine hosts catalogs from other connections
// (federated: Trino, later Doris/StarRocks) or runs SQL on its own data
// (direct: PostgreSQL, MySQL, …).
type Class string

const (
	ClassFederated Class = "federated"
	ClassDirect    Class = "direct"
)

// Identity says on whose behalf a session runs. It never contains secrets.
type Identity struct {
	UserID  string // Baleia user id ("u_<uuid>" for Trino impersonation, #92)
	AgentID string // set for agent principals (#59)
}

// SessionSpec is everything an engine needs to open a session.
type SessionSpec struct {
	Catalog  string   // binding.engine_catalog_name ("" = engine default)
	Schema   []string // namespace parts; the dialect renders them (#14)
	Identity Identity
	// ReadOnly comes from Target.ReadOnly (#13) or agent policy (#59). On an
	// engine that cannot enforce it, OpenSession fails with
	// read_only_unsupported unless the connection asserts read-only
	// credentials (docs/adr/0011).
	ReadOnly bool
	Timeout  time.Duration // per statement; 0 = engine default
	Tags     Tags
}

// Tags correlate a session with Baleia; each engine maps them to its own
// mechanism (Trino client tags, PostgreSQL application_name, …).
type Tags struct {
	ExecutionID string
	RequestID   string
	Source      string // "editor" | "explorer" | "sync" | "agent" | …
}

// QueryHandle identifies a running statement well enough to cancel it from
// another process. It is serializable and never contains credentials.
type QueryHandle struct {
	EngineID string            `json:"engine_id"`
	QueryID  string            `json:"query_id"`
	Extra    map[string]string `json:"extra,omitempty"` // e.g. {"backend_pid":"1234"}
}

// RecordSink receives the results of one statement. Every statement produces
// at least one result set: Begin once per result set (also with zero rows, and
// with an empty schema for statements that return no columns), Write for each
// batch (the sink must Release it), End to close the result set. A statement
// with several result sets repeats Begin/Write/End in order.
type RecordSink interface {
	Begin(schema *arrow.Schema) error
	Write(rec arrow.RecordBatch) error
	End(stats Stats) error
}

// Stats closes one result set.
type Stats struct {
	RowCount    int64
	UpdateCount *int64 // rows affected by DML, when the engine reports it
	ElapsedMs   int64
}

// Callbacks report on a running statement.
type Callbacks struct {
	// OnStart is called as soon as the handle is known, so it can be
	// persisted for cross-process cancel (#84).
	OnStart func(h QueryHandle)
	// OnProgress reports progress; callers use it for heartbeats.
	OnProgress func(p Progress)
}

// Progress of a running statement.
type Progress struct {
	State     string
	Rows      int64
	Bytes     int64
	ElapsedMs int64
}

// Engine is the only way the backend runs SQL. Binding catalogs, cluster
// monitoring and table maintenance are engine-specific extensions, never part
// of this interface.
type Engine interface {
	ID() string
	Kind() string
	Class() Class
	Capabilities() Capabilities
	Dialect() Dialect
	// OpenSession reserves what the engine needs to keep state across
	// statements: a dedicated connection, or client-side state.
	OpenSession(ctx context.Context, spec SessionSpec) (Session, error)
	// Cancel cancels a running statement by handle, possibly from another
	// process. A statement that is no longer running is not an error.
	Cancel(ctx context.Context, h QueryHandle) error
	Ping(ctx context.Context) error
	// Introspector returns nil when the engine cannot list its own metadata.
	Introspector() Introspector
	Close() error
}

// Session runs statements one at a time. USE/SET/transaction state persists
// across Execute calls on the same session.
type Session interface {
	// Execute runs ONE statement and streams its results into sink.
	Execute(ctx context.Context, sql string, cb Callbacks, sink RecordSink) error
	// Close releases the session; it rolls back an open transaction.
	Close() error
}

// Introspector lists metadata as the engine sees it. The queries belong to
// the dialect (#14).
type Introspector interface {
	ListCatalogs(ctx context.Context) ([]string, error)
	ListSchemas(ctx context.Context, catalog string) ([][]string, error)
	ListTables(ctx context.Context, catalog string, schema []string) ([]connections.TableRef, error)
	DescribeTable(ctx context.Context, t connections.TableRef) (*connections.TableMeta, error)
}

// ReadOnlySupport says whether an engine can enforce a read-only session.
type ReadOnlySupport string

const (
	ReadOnlyEnforced ReadOnlySupport = "enforced"
	ReadOnlyNone     ReadOnlySupport = "none"
)

// CancelScope says where a statement can be cancelled from.
type CancelScope string

const (
	// CancelAnyProcess: Cancel works with a persisted handle from any process.
	CancelAnyProcess CancelScope = "any_process"
	// CancelInProcess: only cancelling the Execute context works; Cancel
	// returns ErrCancelUnsupported.
	CancelInProcess CancelScope = "in_process"
)

// Capabilities declare what an engine can do; generic code never assumes any
// of them. They never authorize anything: what a user may do is
// internal/policy (TestGateNotCapabilityDriven).
type Capabilities struct {
	Dialect            string   // internal/dialect registry key
	IdentityModes      []string // "impersonation", "connection", "per_user"
	ReadOnly           ReadOnlySupport
	CancelScope        CancelScope
	SupportsCancel     bool
	Transactions       bool // BEGIN/COMMIT/ROLLBACK within a session
	MultipleResultSets bool
	MultipleCatalogs   bool // three-part names across catalogs in one statement
	// NamespaceDepth is the schema depth in SQL: 1 for most engines, 0 when
	// there are no schemas (SQLite).
	NamespaceDepth      int
	SessionSchema       bool // a default schema can be set per session
	SupportsExplain     bool
	SupportsTimeTravel  bool   // FOR VERSION/TIMESTAMP AS OF
	SupportsCatalogDDL  bool   // informational; never used by policy
	Monitoring          bool   // a cluster monitor extension exists (Trino)
	IcebergMaintenance  bool   // ALTER TABLE … EXECUTE optimize/expire_snapshots
	ArrowNative         bool   // results arrive as Arrow (Flight SQL)
	MaxIdentifierLength int    // 0 = unknown
	IdentifierCase      string // unquoted identifiers: "preserve", "lower" or "upper"
}

// Versioner is implemented by engines that report their server version.
type Versioner interface {
	Version(ctx context.Context) (string, error)
}

// Dialect is how an engine spells SQL (internal/dialect).
type Dialect = dialect.Dialect

// IcebergMaintainer is implemented by engines with
// Capabilities.IcebergMaintenance. It renders the statements of one
// maintenance run (optimize only for an empty retention); invalid input is
// an error before anything runs.
type IcebergMaintainer interface {
	MaintenanceStatements(table []string, retention string) ([]string, error)
}

// As finds an extension or concrete type behind the Manager's wrappers.
func As[T any](e Engine) (T, bool) {
	for e != nil {
		if v, ok := e.(T); ok {
			return v, true
		}
		u, ok := e.(interface{ Unwrap() Engine })
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	var zero T
	return zero, false
}
