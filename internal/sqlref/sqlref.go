// Package sqlref extracts the tables, columns and equi-joins a statement reads
// and writes from the sqlglot AST (#538, #625). Extraction runs the fork's
// qualify pass so a reference is resolved to its physical source: a CTE,
// subquery or derived table is followed to the table it reads, and an
// unqualified table is stamped with the target's default schema.
//
// It is best-effort: no engine round-trip is needed, and a parse or qualify
// failure yields an empty Reference, never an error the caller must treat as
// fatal.
package sqlref

import (
	"slices"
	"strings"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/maltzsama/sqlglot-go/expressions"
	"github.com/maltzsama/sqlglot-go/optimizer"
)

// Table is one physical table named in FROM/JOIN/UPDATE/INTO.
type Table struct {
	// Path is the resolved name, 1 to 3 parts (table, schema.table,
	// catalog.schema.table). The default schema is stamped on unqualified
	// names when ExtractResolved is given one.
	Path  []string
	Alias string
	// Kind is "read" for FROM/JOIN and "write" for UPDATE/INTO.
	Kind string
}

// Column is one column reference, resolved to its physical table. Namespace
// and Table are both empty when the reference is unqualified and ambiguous or
// unresolved.
type Column struct {
	Namespace string
	Table     string
	Name      string
}

// Join is an equi-join predicate between two aliases: left = right.
type Join struct {
	LeftAlias   string
	LeftColumn  string
	RightAlias  string
	RightColumn string
}

// Reference is what one statement reads and writes.
type Reference struct {
	Tables  []Table
	Columns []Column
	Joins   []Join
}

var genericDialect = dialect.NewRegistry().MustGet(dialect.FlightSQL)

// Extract returns references using Baleia's generic SQL dialect. Callers that
// know the connection dialect and its default schema should use
// ExtractResolved.
func Extract(sql string) Reference {
	return ExtractResolved(genericDialect, "", sql)
}

// ExtractWithDialect returns references parsed with the connection dialect but
// without a default schema; unqualified names stay unqualified.
func ExtractWithDialect(d dialect.Dialect, sql string) Reference {
	return ExtractResolved(d, "", sql)
}

// ExtractResolved returns the tables, columns and equi-joins of one statement,
// parsed with d and resolved against defaultSchema (the target's default
// schema, stamped on unqualified table references). An unparsable statement
// yields an empty Reference.
func ExtractResolved(d dialect.Dialect, defaultSchema, sql string) Reference {
	qualified, report := parseAndQualify(d, defaultSchema, sql, true)
	if qualified == nil {
		// Column qualification can reject a statement the table pass accepts
		// (a duplicate alias, for instance); keep its tables rather than lose
		// the whole reference.
		qualified, report = parseAndQualify(d, defaultSchema, sql, false)
	}
	if qualified == nil {
		return Reference{}
	}

	tables := make([]Table, 0, 4)
	for _, tableExpr := range qualified.FindAll(expressions.KindTable) {
		src, ok := report[tableExpr]
		if !ok || src.Kind != optimizer.Physical {
			continue // a CTE/subquery reference or an unresolved name
		}
		path := physicalPath(src)
		if len(path) == 0 {
			continue
		}
		kind := "read"
		if isWriteTarget(qualified, tableExpr) {
			kind = "write"
		}
		tables = append(tables, Table{Path: path, Alias: tableAlias(tableExpr, path), Kind: kind})
	}

	res := newResolver(qualified, report)
	columns := make([]Column, 0, 8)
	seenColumns := make(map[Column]bool, 8)
	for _, columnExpr := range qualified.FindAll(expressions.KindColumn) {
		name := columnExpr.Name()
		if name == "" || name == "*" {
			continue // a star is not a column
		}
		scope := res.scopeOf(columnExpr)
		if isOutputAliasRef(scope, columnExpr) {
			continue // a reference to a projection alias, not a physical column
		}
		column := Column{Name: name}
		if physical, ok := res.column(scope, columnExpr); ok {
			column = Column{Namespace: tableNamespace(physical.table), Table: tableName(physical.table), Name: physical.column}
		}
		if seenColumns[column] {
			continue
		}
		seenColumns[column] = true
		columns = append(columns, column)
	}

	return Reference{Tables: tables, Columns: columns, Joins: res.joins(qualified, ambiguousAliases(tables))}
}

// parseAndQualify parses sql and runs the fork's qualify pass, returning the
// qualified tree and the resolution report keyed by its nodes. With columns,
// the pass also qualifies every column with the source it reads in its own
// scope (#1089); schema inference stays off, so a column is qualified only
// when the statement itself proves its source.
func parseAndQualify(d dialect.Dialect, defaultSchema, sql string, columns bool) (root expressions.Expression, report map[expressions.Expression]optimizer.ResolvedSource) {
	expr, err := dialect.ParseStatement(d, sql)
	if err != nil || expr == nil {
		return nil, nil
	}
	name, err := dialect.SqlglotDialect(d)
	if err != nil {
		return nil, nil
	}
	infer := false
	report = make(map[expressions.Expression]optimizer.ResolvedSource)
	opts := optimizer.QualifyOpts{
		Dialect:          name,
		ResolutionReport: report,
		// No column metadata and no identifier quoting: the returned tree keeps
		// the source spelling, normalized per dialect. Alias references are not
		// expanded: without column metadata the pass cannot tell a WHERE name
		// that reads a source column from a projection alias, so a name that
		// matches an alias is left to isOutputAliasRef instead of rewritten.
		QualifyColumns:            columns,
		ExpandStars:               columns,
		ExpandAliasRefs:           false,
		InferSchema:               &infer,
		AllowPartialQualification: true,
		ValidateQualifyColumns:    false,
		QuoteIdentifiers:          false,
		Identify:                  false,
	}
	if defaultSchema != "" {
		opts.DefaultSchema = defaultSchema
	}
	defer func() {
		if recover() != nil {
			root, report = nil, nil
		}
	}()
	return optimizer.Qualify(expr, opts), report
}

// physicalPath drops empty catalog/schema parts of a resolved source.
func physicalPath(src optimizer.ResolvedSource) []string {
	path := make([]string, 0, 3)
	for _, part := range []string{src.Catalog, src.Schema, src.Table} {
		if part != "" {
			path = append(path, part)
		}
	}
	return path
}

// tableAlias is the name a reference to the table uses: the alias it declares,
// or its own name when it declares none.
func tableAlias(t expressions.Expression, path []string) string {
	if alias := t.Alias(); alias != "" {
		return alias
	}
	return path[len(path)-1]
}

// maxFollowDepth bounds how many CTE or derived-table projections a column is
// followed through, so a malformed self-reference cannot recurse forever.
const maxFollowDepth = 64

// physicalColumn is a column reference resolved to the physical table it reads.
type physicalColumn struct {
	table  Table
	column string
}

// resolver maps the references of a qualified tree to physical columns. The
// fork's qualify pass has already qualified each column with the source it
// reads in its own scope; the resolver only finds that scope and follows a CTE
// or derived-table source to the physical table behind it.
type resolver struct {
	report map[expressions.Expression]optimizer.ResolvedSource
	// owner maps a node to the innermost scope that contains it.
	owner map[expressions.Expression]*optimizer.Scope
}

func newResolver(root expressions.Expression, report map[expressions.Expression]optimizer.ResolvedSource) (r *resolver) {
	r = &resolver{report: report, owner: map[expressions.Expression]*optimizer.Scope{}}
	defer func() {
		if recover() != nil {
			// A tree the fork cannot traverse resolves nothing: every column is
			// kept by name only, never against a guessed scope.
			r.owner = map[expressions.Expression]*optimizer.Scope{}
		}
	}()
	// TraverseScope yields a child scope before its parent, so the first scope
	// to claim a node is the innermost one.
	for _, scope := range optimizer.TraverseScope(root) {
		for _, node := range optimizer.WalkInScope(scope.Expression, nil) {
			if _, claimed := r.owner[node]; !claimed {
				r.owner[node] = scope
			}
		}
	}
	return r
}

func (r *resolver) scopeOf(node expressions.Expression) *optimizer.Scope {
	return r.owner[node]
}

// column resolves a column reference in scope to the physical column it reads.
func (r *resolver) column(scope *optimizer.Scope, columnExpr expressions.Expression) (physicalColumn, bool) {
	if scope == nil {
		return physicalColumn{}, false
	}
	source, ok := sourceOf(scope, columnExpr.TableName())
	if !ok {
		return physicalColumn{}, false
	}
	return r.follow(source, columnExpr.Name(), 0)
}

// sourceOf finds the source a qualifier names, from scope outward: a correlated
// reference names a source of an enclosing query. Only a source the scope binds
// as a row source (selected, or the table of a SEMI or ANTI JOIN) matches; a
// CTE the scope merely inherits does not. An unqualified reference resolves only
// when scope selects exactly one source, so it is never guessed.
func sourceOf(scope *optimizer.Scope, qualifier string) (any, bool) {
	if qualifier == "" {
		names := scope.SelectedSourceNames()
		if len(names) != 1 {
			return nil, false
		}
		source, ok := scope.Sources[names[0]]
		return source, ok
	}
	for cur := scope; cur != nil; cur = enclosing(cur) {
		bound := slices.Contains(cur.SelectedSourceNames(), qualifier) || cur.SemiOrAntiJoinTables()[qualifier]
		if !bound {
			continue
		}
		if source, ok := cur.Sources[qualifier]; ok {
			return source, true
		}
	}
	return nil, false
}

// enclosing is the next scope a reference may bind to. A CTE body and a
// non-lateral derived table cannot see the sources of the query that declares
// them, only those of the query around it.
func enclosing(scope *optimizer.Scope) *optimizer.Scope {
	if scope.Parent == nil {
		return nil
	}
	if scope.IsCTE() || scope.IsDerivedTable() {
		return scope.Parent.Parent
	}
	return scope.Parent
}

// follow resolves an output name of a source to the physical column behind it:
// a table is the physical source itself, and a CTE, derived table or subquery
// is followed through the projection that produces the name.
func (r *resolver) follow(source any, name string, depth int) (physicalColumn, bool) {
	if depth > maxFollowDepth {
		return physicalColumn{}, false
	}
	switch s := source.(type) {
	case expressions.Expression:
		resolved, ok := r.report[s]
		if !ok || resolved.Kind != optimizer.Physical {
			return physicalColumn{}, false
		}
		path := physicalPath(resolved)
		if len(path) == 0 {
			return physicalColumn{}, false
		}
		return physicalColumn{table: Table{Path: path, Alias: tableAlias(s, path)}, column: name}, true
	case *optimizer.Scope:
		return r.project(s, name, depth+1)
	default:
		return physicalColumn{}, false
	}
}

// project follows an output name of a query to the column its projection reads.
// A computed projection is not a physical column, and a set operation is not
// followed because its branches may read different tables.
func (r *resolver) project(scope *optimizer.Scope, name string, depth int) (physicalColumn, bool) {
	query := scope.Expression
	if query == nil || query.Kind() != expressions.KindSelect {
		return physicalColumn{}, false
	}
	var stars []expressions.Expression
	for _, item := range query.Selects() {
		if item == nil {
			continue
		}
		if isStar(item) {
			stars = append(stars, item)
			continue
		}
		if item.OutputName() != name {
			continue
		}
		inner := item
		if item.Kind() == expressions.KindAlias {
			inner = item.This()
		}
		if inner == nil || inner.Kind() != expressions.KindColumn {
			return physicalColumn{}, false
		}
		source, ok := sourceOf(scope, inner.TableName())
		if !ok {
			return physicalColumn{}, false
		}
		return r.follow(source, inner.Name(), depth)
	}
	// Without column metadata a star is not expanded: the name passes through
	// it only when one star could produce it.
	if len(stars) != 1 {
		return physicalColumn{}, false
	}
	source, ok := sourceOf(scope, stars[0].TableName())
	if !ok {
		return physicalColumn{}, false
	}
	return r.follow(source, name, depth)
}

// isStar reports a star projection, including the qualified o.* form, which the
// fork models as a column with a star child.
func isStar(item expressions.Expression) bool {
	if item.Kind() == expressions.KindStar {
		return true
	}
	return item.Kind() == expressions.KindColumn && item.This() != nil && item.This().Kind() == expressions.KindStar
}

// isOutputAliasRef reports an unqualified reference to a projection alias of its
// own scope (ORDER BY x after AS x), which is not a physical column. A column
// inside the projection list itself is never one.
func isOutputAliasRef(scope *optimizer.Scope, columnExpr expressions.Expression) bool {
	if scope == nil || columnExpr.TableName() != "" {
		return false
	}
	query := scope.Expression
	if query == nil || query.Kind() != expressions.KindSelect || !slices.Contains(query.NamedSelects(), columnExpr.Name()) {
		return false
	}
	projections := query.Selects()
	for node := expressions.Expression(columnExpr); node != nil && node != query; node = node.Parent() {
		if slices.Contains(projections, node) {
			return false
		}
	}
	return true
}

// joins resolves each equi-join predicate in the scope that contains it. An
// endpoint read through a CTE or derived table is named after the physical
// table and column behind it, so it matches the statement's table references;
// an endpoint that does not resolve to one physical column is skipped. A join is
// stored by alias, so an endpoint whose alias names more than one table of the
// statement is skipped too: its reader could not tell which table it means.
func (r *resolver) joins(root expressions.Expression, ambiguous map[string]bool) []Join {
	joins := make([]Join, 0, 2)
	seenJoins := make(map[Join]bool, 2)
	for _, equality := range root.FindAll(expressions.KindEQ) {
		left, right := equality.Left(), equality.Right()
		if left == nil || right == nil || left.Kind() != expressions.KindColumn || right.Kind() != expressions.KindColumn {
			continue
		}
		scope := r.scopeOf(equality)
		leftColumn, ok := r.column(scope, left)
		if !ok || ambiguous[strings.ToLower(leftColumn.table.Alias)] {
			continue
		}
		rightColumn, ok := r.column(scope, right)
		if !ok || ambiguous[strings.ToLower(rightColumn.table.Alias)] {
			continue
		}
		join := Join{
			LeftAlias: leftColumn.table.Alias, LeftColumn: leftColumn.column,
			RightAlias: rightColumn.table.Alias, RightColumn: rightColumn.column,
		}
		if seenJoins[join] {
			continue
		}
		seenJoins[join] = true
		joins = append(joins, join)
	}
	return joins
}

// ambiguousAliases reports the aliases that name more than one physical table in
// the statement, compared without case as the join reader matches them.
func ambiguousAliases(tables []Table) map[string]bool {
	pathOf := make(map[string]string, len(tables))
	ambiguous := map[string]bool{}
	for _, table := range tables {
		alias := strings.ToLower(table.Alias)
		path := strings.Join(table.Path, ".")
		if seen, ok := pathOf[alias]; ok && seen != path {
			ambiguous[alias] = true
		}
		pathOf[alias] = path
	}
	return ambiguous
}

func tableNamespace(t Table) string {
	if len(t.Path) >= 2 {
		return t.Path[len(t.Path)-2]
	}
	return ""
}

func tableName(t Table) string {
	return t.Path[len(t.Path)-1]
}

func isWriteTarget(root, table expressions.Expression) bool {
	switch root.Kind() {
	case expressions.KindInsert, expressions.KindUpdate, expressions.KindDelete, expressions.KindMerge:
		target := root.This()
		// INSERT INTO t (a, b) wraps the target in a Schema node; unwrap it so
		// the comparison reaches the physical table.
		if target != nil && target.Kind() == expressions.KindSchema {
			target = target.This()
		}
		return target == table
	default:
		return false
	}
}
