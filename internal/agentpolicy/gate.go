package agentpolicy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/policy"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/time/rate"
)

// ReasonCode is the bounded refusal reason shared by the gate, the MCP server
// and the audit trail (#61).
type ReasonCode string

const (
	ReasonPolicy    ReasonCode = "policy"    // tool, catalog or engine not allowed
	ReasonScope     ReasonCode = "scope"     // workspace/catalog/engine not accessible
	ReasonRate      ReasonCode = "rate"      // per-minute burst exceeded
	ReasonQuota     ReasonCode = "quota"     // concurrency or hourly quota exceeded
	ReasonBudget    ReasonCode = "budget"    // assistant turn budget (#62)
	ReasonStatement ReasonCode = "statement" // statement kind not allowed
)

// Call is one tool invocation the gate decides on.
type Call struct {
	Tool        string
	WorkspaceID pgtype.UUID
	CatalogID   *pgtype.UUID
	EngineID    *pgtype.UUID
	ExecutionID *pgtype.UUID
	SQL         *string
}

// Decision is the gate's answer. Effective is only meaningful when Allowed.
type Decision struct {
	Allowed    bool
	Reason     ReasonCode
	Detail     string
	RetryAfter time.Duration
	Effective  Effective
}

func allow(eff Effective) Decision { return Decision{Allowed: true, Effective: eff} }

func refuse(reason ReasonCode, detail string) Decision {
	return Decision{Reason: reason, Detail: detail}
}

// Store is the slice of the database the gate reads.
type Store interface {
	GetAgentByID(ctx context.Context, id pgtype.UUID) (db.Agent, error)
	AgentWorkspaceBound(ctx context.Context, arg db.AgentWorkspaceBoundParams) (bool, error)
	CheckWorkspaceAccess(ctx context.Context, arg db.CheckWorkspaceAccessParams) (bool, error)
	CatalogGrantedToAgent(ctx context.Context, arg db.CatalogGrantedToAgentParams) (bool, error)
	GetEngineByID(ctx context.Context, id pgtype.UUID) (db.Engine, error)
	GetCatalogConnectionByID(ctx context.Context, id pgtype.UUID) (db.CatalogConnection, error)
	CountRunningExecutionsByAgent(ctx context.Context, agentID pgtype.UUID) (int64, error)
	CountExecutionsByAgentSince(ctx context.Context, arg db.CountExecutionsByAgentSinceParams) (int64, error)
	OldestExecutionByAgentSince(ctx context.Context, arg db.OldestExecutionByAgentSinceParams) (pgtype.Timestamptz, error)
}

// EngineCapabilities reports whether an engine can enforce a read-only
// session. The API wires it to the query-engine manager.
type EngineCapabilities interface {
	ReadOnly(ctx context.Context, engineID pgtype.UUID) (queryengine.ReadOnlySupport, error)
}

// Observer records every decision (metrics, #87). It must not block.
type Observer interface {
	Decision(tool string, allowed bool, reason string)
}

// Gate is the single enforcement point for agent tool calls.
type Gate struct {
	store    Store
	engines  EngineCapabilities
	ceilings Ceilings
	limiter  *BurstLimiter
	observer Observer
	now      func() time.Time
}

// NewGate builds a gate. ceilings zero values fall back to DefaultCeilings.
func NewGate(store Store, engines EngineCapabilities, ceilings Ceilings) *Gate {
	if ceilings == (Ceilings{}) {
		ceilings = DefaultCeilings()
	}
	return &Gate{
		store:    store,
		engines:  engines,
		ceilings: ceilings,
		limiter:  NewBurstLimiter(),
		now:      time.Now,
	}
}

// SetClock is for tests.
func (g *Gate) SetClock(now func() time.Time) { g.now = now }

// SetLimiter is for tests.
func (g *Gate) SetLimiter(l *BurstLimiter) { g.limiter = l }

// SetObserver records gate decisions (metrics, #87).
func (g *Gate) SetObserver(o Observer) { g.observer = o }

// Check decides whether p may run c. The order is the one from #60: identity,
// tool, workspace, catalog, burst, then the run_query rules.
func (g *Gate) Check(ctx context.Context, p authz.Principal, c Call) (dec Decision, err error) {
	defer func() {
		if g.observer == nil {
			return
		}
		reason := string(dec.Reason)
		if !dec.Allowed && reason == "" {
			reason = "internal"
		}
		g.observer.Decision(c.Tool, dec.Allowed, reason)
	}()
	if !p.IsAgent() || !p.AgentID.Valid {
		return refuse(ReasonScope, "not an agent principal"), nil
	}
	agent, err := g.store.GetAgentByID(ctx, p.AgentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return refuse(ReasonScope, "agent not found"), nil
		}
		return Decision{}, err
	}
	if agent.OwnerID.Bytes != p.UserID.Bytes {
		return refuse(ReasonScope, "agent does not belong to this principal"), nil
	}
	if agent.Status != "active" {
		return refuse(ReasonPolicy, "agent is disabled"), nil
	}
	stored, err := Parse(agent.Policy)
	if err != nil {
		return Decision{}, err
	}
	eff := stored.Effective(g.ceilings)

	if !eff.Tools[c.Tool] {
		return refuse(ReasonPolicy, fmt.Sprintf("tool %q is not allowed", c.Tool)), nil
	}
	// list_workspaces has no workspace argument; every other tool must name one.
	if c.Tool != ToolListWorkspaces {
		if !c.WorkspaceID.Valid {
			return refuse(ReasonScope, "workspace is required"), nil
		}
		bound, err := g.store.AgentWorkspaceBound(ctx, db.AgentWorkspaceBoundParams{AgentID: p.AgentID, WorkspaceID: c.WorkspaceID})
		if err != nil {
			return Decision{}, err
		}
		if !bound {
			return refuse(ReasonScope, "workspace is not bound to this agent"), nil
		}
		member, err := g.store.CheckWorkspaceAccess(ctx, db.CheckWorkspaceAccessParams{ID: c.WorkspaceID, UserID: p.UserID})
		if err != nil {
			return Decision{}, err
		}
		if !member {
			return refuse(ReasonScope, "workspace is no longer accessible"), nil
		}
	}
	if c.CatalogID != nil && c.CatalogID.Valid {
		granted, err := g.store.CatalogGrantedToAgent(ctx, db.CatalogGrantedToAgentParams{
			CatalogID: *c.CatalogID,
			AgentID:   p.AgentID,
			UserID:    p.UserID,
		})
		if err != nil {
			return Decision{}, err
		}
		if !granted {
			return refuse(ReasonScope, "catalog is not granted to a bound workspace"), nil
		}
		if len(eff.Catalogs) > 0 && !eff.Catalogs[uuidString(*c.CatalogID)] {
			return refuse(ReasonPolicy, "catalog is not allowed by the policy"), nil
		}
	}
	if !g.limiter.Allow(p.AgentID, eff.MaxToolCallsPerMinute, g.now()) {
		return refuse(ReasonRate, "tool-call rate exceeded"), nil
	}
	if c.Tool == ToolRunQuery {
		return g.checkRunQuery(ctx, p, c, eff)
	}
	return allow(eff), nil
}

func (g *Gate) checkRunQuery(ctx context.Context, p authz.Principal, c Call, eff Effective) (Decision, error) {
	if c.SQL == nil || strings.TrimSpace(*c.SQL) == "" {
		return refuse(ReasonStatement, "run_query requires SQL"), nil
	}
	if c.EngineID == nil || !c.EngineID.Valid {
		return refuse(ReasonScope, "engine is required"), nil
	}
	engine, err := g.store.GetEngineByID(ctx, *c.EngineID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return refuse(ReasonScope, "engine not found"), nil
		}
		return Decision{}, err
	}
	d, ok := dialect.ForEngineKind(engine.Kind)
	if !ok {
		return refuse(ReasonPolicy, "engine dialect is not supported"), nil
	}
	stmts, err := d.SplitStatements(*c.SQL)
	if err != nil {
		return refuse(ReasonStatement, "the statement could not be parsed"), nil
	}
	if len(stmts) != 1 {
		return refuse(ReasonStatement, "agents may run exactly one statement per call"), nil
	}
	if policy.ForbiddenInEditor(d, *c.SQL) {
		return refuse(ReasonStatement, "catalog management and session authorization are not allowed"), nil
	}
	if !readOnlyKeyword(leadingKeyword(*c.SQL)) {
		return refuse(ReasonStatement, "agents may only run read statements"), nil
	}
	if len(eff.Engines) > 0 && !eff.Engines[uuidString(engine.ID)] {
		return refuse(ReasonPolicy, "engine is not allowed by the policy"), nil
	}
	cap, err := g.engines.ReadOnly(ctx, engine.ID)
	if err != nil {
		return Decision{}, err
	}
	conn, err := g.store.GetCatalogConnectionByID(ctx, engine.ConnectionID)
	if err != nil {
		return Decision{}, err
	}
	if cap != queryengine.ReadOnlyEnforced && !conn.CredentialsReadOnly {
		return refuse(ReasonScope, "read-only is not enforced on this engine"), nil
	}

	running, err := g.store.CountRunningExecutionsByAgent(ctx, p.AgentID)
	if err != nil {
		return Decision{}, err
	}
	if running >= int64(eff.MaxConcurrentQueries) {
		return refuse(ReasonQuota, "concurrent query quota reached"), nil
	}

	since := g.now().Add(-time.Hour)
	hourly, err := g.store.CountExecutionsByAgentSince(ctx, db.CountExecutionsByAgentSinceParams{
		AgentID:   p.AgentID,
		StartedAt: pgtype.Timestamptz{Time: since, Valid: true},
	})
	if err != nil {
		return Decision{}, err
	}
	if hourly >= int64(eff.MaxQueriesPerHour) {
		retry := time.Hour
		oldest, err := g.store.OldestExecutionByAgentSince(ctx, db.OldestExecutionByAgentSinceParams{
			AgentID:   p.AgentID,
			StartedAt: pgtype.Timestamptz{Time: since, Valid: true},
		})
		if err == nil && oldest.Valid {
			if d := oldest.Time.Add(time.Hour).Sub(g.now()); d > 0 {
				retry = d
			} else {
				retry = 0
			}
		}
		dec := refuse(ReasonQuota, "hourly query quota reached")
		dec.RetryAfter = retry
		return dec, nil
	}
	return allow(eff), nil
}

// BurstLimiter is the per-agent, in-process token bucket (#60). Quotas that
// must hold across replicas are computed from query_executions instead.
type BurstLimiter struct {
	mu       sync.Mutex
	limiters map[[16]byte]*rate.Limiter
	seen     map[[16]byte]time.Time
}

// NewBurstLimiter returns an empty limiter.
func NewBurstLimiter() *BurstLimiter {
	return &BurstLimiter{limiters: map[[16]byte]*rate.Limiter{}, seen: map[[16]byte]time.Time{}}
}

// Allow consumes one token for agentID at now. It evicts agents idle for more
// than ten minutes so the map does not grow forever.
func (b *BurstLimiter) Allow(agentID pgtype.UUID, perMinute int, now time.Time) bool {
	if perMinute <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, at := range b.seen {
		if now.Sub(at) > 10*time.Minute {
			delete(b.seen, id)
			delete(b.limiters, id)
		}
	}
	id := agentID.Bytes
	l, ok := b.limiters[id]
	if !ok {
		l = rate.NewLimiter(rate.Limit(float64(perMinute)/60.0), perMinute)
		b.limiters[id] = l
	}
	b.seen[id] = now
	return l.AllowN(now, 1)
}

// readOnlyKeyword reports whether the leading keyword is one agents may use.
func readOnlyKeyword(kw string) bool {
	switch strings.ToUpper(kw) {
	case "SELECT", "WITH", "VALUES", "TABLE", "SHOW", "DESCRIBE", "EXPLAIN":
		return true
	}
	return false
}

// leadingKeyword returns the first word after whitespace and comments. It is a
// friendly fast refusal; the engine's read-only enforcement is the control.
func leadingKeyword(sql string) string {
	s := strings.TrimSpace(sql)
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = strings.TrimSpace(s[i+1:])
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = strings.TrimSpace(s[i+2:])
				continue
			}
			return ""
		}
		break
	}
	if i := strings.IndexAny(s, " \t\r\n("); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return s
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
