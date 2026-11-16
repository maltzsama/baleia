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

// Column is one column reference. Table is the resolved physical table name,
// or empty when the reference is unqualified and ambiguous or unresolved.
type Column struct {
	Table string
	Name  string
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
	byAlias := make(map[string]Table, 4)
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
		table := Table{Path: path, Alias: alias, Kind: kind}
		tables = append(tables, table)
		byAlias[strings.ToLower(alias)] = table
	}

	columns := make([]Column, 0, 8)
	seenColumns := make(map[Column]bool, 8)
	for _, columnExpr := range qualified.FindAll(expressions.KindColumn) {
		name := columnExpr.Name()
		if name == "" {
			continue
		}
		column := Column{Name: name, Table: resolveColumnTable(columnExpr.TableName(), byAlias, tables)}
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

// resolveColumnTable resolves a column's qualifier (the alias as written) to
// the physical table name; an empty qualifier resolves to the only table when
// the statement reads exactly one, and stays empty otherwise.
func resolveColumnTable(qualifier string, byAlias map[string]Table, tables []Table) string {
	if qualifier != "" {
		if table, ok := byAlias[strings.ToLower(qualifier)]; ok {
			return table.Path[len(table.Path)-1]
		}
		return qualifier // an unresolved qualifier: keep the spelling
	}
	if len(tables) == 1 {
		return tables[0].Path[len(tables[0].Path)-1]
	}
	return ""
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
		return root.This() == table
	default:
		return false
	}
}
