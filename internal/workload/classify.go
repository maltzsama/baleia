package workload

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/galileostudio/baleia/internal/queryengine"
)

// Origin is where a run came from. Classification looks at it before any
// admin rule (#399, ADR 0014).
type Origin string

const (
	// OriginEditor is a Run from the SQL workbench.
	OriginEditor Origin = "editor"
	// OriginAgent is a run made by an agent principal.
	OriginAgent Origin = "agent"
	// OriginExplore is a compiled Explore query or an Explore preview.
	OriginExplore Origin = "explore"
	// OriginRetriever is a retriever refresh.
	OriginRetriever Origin = "retriever"
	// OriginMetadata is metadata introspection through an engine.
	OriginMetadata Origin = "metadata"
	// OriginMaintenance is Iceberg maintenance.
	OriginMaintenance Origin = "maintenance"
	// OriginExport is a user export of a result set as a run.
	OriginExport Origin = "export"
	// OriginDownload is a user download of a stored result as a run.
	OriginDownload Origin = "download"
)

// Valid reports whether o is one of the known origins.
func (o Origin) Valid() bool {
	switch o {
	case OriginEditor, OriginAgent, OriginExplore, OriginRetriever, OriginMetadata, OriginMaintenance,
		OriginExport, OriginDownload:
		return true
	default:
		return false
	}
}

// Decision is the class a run gets and the step that chose it. The reason is
// stored on the execution and shown in its details.
type Decision struct {
	Class  queryengine.WorkloadClass
	Reason string
	// Weight is the fairness weight of the run within its class. Rules may set
	// it; it is 1 otherwise (#546).
	Weight int
	// MaxClass is the most important class a later step (the user downgrade)
	// may assign. Empty means no rule capped it (#546).
	MaxClass queryengine.WorkloadClass
}

// Rule is one ordered, admin-managed workload rule (#539, #546): the in-memory
// form of a workload_rules row.
type Rule struct {
	ID       string
	Position int
	Enabled  bool
	Match    RuleMatch
	Class    queryengine.WorkloadClass
	MaxClass queryengine.WorkloadClass
	Weight   int
}

// RuleMatch is the closed set of match fields (#539). A zero field does not
// constrain the run; every non-zero field must match.
type RuleMatch struct {
	WorkspaceID   string
	TeamID        string
	Role          string
	UserID        string
	AgentID       string
	EngineID      string
	Origin        Origin
	SavedQueryID  string
	MinStatements int
	MaxStatements int
}

// RuleInput is the run being classified (#546).
type RuleInput struct {
	WorkspaceID  string
	TeamID       string
	Role         string
	UserID       string
	AgentID      string
	EngineID     string
	Origin       Origin
	SavedQueryID string
	Statements   int
	// DDL marks a data-definition statement: it is never demoted to background
	// by origin alone (#580).
	DDL bool
	// CostHeavy is set by the caller when the engine's cost estimate exceeds
	// its threshold (#550).
	CostHeavy bool
}

// Classify is the first classification step: background work gets the
// background class, everything a person waits for is interactive. Admin rules,
// cost routing and the user downgrade are later steps and come after this.
func Classify(origin Origin) Decision {
	switch origin {
	case OriginAgent, OriginRetriever, OriginMetadata, OriginMaintenance:
		return Decision{Class: queryengine.ClassBackground, Reason: "origin:" + string(origin), Weight: 1}
	case OriginExport, OriginDownload:
		return Decision{Class: queryengine.ClassHeavy, Reason: "origin:" + string(origin), Weight: 1}
	case OriginExplore:
		return Decision{Class: queryengine.ClassInteractive, Reason: "origin:explore", Weight: 1}
	default:
		return Decision{Class: queryengine.ClassInteractive, Reason: "default:editor", Weight: 1}
	}
}

// Decide classifies a run: origin first, then the ordered, enabled rules
// (first match wins), then the default (#399, #546). Rules can override the
// origin's class, which is how an admin moves a retriever's DDL or a heavy
// workspace (#580 lands the DDL/export refinements). Cost routing and the
// user downgrade are later steps.
func Decide(origin Origin, in RuleInput, rules []Rule) Decision {
	base := Classify(origin)
	in.Origin = origin
	// DDL must run promptly: it is never kept in background by origin alone.
	// A rule may still set its class (#580).
	if in.DDL && base.Class == queryengine.ClassBackground {
		base = Decision{Class: queryengine.ClassInteractive, Reason: "statement:ddl", Weight: 1}
	}
	for _, r := range rules {
		if !r.Enabled || !r.matches(in) {
			continue
		}
		class := r.Class
		if !class.Valid() {
			class = base.Class
		}
		if r.MaxClass != "" {
			class = ClampToMax(class, r.MaxClass)
		}
		weight := r.Weight
		if weight < 1 {
			weight = 1
		}
		return Decision{Class: class, Reason: "rule:" + r.ID, Weight: weight, MaxClass: r.MaxClass}
	}
	// Cost routing comes after the rules and before the default (#550).
	if in.CostHeavy {
		return Decision{Class: queryengine.ClassHeavy, Reason: "cost:trino", Weight: 1}
	}
	return base
}

// matches reports whether in satisfies every field the rule constrains.
func (r Rule) matches(in RuleInput) bool {
	m := r.Match
	if m.WorkspaceID != "" && m.WorkspaceID != in.WorkspaceID {
		return false
	}
	if m.TeamID != "" && m.TeamID != in.TeamID {
		return false
	}
	if m.Role != "" && m.Role != in.Role {
		return false
	}
	if m.UserID != "" && m.UserID != in.UserID {
		return false
	}
	if m.AgentID != "" && m.AgentID != in.AgentID {
		return false
	}
	if m.EngineID != "" && m.EngineID != in.EngineID {
		return false
	}
	if m.Origin != "" && m.Origin != in.Origin {
		return false
	}
	if m.SavedQueryID != "" && m.SavedQueryID != in.SavedQueryID {
		return false
	}
	if m.MinStatements > 0 && in.Statements < m.MinStatements {
		return false
	}
	if m.MaxStatements > 0 && in.Statements > m.MaxStatements {
		return false
	}
	return true
}

// ClassRank orders the classes by priority: interactive is the most important
// (0) and background the least (2). An unknown class ranks last.
func ClassRank(class queryengine.WorkloadClass) int {
	switch class {
	case queryengine.ClassInteractive:
		return 0
	case queryengine.ClassHeavy:
		return 1
	case queryengine.ClassBackground:
		return 2
	default:
		return 3
	}
}

// ClampToMax caps a class so it is never more important than ceiling: it
// returns ceiling when class outranks it, and class otherwise (#546).
func ClampToMax(class, ceiling queryengine.WorkloadClass) queryengine.WorkloadClass {
	if class == "" {
		return ceiling
	}
	if ceiling == "" {
		return class
	}
	if ClassRank(class) < ClassRank(ceiling) {
		return ceiling
	}
	return class
}

// matchJSON is the closed wire shape stored in workload_rules.match. The keys
// match the database CHECK in migration 00065.
type matchJSON struct {
	WorkspaceID   string `json:"workspace_id,omitempty"`
	TeamID        string `json:"team_id,omitempty"`
	Role          string `json:"role,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	EngineID      string `json:"engine_id,omitempty"`
	Origin        Origin `json:"origin,omitempty"`
	SavedQueryID  string `json:"saved_query_id,omitempty"`
	MinStatements int    `json:"min_statements,omitempty"`
	MaxStatements int    `json:"max_statements,omitempty"`
}

// ParseRuleMatch decodes a workload_rules.match column. An empty object is a
// catch-all rule.
func ParseRuleMatch(raw []byte) (RuleMatch, error) {
	if len(raw) == 0 {
		return RuleMatch{}, nil
	}
	var m matchJSON
	if err := json.Unmarshal(raw, &m); err != nil {
		return RuleMatch{}, fmt.Errorf("workload: invalid rule match: %w", err)
	}
	if m.Origin != "" && !m.Origin.Valid() {
		return RuleMatch{}, fmt.Errorf("workload: unknown origin %q in rule match", m.Origin)
	}
	return RuleMatch(m), nil
}

// EncodeRuleMatch encodes a match for storage. Keys with a zero value are
// omitted so the database CHECK accepts the object.
func EncodeRuleMatch(m RuleMatch) ([]byte, error) {
	if m.Origin != "" && !m.Origin.Valid() {
		return nil, fmt.Errorf("workload: unknown origin %q in rule match", m.Origin)
	}
	return json.Marshal(matchJSON(m))
}

// IsDDL reports whether sql is a data-definition statement. The caller passes
// one statement, already split by the engine dialect (#580).
func IsDDL(sql string) bool {
	switch leadingKeyword(sql) {
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME", "COMMENT", "GRANT", "REVOKE":
		return true
	default:
		return false
	}
}

// leadingKeyword is the first word of sql, upper-cased, ignoring leading line
// and block comments.
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
	return strings.ToUpper(s)
}

// IsSelect reports whether sql is a statement a cost estimate can be had for:
// a SELECT or a CTE that starts with WITH. Other statements never get an
// EXPLAIN (#550).
func IsSelect(sql string) bool {
	switch leadingKeyword(sql) {
	case "SELECT", "WITH", "VALUES", "TABLE":
		return true
	default:
		return false
	}
}
