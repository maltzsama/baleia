package datapolicy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	trinoclient "github.com/maltzsama/baleia/internal/trino"
)

// TypeChecker asks the engine for the type of expressions over a table. It
// runs as a trusted identity and reads no rows (ADR 0028: type checks run in
// the engine). A *trinoclient.QueryError of type USER_ERROR means the engine
// refused an expression; any other error means the check could not run.
type TypeChecker interface {
	ExpressionTypes(ctx context.Context, table []string, exprs []string) ([]string, error)
}

// Reasons of an invalid policy besides the expression language's own.
const (
	ReasonEngineRefused Reason = "engine_refused"
	ReasonNotBoolean    Reason = "not_boolean"
	ReasonTypeMismatch  Reason = "type_mismatch"
)

// Check validates p's expression and records the outcome on p: the language
// check of Compile against the table's columns, then the engine's type check
// over table (the catalog, schema and table names on the engine). A row
// filter must be boolean; a mask must keep its column's type.
//
// A refused expression is not an error: p becomes an invalid draft. Check
// returns an error only when the engine could not be asked, and p is then
// left unchanged.
func Check(ctx context.Context, p *Policy, columns []string, tc TypeChecker, table []string) error {
	compiled, err := Compile(p.Expression, columns)
	var exprErr *ExprError
	if errors.As(err, &exprErr) {
		p.markInvalid(exprErr.Reason, exprErr.Detail)
		return nil
	}
	if err != nil {
		return err
	}

	exprs := []string{compiled}
	if p.Kind == KindColumnMask {
		// COALESCE has a type only when the mask can be coerced to the
		// column's type, and it is the column's type only when nothing wider
		// was needed: the same rule Trino applies to a mask.
		column := quoteIdentifier(p.Target.Column)
		exprs = []string{fmt.Sprintf("COALESCE(%s, %s)", column, compiled), column}
	}
	types, err := tc.ExpressionTypes(ctx, table, exprs)
	var qe *trinoclient.QueryError
	if errors.As(err, &qe) && qe.ErrorType == "USER_ERROR" {
		p.markInvalid(ReasonEngineRefused, qe.Code())
		return nil
	}
	if err != nil {
		return fmt.Errorf("type check: %w", err)
	}
	if len(types) != len(exprs) {
		return fmt.Errorf("type check: the engine returned %d types for %d expressions", len(types), len(exprs))
	}

	switch {
	case p.Kind == KindRowFilter && types[0] != "boolean":
		p.markInvalid(ReasonNotBoolean, types[0])
	case p.Kind == KindColumnMask && types[0] != types[1]:
		p.markInvalid(ReasonTypeMismatch, types[0])
	default:
		p.Validation, p.Compiled, p.InvalidReason, p.InvalidDetail = ValidationValid, compiled, "", ""
	}
	return nil
}

func (p *Policy) markInvalid(reason Reason, detail string) {
	p.Validation, p.Compiled = ValidationInvalid, ""
	p.InvalidReason, p.InvalidDetail = string(reason), truncate(detail, MaxIdentifierLength)
}

// quoteIdentifier quotes a column name for Trino.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
