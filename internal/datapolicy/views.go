package datapolicy

import (
	"maps"
	"slices"
	"strings"

	"github.com/galileostudio/baleia/internal/connections"
)

// TableName names a table or view on the engines: its engine catalog name,
// schema and name, lowercase as Trino resolves them.
type TableName struct {
	Catalog string
	Schema  string
	Table   string
}

func (t TableName) String() string { return t.Catalog + "." + t.Schema + "." + t.Table }

// NewTableName names a table, lowercase as Trino resolves names.
func NewTableName(catalog, schema, table string) TableName {
	return TableName{Catalog: strings.ToLower(catalog), Schema: strings.ToLower(schema), Table: strings.ToLower(table)}
}

// CatalogView is a view found in a catalog manager, with the engine catalog
// names of the catalog that holds it and the tables and views its SQL reads
// (package viewsql resolves them). Unreadable means its SQL could not be
// parsed.
type CatalogView struct {
	Catalogs   []string
	View       connections.View
	Reads      []TableName
	Unreadable bool
}

// ViewReach is a SECURITY DEFINER view and the policied tables it reads,
// directly or through other views. Unreadable means its SQL, or the SQL of a
// view it reads, could not be parsed, so it is taken to read every policied
// table.
type ViewReach struct {
	View       TableName
	Owner      string
	Reads      []TableName
	Unreadable bool
}

// PoliciedTables lists the tables a content's catalogs put policies on, by
// every engine name of their catalog.
func (c Content) PoliciedTables() map[TableName]bool {
	out := map[TableName]bool{}
	for _, catalog := range c.Catalogs {
		for schema, tables := range catalog.Tables {
			for table := range tables {
				for _, name := range catalog.Names {
					out[NewTableName(name, schema, table)] = true
				}
			}
		}
	}
	return out
}

// DefinerReach returns every SECURITY DEFINER view that reads a policied
// table, with the tables it reaches. Trino evaluates a DEFINER view as its
// owner (ADR 0028), and a view it reads runs as that owner too, so the reach
// follows views of either kind.
func DefinerReach(views []CatalogView, policied map[TableName]bool) []ViewReach {
	type node struct {
		view       connections.View
		reads      []TableName
		unreadable bool
	}
	nodes := map[TableName]*node{}
	var order []TableName
	for _, cv := range views {
		schema := strings.Join(cv.View.Namespace, ".")
		n := &node{view: cv.View, reads: cv.Reads, unreadable: cv.Unreadable}
		for _, catalog := range cv.Catalogs {
			key := NewTableName(catalog, schema, cv.View.Name)
			nodes[key] = n
			order = append(order, key)
		}
	}

	// Each DEFINER view gets its own walk: a memo shared across views would
	// keep a partial answer for a view met inside a cycle.
	reach := func(start *node) (map[TableName]bool, bool) {
		tables := map[TableName]bool{}
		unreadable := false
		visited := map[*node]bool{}
		stack := []*node{start}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if visited[n] {
				continue
			}
			visited[n] = true
			unreadable = unreadable || n.unreadable
			for _, t := range n.reads {
				if policied[t] {
					tables[t] = true
				}
				if inner, ok := nodes[t]; ok {
					stack = append(stack, inner)
				}
			}
		}
		return tables, unreadable
	}

	var out []ViewReach
	seen := map[*node]bool{}
	for _, key := range order {
		n := nodes[key]
		if n.view.Owner == "" || seen[n] {
			continue
		}
		seen[n] = true
		tables, unreadable := reach(n)
		if unreadable {
			tables = maps.Clone(policied)
		}
		if len(tables) == 0 {
			continue
		}
		reads := slices.SortedFunc(maps.Keys(tables), func(a, b TableName) int { return strings.Compare(a.String(), b.String()) })
		out = append(out, ViewReach{View: key, Owner: n.view.Owner, Reads: reads, Unreadable: unreadable})
	}
	return out
}

// TrustedDefinerViews keeps the views of reach whose owner is a trusted
// identity. Trusted identities are never filtered or masked, so such a view
// would serve a policied table unfiltered: publishing refuses while one
// exists (ADR 0028).
func TrustedDefinerViews(reach []ViewReach, trusted map[string]bool) []ViewReach {
	var out []ViewReach
	for _, r := range reach {
		if trusted[r.Owner] {
			out = append(out, r)
		}
	}
	return out
}
