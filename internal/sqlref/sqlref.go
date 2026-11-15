// Package sqlref extracts table, column and equi-join references from the
// sqlglot AST. It is pure: no engine round-trip is needed and parse failures
// produce an empty result for best-effort lineage callers.
package sqlref

import (
	"strings"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/maltzsama/sqlglot-go/expressions"
)

// Table is one table named in FROM/JOIN/UPDATE/INTO.
type Table struct {
	// Path is the name as written, 1 to 3 parts (table, schema.table,
	// catalog.schema.table).
	Path  []string
	Alias string
	// Kind is "read" for FROM/JOIN and "write" for UPDATE/INTO.
	Kind string
}

// Column is one column reference. TableAlias is empty for an unqualified
// column; Name is the identifier without its qualifier.
type Column struct {
	TableAlias string
	Name       string
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
// know the connection dialect should use ExtractWithDialect.
func Extract(sql string) Reference {
	return ExtractWithDialect(genericDialect, sql)
}

// ExtractWithDialect returns the tables, columns and equi-joins represented by
// one statement's AST. An unparsable statement yields an empty Reference,
// never an error the best-effort callers must treat as fatal.
func ExtractWithDialect(d dialect.Dialect, sql string) Reference {
	expr, err := dialect.ParseStatement(d, sql)
	if err != nil || expr == nil {
		return Reference{}
	}

	tables := make([]Table, 0)
	for _, tableExpr := range expr.FindAll(expressions.KindTable) {
		path := tablePath(tableExpr)
		if len(path) == 0 {
			continue
		}
		alias := tableExpr.Alias()
		if alias == "" {
			alias = path[len(path)-1]
		}
		kind := "read"
		if isWriteTarget(expr, tableExpr) {
			kind = "write"
		}
		tables = append(tables, Table{Path: path, Alias: alias, Kind: kind})
	}

	columns := make([]Column, 0)
	seenColumns := make(map[Column]bool)
	for _, columnExpr := range expr.FindAll(expressions.KindColumn) {
		column := Column{TableAlias: columnExpr.TableName(), Name: columnExpr.Name()}
		if column.Name == "" || seenColumns[column] {
			continue
		}
		seenColumns[column] = true
		columns = append(columns, column)
	}

	joins := make([]Join, 0)
	aliases := make(map[string]bool, len(tables))
	for _, table := range tables {
		aliases[strings.ToLower(table.Alias)] = true
	}
	seenJoins := make(map[Join]bool)
	for _, equality := range expr.FindAll(expressions.KindEQ) {
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

	return Reference{Tables: tables, Columns: columns, Joins: joins}
}

func tablePath(table expressions.Expression) []string {
	path := make([]string, 0, 3)
	for _, key := range []string{"catalog", "schema", "this"} {
		part, ok := table.Arg(key).(expressions.Expression)
		if !ok || part == nil {
			continue
		}
		name := part.Name()
		if name == "" {
			name = part.Text("this")
		}
		if name != "" {
			path = append(path, name)
		}
	}
	return path
}

func isWriteTarget(root, table expressions.Expression) bool {
	switch root.Kind() {
	case expressions.KindInsert, expressions.KindUpdate, expressions.KindDelete, expressions.KindMerge:
		return root.This() == table
	default:
		return false
	}
}
