package datapolicy

import (
	"fmt"
	"strings"
	"unicode/utf8"

	sqlglot "github.com/maltzsama/sqlglot-go"
	sqlerrors "github.com/maltzsama/sqlglot-go/errors"
	"github.com/maltzsama/sqlglot-go/expressions"
	"github.com/maltzsama/sqlglot-go/generator"
)

// MaxExpressionLength bounds a policy expression, in characters.
const MaxExpressionLength = 4096

// trinoDialect is the dialect policy expressions are written in: Trino
// evaluates them (ADR 0028).
const trinoDialect = "trino"

// Reason says why an expression was refused. It is stable, so the API can
// return it to the UI.
type Reason string

const (
	ReasonEmpty            Reason = "empty"
	ReasonTooLong          Reason = "too_long"
	ReasonSyntax           Reason = "syntax"
	ReasonNotAnExpression  Reason = "not_an_expression"
	ReasonNotAllowed       Reason = "not_allowed"
	ReasonQualifiedColumn  Reason = "qualified_column"
	ReasonUnknownColumn    Reason = "unknown_column"
	ReasonFunctionNotAllow Reason = "function_not_allowed"
)

// ExprError is a refused expression. Detail names the offending construct
// (a function, a column or a node kind), never the expression itself.
type ExprError struct {
	Reason Reason
	Detail string
}

func (e *ExprError) Error() string {
	if e.Detail == "" {
		return "data policy expression: " + string(e.Reason)
	}
	return fmt.Sprintf("data policy expression: %s: %s", e.Reason, e.Detail)
}

// Compile checks a policy expression against the restricted language of ADR
// 0028 and returns its canonical Trino SQL.
//
// The language is: the target table's own columns, unqualified; literals;
// comparison, boolean and arithmetic operators; IN with literal lists; CASE,
// COALESCE and casts; and the deterministic scalar functions of
// allowedFunctionCalls. columns are the table's column names; a nil set skips
// the column check (the engine's type check still catches a missing column).
//
// The result is regenerated from the checked tree, so what Trino runs is what
// was checked, not the author's text. It is checked again after generation.
func Compile(expr string, columns []string) (string, error) {
	known := columnSet(columns)
	tree, err := checkedTree(expr, known)
	if err != nil {
		return "", err
	}
	canonical, err := generate(tree)
	if err != nil {
		return "", err
	}
	again, err := checkedTree(canonical, known)
	if err != nil {
		return "", fmt.Errorf("canonical form refused: %w", err)
	}
	if regenerated, err := generate(again); err != nil || regenerated != canonical {
		return "", &ExprError{Reason: ReasonNotAllowed, Detail: "unstable canonical form"}
	}
	return canonical, nil
}

func columnSet(columns []string) map[string]bool {
	if columns == nil {
		return nil
	}
	out := make(map[string]bool, len(columns))
	for _, c := range columns {
		out[strings.ToLower(c)] = true
	}
	return out
}

// checkedTree parses expr as the single projection of a SELECT and walks it.
func checkedTree(expr string, columns map[string]bool) (expressions.Expression, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, &ExprError{Reason: ReasonEmpty}
	}
	if utf8.RuneCountInString(expr) > MaxExpressionLength {
		return nil, &ExprError{Reason: ReasonTooLong, Detail: fmt.Sprintf("more than %d characters", MaxExpressionLength)}
	}
	stmt, err := sqlglot.ParseOne("SELECT "+expr, trinoDialect)
	if err != nil {
		return nil, &ExprError{Reason: ReasonSyntax}
	}
	tree, err := soleProjection(stmt)
	if err != nil {
		return nil, err
	}
	if err := walk(tree, expressions.KindSelect, columns); err != nil {
		return nil, err
	}
	return tree, nil
}

// soleProjection returns the one expression a "SELECT <expr>" carries. Any
// other clause (FROM, WHERE, a second projection, a second statement) means
// the text was not a single expression.
func soleProjection(stmt expressions.Expression) (expressions.Expression, error) {
	if stmt == nil || stmt.Kind() != expressions.KindSelect {
		return nil, &ExprError{Reason: ReasonNotAnExpression}
	}
	projections := stmt.Expressions()
	children := stmt.IterExpressions(false)
	if len(projections) != 1 || len(children) != 1 || projections[0] == nil {
		return nil, &ExprError{Reason: ReasonNotAnExpression}
	}
	return projections[0], nil
}

// walk checks node and its subtree. parent is the kind of node's parent, for
// the kinds that are only allowed in one place.
func walk(node expressions.Expression, parent expressions.Kind, columns map[string]bool) error {
	if node == nil {
		return nil
	}
	kind := node.Kind()
	switch {
	case kind == expressions.KindColumn:
		return checkColumn(node, columns)
	case kind == expressions.KindAnonymous:
		name := strings.ToLower(node.Name())
		if !allowedAnonymous[name] {
			return &ExprError{Reason: ReasonFunctionNotAllow, Detail: name}
		}
	case kind == expressions.KindDataType || kind == expressions.KindDataTypeParam:
		if !castKinds[parent] && parent != expressions.KindDataType {
			return notAllowed(kind)
		}
	case kind == expressions.KindVar:
		// date_trunc's unit is the only bare word an expression carries.
		if parent != expressions.KindTimestampTrunc {
			return notAllowed(kind)
		}
	case structuralKinds[kind]:
	case allowedFunctionKinds[kind]:
	case node.Is(expressions.TraitFunc):
		return &ExprError{Reason: ReasonFunctionNotAllow, Detail: strings.ToLower(expressions.ClassName(kind))}
	default:
		return notAllowed(kind)
	}
	for _, child := range node.IterExpressions(false) {
		if err := walk(child, kind, columns); err != nil {
			return err
		}
	}
	return nil
}

func notAllowed(kind expressions.Kind) error {
	return &ExprError{Reason: ReasonNotAllowed, Detail: strings.ToLower(expressions.ClassName(kind))}
}

// checkColumn accepts an unqualified reference to one of the table's own
// columns. Its only child is the identifier, which needs no further walk.
func checkColumn(node expressions.Expression, columns map[string]bool) error {
	for _, part := range []string{"table", "db", "catalog"} {
		if node.Arg(part) != nil {
			return &ExprError{Reason: ReasonQualifiedColumn, Detail: strings.ToLower(node.Name())}
		}
	}
	id := node.This()
	if id == nil || id.Kind() != expressions.KindIdentifier {
		return notAllowed(node.Kind())
	}
	name := strings.ToLower(id.Name())
	if sessionKeywords[name] {
		return &ExprError{Reason: ReasonFunctionNotAllow, Detail: name}
	}
	if columns != nil && !columns[name] {
		return &ExprError{Reason: ReasonUnknownColumn, Detail: name}
	}
	return nil
}

// generate renders a checked tree as Trino SQL, without comments.
func generate(tree expressions.Expression) (string, error) {
	noComments := false
	raise := sqlerrors.RAISE
	out, err := sqlglot.Generate(tree, trinoDialect, generator.Options{Comments: &noComments, UnsupportedLevel: &raise})
	if err != nil {
		return "", &ExprError{Reason: ReasonNotAllowed, Detail: "cannot render"}
	}
	if strings.TrimSpace(out) == "" {
		return "", &ExprError{Reason: ReasonNotAllowed, Detail: "cannot render"}
	}
	return out, nil
}
