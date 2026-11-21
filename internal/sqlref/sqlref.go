// Package sqlref extracts the tables, columns and equi-joins a statement reads
// and writes from the sqlglot AST (#538, #625). Extraction runs the fork's
// qualify pass so a reference is resolved to its physical source: a CTE,
// subquery or derived table is followed to the table it reads, and an
// unqualified table is stamped with the target's default schema.
//
// Resolution is per scope (#1089): an alias reused in another scope, a column
// read through a CTE or derived table, and a join between them all resolve
// against the scope that contains them, never a statement-wide alias map.
//
// It is best-effort: no engine round-trip is needed, and a parse or qualify
// failure yields an empty Reference, never an error the caller must treat as
// fatal.
package sqlref

import (
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
	expr, err := dialect.ParseStatement(d, sql)
	if err != nil || expr == nil {
		return Reference{}
	}
	qualified, report := qualify(d, defaultSchema, expr)
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

	// Resolve every reference in the scope that declares it, so an alias
	// reused in another scope does not merge, and a column or join through a
	// CTE or derived table reaches the physical table it reads.
	index := buildScopeIndex(qualified)

	// A projection alias (AS x) referenced elsewhere is not a physical column;
	// skip it rather than infer a phantom. The alias binds only in the scope
	// that declares it, so a same-named column in another scope still resolves.
	columns := make([]Column, 0, 8)
	seenColumns := make(map[Column]bool, 8)
	for _, columnExpr := range qualified.FindAll(expressions.KindColumn) {
		name := columnExpr.Name()
		if name == "" || name == "*" {
			continue // a star is not a column
		}
		scope := index.scopeOf(columnExpr)
		if columnExpr.TableName() == "" && index.declaresAlias(scope, name) {
			continue // a reference to a projection alias, not a physical column
		}
		var namespace, table, physical string
		if columnExpr.FindAncestor(expressions.KindReturning) != nil {
			// A RETURNING name binds to the write target, not to the sources
			// the statement reads.
			namespace, table, physical = returningColumn(columnExpr, tables)
		} else {
			namespace, table, physical = index.column(columnExpr)
		}
		column := Column{Namespace: namespace, Table: table, Name: physical}
		if seenColumns[column] {
			continue
		}
		seenColumns[column] = true
		columns = append(columns, column)
	}

	return Reference{Tables: tables, Columns: columns, Joins: index.joins(qualified)}
}

// qualify runs the fork's qualify pass with table resolution only (no column
// metadata), returning the qualified tree and the resolution report keyed by
// the qualified tree's nodes.
func qualify(d dialect.Dialect, defaultSchema string, expr expressions.Expression) (root expressions.Expression, report map[expressions.Expression]optimizer.ResolvedSource) {
	name, err := dialect.SqlglotDialect(d)
	if err != nil {
		return nil, nil
	}
	report = make(map[expressions.Expression]optimizer.ResolvedSource)
	opts := optimizer.QualifyOpts{
		Dialect:          name,
		ResolutionReport: report,
		// Table resolution only: no column metadata, no star expansion, no
		// identifier quoting. The returned tree keeps source spelling.
		QualifyColumns:         false,
		ExpandStars:            false,
		ExpandAliasRefs:        false,
		ValidateQualifyColumns: false,
		QuoteIdentifiers:       false,
		Identify:               false,
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

// tableNodePath reads a resolved table's catalog/schema/name parts.
func tableNodePath(t expressions.Expression) []string {
	path := make([]string, 0, 3)
	for _, part := range []string{t.CatalogName(), t.SchemaName(), t.Name()} {
		if part != "" {
			path = append(path, part)
		}
	}
	return path
}

// scopeIndex resolves references per scope (#1089). scopes maps each scope to
// its selected sources, following CTE, derived-table and subquery scopes,
// aliases maps a scope to the projection aliases it declares, and nodeOf maps a
// node to the innermost scope that contains it.
type scopeIndex struct {
	root    *optimizer.Scope
	scopes  map[*optimizer.Scope]*scopeSources
	aliases map[*optimizer.Scope]*nameIndex[bool]
	nodeOf  map[expressions.Expression]*optimizer.Scope
}

// nameIndex maps an identifier to a value. The exact spelling matches first, so
// quoted aliases that differ only in case stay distinct; a case-insensitive
// match is the fallback for unquoted identifiers, and only while it names one
// entry.
type nameIndex[V any] struct {
	exact     map[string]V
	folded    map[string]V
	ambiguous map[string]bool
}

func (n *nameIndex[V]) set(name string, value V) {
	if n.exact == nil {
		n.exact = map[string]V{}
		n.folded = map[string]V{}
		n.ambiguous = map[string]bool{}
	}
	key := strings.ToLower(name)
	if _, exists := n.exact[name]; exists {
		n.exact[name] = value
		if !n.ambiguous[key] {
			n.folded[key] = value
		}
		return
	}
	n.exact[name] = value
	if n.ambiguous[key] {
		return
	}
	if _, exists := n.folded[key]; exists {
		delete(n.folded, key)
		n.ambiguous[key] = true
		return
	}
	n.folded[key] = value
}

func (n *nameIndex[V]) get(name string) (V, bool) {
	var zero V
	if n == nil {
		return zero, false
	}
	if value, ok := n.exact[name]; ok {
		return value, true
	}
	if value, ok := n.folded[strings.ToLower(name)]; ok {
		return value, true
	}
	return zero, false
}

// declared reports whether the name is declared, even when it resolves to an
// ambiguous value.
func (n *nameIndex[V]) declared(name string) bool {
	if n == nil {
		return false
	}
	if _, ok := n.exact[name]; ok {
		return true
	}
	key := strings.ToLower(name)
	if n.ambiguous[key] {
		return true
	}
	_, ok := n.folded[key]
	return ok
}

func (n *nameIndex[V]) length() int {
	if n == nil {
		return 0
	}
	return len(n.exact)
}

func (n *nameIndex[V]) one() (V, bool) {
	var zero V
	if n == nil || len(n.exact) != 1 {
		return zero, false
	}
	for _, value := range n.exact {
		return value, true
	}
	return zero, false
}

func (n *nameIndex[V]) merge(other *nameIndex[V]) {
	if other == nil {
		return
	}
	for name, value := range other.exact {
		n.set(name, value)
	}
}

// scopeSources is one scope's sources: the aliases it selects in FROM/JOIN,
// each with the physical table (or CTE/derived projection) it reads, the
// SEMI/ANTI JOIN tables its ON predicate may name, and the projection that maps
// output names to physical columns.
type scopeSources struct {
	selected *nameIndex[*sourceRef]
	// extra holds sources the projection does not expose but a predicate may
	// name, such as the right side of a SEMI or ANTI JOIN.
	extra   *nameIndex[*sourceRef]
	columns *nameIndex[string]
	star    bool
	// unknown marks a projection whose output names are not fully known (a set
	// operation, for instance).
	unknown bool
}

// sourceRef is one resolved source: a physical table and, for a CTE, derived
// table or subquery, the projection that maps its output names to columns.
type sourceRef struct {
	table    Table
	resolved bool
	// columns maps output names to physical columns; nil means the projection
	// is unknown, and an entry mapped to "" is not a physical column.
	columns *nameIndex[string]
	// star reports a projection of *, whose output names are physical names.
	star bool
	// unknown reports a projection whose output names are not fully known.
	unknown bool
}

// buildScopeIndex builds the per-scope resolution of one qualified tree. A
// tree the fork cannot traverse yields an empty index, so every reference is
// dropped rather than resolved against a guessed scope.
func buildScopeIndex(root expressions.Expression) (index *scopeIndex) {
	index = newScopeIndex()
	defer func() {
		if recover() != nil {
			index = newScopeIndex()
		}
	}()
	scopes := optimizer.TraverseScope(root)
	built := make(map[*optimizer.Scope]*scopeSources, len(scopes))
	var scopeSourcesOf func(*optimizer.Scope) *scopeSources
	scopeSourcesOf = func(scope *optimizer.Scope) *scopeSources {
		if sources, ok := built[scope]; ok {
			return sources
		}
		// A recursive CTE may reach itself before it is built: it stays
		// unresolved rather than recursing forever.
		sources := &scopeSources{selected: &nameIndex[*sourceRef]{}, extra: &nameIndex[*sourceRef]{}}
		built[scope] = sources
		for _, name := range scope.SelectedSourceNames() {
			if source, ok := scope.Sources[name]; ok {
				sources.selected.set(name, resolveSource(source, scopeSourcesOf))
			}
		}
		for name := range scope.SemiOrAntiJoinTables() {
			if source, ok := scope.Sources[name]; ok {
				sources.extra.set(name, resolveSource(source, scopeSourcesOf))
			}
		}
		sources.columns, sources.star, sources.unknown = projection(scope, sources)
		return sources
	}
	for _, scope := range scopes {
		if scope.Parent == nil {
			index.root = scope
		}
		index.scopes[scope] = scopeSourcesOf(scope)
		index.aliases[scope] = projectionAliases(scope)
	}
	for _, scope := range scopes {
		for _, node := range optimizer.WalkInScope(scope.Expression, nil) {
			// A union branch hangs off both its own scope and the union's,
			// whose walk still reaches it: the innermost scope wins.
			if owner, ok := index.nodeOf[node]; ok && scopeDepth(owner) >= scopeDepth(scope) {
				continue
			}
			index.nodeOf[node] = scope
		}
	}
	return index
}

func newScopeIndex() *scopeIndex {
	return &scopeIndex{
		scopes:  map[*optimizer.Scope]*scopeSources{},
		aliases: map[*optimizer.Scope]*nameIndex[bool]{},
		nodeOf:  map[expressions.Expression]*optimizer.Scope{},
	}
}

func scopeDepth(scope *optimizer.Scope) int {
	depth := 0
	for cur := scope.Parent; cur != nil; cur = cur.Parent {
		depth++
	}
	return depth
}

// resolveSource resolves one selected source to the physical table it reads: a
// table node directly, or the single physical table a CTE, derived table or
// subquery reads. A source that reads several tables is ambiguous and resolves
// to nothing, so no phantom reference is inferred.
func resolveSource(src any, scopeSourcesOf func(*optimizer.Scope) *scopeSources) *sourceRef {
	switch s := src.(type) {
	case expressions.Expression:
		if s == nil || s.Kind() != expressions.KindTable {
			return &sourceRef{}
		}
		path := tableNodePath(s)
		if len(path) == 0 {
			return &sourceRef{}
		}
		// A physical table's projection is its own columns, so an output name
		// is already the physical column name.
		return &sourceRef{table: Table{Path: path, Alias: tableAlias(s, path)}, resolved: true, star: true}
	case *optimizer.Scope:
		inner := scopeSourcesOf(s)
		source, ok := inner.selected.one()
		if !ok || !source.resolved {
			return &sourceRef{}
		}
		return &sourceRef{
			table: source.table, resolved: true,
			columns: inner.columns, star: inner.star, unknown: inner.unknown,
		}
	}
	return &sourceRef{}
}

// projectionAliases collects the projection aliases a scope declares, so an
// ORDER BY reference to one is not mistaken for a physical column. The filter
// is per scope: an alias of one SELECT does not hide a physical column of a
// nested scope (#1089).
func projectionAliases(scope *optimizer.Scope) *nameIndex[bool] {
	aliases := &nameIndex[bool]{}
	if scope.Expression == nil || scope.Expression.Kind() != expressions.KindSelect {
		return aliases
	}
	for _, item := range scope.Expression.Selects() {
		if item != nil && item.Kind() == expressions.KindAlias && item.Alias() != "" {
			aliases.set(item.Alias(), true)
		}
	}
	return aliases
}

// projection maps the output names of a scope's SELECT to the physical columns
// behind them, and reports a star projection. A CTE with an explicit column list
// names its outputs by position. A star inherits the projection of the source it
// expands, so a mapping upstream is not lost. A projection that is not a plain
// column (an expression, an aggregate) maps to "", so it never becomes a
// physical column.
func projection(scope *optimizer.Scope, sources *scopeSources) (*nameIndex[string], bool, bool) {
	if scope.Expression == nil || scope.Expression.Kind() != expressions.KindSelect {
		return nil, false, true
	}
	columns := &nameIndex[string]{}
	star := false
	unknown := false
	selects := scope.Expression.Selects()
	listed := scope.IsCTE() && len(scope.OuterColumns) == len(selects) && len(selects) > 0
	for i, item := range selects {
		if item == nil {
			continue
		}
		if item.Kind() == expressions.KindStar {
			star, unknown = expandStar(columns, star, unknown, sources, "")
			continue
		}
		output := item.OutputName()
		if listed {
			output = scope.OuterColumns[i]
		}
		if output == "" {
			continue
		}
		inner := item
		if item.Kind() == expressions.KindAlias {
			inner = item.This()
		}
		switch {
		case inner == nil:
			columns.set(output, "")
		case isStar(inner):
			star, unknown = expandStar(columns, star, unknown, sources, inner.TableName())
		case inner.Kind() == expressions.KindColumn:
			if physical, ok := sources.column(inner); ok {
				columns.set(output, physical)
			} else {
				columns.set(output, "")
			}
		default:
			columns.set(output, "")
		}
	}
	return columns, star, unknown
}

// isStar reports a star projection, including the qualified o.* form, which the
// fork models as a column with a star child.
func isStar(item expressions.Expression) bool {
	if item == nil {
		return false
	}
	if item.Kind() == expressions.KindStar {
		return true
	}
	return item.Kind() == expressions.KindColumn && item.This() != nil && item.This().Kind() == expressions.KindStar
}

// expandStar folds the projection of a source a star expands into the scope's
// own projection: a star over a CTE keeps that CTE's output-to-column mappings.
func expandStar(columns *nameIndex[string], star, unknown bool, sources *scopeSources, qualifier string) (bool, bool) {
	var source *sourceRef
	var ok bool
	if qualifier == "" {
		source, ok = sources.selected.one()
	} else {
		source, ok = sources.selected.get(qualifier)
	}
	if !ok || !source.resolved {
		return star, unknown
	}
	columns.merge(source.columns)
	return star || source.star, unknown || (source.columns == nil && !source.star)
}

// column maps an output name of a column expression to the physical column it
// reads, resolving its qualifier in this scope.
func (s *scopeSources) column(columnExpr expressions.Expression) (string, bool) {
	var source *sourceRef
	var ok bool
	if qualifier := columnExpr.TableName(); qualifier != "" {
		source, ok = s.selected.get(qualifier)
	} else {
		source, ok = s.selected.one()
	}
	if !ok {
		return "", false
	}
	physical, known, _ := source.column(columnExpr.Name())
	return physical, known
}

// column maps an output name to the physical column behind it. ok is false when
// the source does not establish one; unknown distinguishes a projection whose
// output names are not known from a known non-column projection, whose output
// must not be stored as a column.
func (s *sourceRef) column(name string) (physical string, ok, unknown bool) {
	if !s.resolved {
		return "", false, false
	}
	if physical, found := s.columns.get(name); found {
		return physical, physical != "", false
	}
	if s.star {
		return name, true, false
	}
	return "", false, s.unknown
}

// scopeOf is the innermost scope that contains node.
func (i *scopeIndex) scopeOf(node expressions.Expression) *optimizer.Scope {
	if scope, ok := i.nodeOf[node]; ok {
		return scope
	}
	return i.root
}

// nextVisible is the next enclosing scope a reference in scope may bind to. A
// CTE body is not correlated with the query that defines it, and a derived
// table cannot see the sources of the FROM it belongs to: their visibility
// starts at the enclosing query block.
func nextVisible(scope *optimizer.Scope) *optimizer.Scope {
	parent := scope.Parent
	if parent == nil {
		return nil
	}
	switch {
	case scope.IsCTE():
		return nil
	case scope.IsDerivedTable():
		return parent.Parent
	default:
		return parent
	}
}

// declaresAlias reports whether the scope's own SELECT declares a projection
// alias with that name.
func (i *scopeIndex) declaresAlias(scope *optimizer.Scope, name string) bool {
	aliases := i.aliases[scope]
	if aliases == nil {
		return false
	}
	_, ok := aliases.exact[name]
	return ok
}

// lookup resolves a table qualifier in scope and then in its visible enclosing
// scopes: a correlated reference names a table declared outside its own scope. A
// local declaration stops the walk even when it cannot be resolved, so an
// ambiguous local table never borrows an outer alias with the same name.
func (i *scopeIndex) lookup(scope *optimizer.Scope, qualifier string) (*sourceRef, bool) {
	if qualifier == "" {
		return nil, false
	}
	for cur := scope; cur != nil; cur = nextVisible(cur) {
		sources, ok := i.scopes[cur]
		if !ok {
			continue
		}
		for _, index := range []*nameIndex[*sourceRef]{sources.selected, sources.extra} {
			if source, found := index.get(qualifier); found {
				if !source.resolved {
					return nil, false
				}
				return source, true
			}
			if index.declared(qualifier) {
				return nil, false
			}
		}
	}
	return nil, false
}

// single resolves an unqualified reference to the only source visible from
// scope. A reference that could bind to a local source or to a visible outer
// one stays ambiguous, because no column metadata says which owns the name; a
// scope that declares several sources stays ambiguous even when some cannot be
// resolved.
func (i *scopeIndex) single(scope *optimizer.Scope) (*sourceRef, bool) {
	total := 0
	var found *sourceRef
	for cur := scope; cur != nil; cur = nextVisible(cur) {
		sources, ok := i.scopes[cur]
		if !ok {
			continue
		}
		total += sources.selected.length()
		if total > 1 {
			return nil, false
		}
		if source, ok := sources.selected.one(); ok {
			if !source.resolved {
				return nil, false
			}
			found = source
		}
	}
	if total != 1 {
		return nil, false
	}
	return found, true
}

// column resolves a column reference to the physical table and column of its
// qualifier, in the scope that declares it.
func (i *scopeIndex) column(columnExpr expressions.Expression) (namespace, table, name string) {
	scope := i.scopeOf(columnExpr)
	var source *sourceRef
	var ok bool
	if qualifier := columnExpr.TableName(); qualifier != "" {
		source, ok = i.lookup(scope, qualifier)
	} else {
		source, ok = i.single(scope)
	}
	if !ok {
		return "", "", columnExpr.Name()
	}
	physical, known, unknown := source.column(columnExpr.Name())
	switch {
	case known:
		return tableNamespace(source.table), tableName(source.table), physical
	case unknown:
		// The projection is unknown but the source is a single physical table:
		// keep the reference best-effort, as before.
		return tableNamespace(source.table), tableName(source.table), columnExpr.Name()
	default:
		return "", "", columnExpr.Name() // a known non-column projection
	}
}

// joins resolves each equi-join predicate in the scope that contains it, so a
// join between a CTE or derived table and a physical table is kept, named after
// the physical tables and columns it reads instead of an alias that is not a
// table. An endpoint whose physical column cannot be established is skipped.
func (i *scopeIndex) joins(root expressions.Expression) []Join {
	joins := make([]Join, 0, 2)
	seenJoins := make(map[Join]bool, 2)
	for _, equality := range root.FindAll(expressions.KindEQ) {
		left, right := equality.Left(), equality.Right()
		if left == nil || right == nil || left.Kind() != expressions.KindColumn || right.Kind() != expressions.KindColumn {
			continue
		}
		scope := i.scopeOf(equality)
		leftSource, ok := i.lookup(scope, left.TableName())
		if !ok {
			continue
		}
		leftColumn, ok, _ := leftSource.column(left.Name())
		if !ok {
			continue
		}
		rightSource, ok := i.lookup(scope, right.TableName())
		if !ok {
			continue
		}
		rightColumn, ok, _ := rightSource.column(right.Name())
		if !ok {
			continue
		}
		join := Join{
			LeftAlias: leftSource.table.Alias, LeftColumn: leftColumn,
			RightAlias: rightSource.table.Alias, RightColumn: rightColumn,
		}
		if seenJoins[join] {
			continue
		}
		seenJoins[join] = true
		joins = append(joins, join)
	}
	return joins
}

// returningColumn resolves a RETURNING reference against the statement's write
// target: an unqualified name there binds to the target table, never to a
// source the write reads (#1089).
func returningColumn(columnExpr expressions.Expression, tables []Table) (namespace, table, name string) {
	name = columnExpr.Name()
	qualifier := columnExpr.TableName()
	var target *Table
	for i := range tables {
		if tables[i].Kind != "write" {
			continue
		}
		if qualifier != "" && !strings.EqualFold(qualifier, tables[i].Alias) && !strings.EqualFold(qualifier, tableName(tables[i])) {
			continue
		}
		if target != nil {
			return "", "", name // several write targets: ambiguous
		}
		target = &tables[i]
	}
	if target == nil {
		return "", "", name
	}
	return tableNamespace(*target), tableName(*target), name
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
