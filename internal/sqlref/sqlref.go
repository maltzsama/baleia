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
		alias := tableExpr.Alias()
		if alias == "" {
			alias = path[len(path)-1]
		}
		kind := "read"
		if isWriteTarget(qualified, tableExpr) {
			kind = "write"
		}
		tables = append(tables, Table{Path: path, Alias: alias, Kind: kind})
	}

	// Resolve column qualifiers against every scope's sources, so a column
	// through a CTE or derived table resolves to the physical table it reads.
	byAlias := physicalTablesByAlias(qualified)

	// A projection alias (AS x) referenced elsewhere (ORDER BY x) is not a
	// physical column; skip it rather than infer a phantom.
	outputAliases := make(map[string]bool)
	for _, alias := range qualified.FindAll(expressions.KindAlias) {
		if name := alias.Alias(); name != "" {
			outputAliases[strings.ToLower(name)] = true
		}
	}

	columns := make([]Column, 0, 8)
	seenColumns := make(map[Column]bool, 8)
	for _, columnExpr := range qualified.FindAll(expressions.KindColumn) {
		name := columnExpr.Name()
		if name == "" {
			continue
		}
		if columnExpr.TableName() == "" && outputAliases[strings.ToLower(name)] {
			continue // a reference to a projection alias, not a physical column
		}
		namespace, table := resolveColumn(columnExpr.TableName(), byAlias, tables)
		column := Column{Namespace: namespace, Table: table, Name: name}
		if seenColumns[column] {
			continue
		}
		seenColumns[column] = true
		columns = append(columns, column)
	}

	return Reference{Tables: tables, Columns: columns, Joins: extractJoins(qualified, tables)}
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

// physicalTablesByAlias maps an alias to the physical table it names, following
// CTE and derived-table scopes: for `WITH x AS (SELECT id FROM orders)`, the
// alias `x` maps to `orders`. An alias that resolves to no table, or to more
// than one, is omitted (best-effort, never a phantom).
func physicalTablesByAlias(root expressions.Expression) (out map[string]Table) {
	out = map[string]Table{}
	defer func() {
		if recover() != nil {
			out = map[string]Table{}
		}
	}()
	var resolve func(any) ([]string, bool)
	resolve = func(src any) ([]string, bool) {
		switch s := src.(type) {
		case expressions.Expression:
			if s != nil && s.Kind() == expressions.KindTable {
				return tableNodePath(s), true
			}
		case *optimizer.Scope:
			var paths [][]string
			for _, inner := range s.Sources {
				if path, ok := resolve(inner); ok {
					paths = append(paths, path)
				}
			}
			if len(paths) == 1 {
				return paths[0], true
			}
		}
		return nil, false
	}
	for _, scope := range optimizer.TraverseScope(root) {
		for alias, src := range scope.Sources {
			if path, ok := resolve(src); ok {
				out[strings.ToLower(alias)] = Table{Path: path}
			}
		}
	}
	return out
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

// resolveColumn resolves a column's qualifier (the alias as written) to the
// physical table's namespace and name; an empty qualifier resolves to the only
// table when the statement reads exactly one. A qualifier that names no single
// physical table resolves to nothing, so the column is dropped rather than
// persisted as a phantom.
func resolveColumn(qualifier string, byAlias map[string]Table, tables []Table) (namespace, table string) {
	if qualifier != "" {
		if t, ok := byAlias[strings.ToLower(qualifier)]; ok {
			return tableNamespace(t), tableName(t)
		}
		return "", ""
	}
	if len(tables) == 1 {
		return tableNamespace(tables[0]), tableName(tables[0])
	}
	return "", ""
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

func extractJoins(root expressions.Expression, tables []Table) []Join {
	aliases := make(map[string]bool, len(tables))
	for _, table := range tables {
		aliases[strings.ToLower(table.Alias)] = true
	}
	joins := make([]Join, 0, 2)
	seenJoins := make(map[Join]bool, 2)
	for _, equality := range root.FindAll(expressions.KindEQ) {
		left, right := equality.Left(), equality.Right()
		if left == nil || right == nil || left.Kind() != expressions.KindColumn || right.Kind() != expressions.KindColumn {
			continue
		}
		join := Join{
			LeftAlias: left.TableName(), LeftColumn: left.Name(),
			RightAlias: right.TableName(), RightColumn: right.Name(),
		}
		if join.LeftAlias == "" || join.RightAlias == "" ||
			!aliases[strings.ToLower(join.LeftAlias)] || !aliases[strings.ToLower(join.RightAlias)] ||
			join.LeftColumn == "" || join.RightColumn == "" || seenJoins[join] {
			continue
		}
		seenJoins[join] = true
		joins = append(joins, join)
	}
	return joins
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
