package datapolicy

import (
	"context"
	"errors"
	"slices"
	"testing"

	trinoclient "github.com/galileostudio/baleia/internal/trino"
)

// fakeTypes answers with fixed types, or an error, and records what it was
// asked.
type fakeTypes struct {
	types []string
	err   error
	table []string
	exprs []string
}

func (f *fakeTypes) ExpressionTypes(_ context.Context, table, exprs []string) ([]string, error) {
	f.table, f.exprs = table, exprs
	return f.types, f.err
}

var ordersTable = []string{"sales", "public", "orders"}

func TestCheck(t *testing.T) {
	t.Parallel()
	refused := &trinoclient.QueryError{ErrorName: "TYPE_MISMATCH", ErrorType: "USER_ERROR", Message: "raw engine text"}
	tests := []struct {
		name          string
		policy        Policy
		engine        fakeTypes
		expectedExprs []string
		validation    Validation
		reason        string
		detail        string
		compiled      string
	}{
		{
			name:          "boolean row filter",
			policy:        Policy{Kind: KindRowFilter, Expression: "region = 'EU'"},
			engine:        fakeTypes{types: []string{"boolean"}},
			expectedExprs: []string{"region = 'EU'"},
			validation:    ValidationValid,
			compiled:      "region = 'EU'",
		},
		{
			name:          "row filter that is not boolean",
			policy:        Policy{Kind: KindRowFilter, Expression: "amount + 1"},
			engine:        fakeTypes{types: []string{"integer"}},
			expectedExprs: []string{"amount + 1"},
			validation:    ValidationInvalid,
			reason:        "not_boolean",
			detail:        "integer",
		},
		{
			name:          "mask that keeps the column type",
			policy:        Policy{Kind: KindColumnMask, Target: Target{Column: "email"}, Expression: "'***'"},
			engine:        fakeTypes{types: []string{"varchar", "varchar"}},
			expectedExprs: []string{`COALESCE("email", '***')`, `"email"`},
			validation:    ValidationValid,
			compiled:      "'***'",
		},
		{
			name:          "mask that widens the column type",
			policy:        Policy{Kind: KindColumnMask, Target: Target{Column: "amount"}, Expression: "amount * 1.5"},
			engine:        fakeTypes{types: []string{"decimal(12,1)", "integer"}},
			expectedExprs: []string{`COALESCE("amount", amount * 1.5)`, `"amount"`},
			validation:    ValidationInvalid,
			reason:        "type_mismatch",
			detail:        "decimal(12,1)",
		},
		{
			name:       "expression the engine refuses",
			policy:     Policy{Kind: KindRowFilter, Expression: "region = 1"},
			engine:     fakeTypes{err: refused},
			validation: ValidationInvalid,
			reason:     "engine_refused",
			detail:     "type_mismatch",
		},
		{
			name:       "expression outside the language",
			policy:     Policy{Kind: KindRowFilter, Expression: "owner = current_user"},
			validation: ValidationInvalid,
			reason:     "function_not_allowed",
			detail:     "currentuser",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := tt.policy
			p.Compiled, p.Validation = "stale", ValidationValid
			engine := tt.engine
			if err := Check(context.Background(), &p, testColumns, &engine, ordersTable); err != nil {
				t.Fatalf("Check error = %v", err)
			}
			if p.Validation != tt.validation || p.InvalidReason != tt.reason || p.InvalidDetail != tt.detail {
				t.Fatalf("Check = %s %q %q, want %s %q %q", p.Validation, p.InvalidReason, p.InvalidDetail, tt.validation, tt.reason, tt.detail)
			}
			if p.Compiled != tt.compiled {
				t.Fatalf("compiled = %q, want %q", p.Compiled, tt.compiled)
			}
			if tt.expectedExprs != nil && !slices.Equal(engine.exprs, tt.expectedExprs) {
				t.Fatalf("asked the engine for %q, want %q", engine.exprs, tt.expectedExprs)
			}
			if tt.expectedExprs != nil && !slices.Equal(engine.table, ordersTable) {
				t.Fatalf("asked the engine about %v, want %v", engine.table, ordersTable)
			}
		})
	}
}

func TestCheck_EngineUnavailableLeavesThePolicy(t *testing.T) {
	t.Parallel()
	for _, err := range []error{
		errors.New("connection refused"),
		&trinoclient.QueryError{ErrorName: "GENERIC_INTERNAL_ERROR", ErrorType: "INTERNAL_ERROR"},
	} {
		p := Policy{Kind: KindRowFilter, Expression: "region = 'EU'", Validation: ValidationPending}
		if got := Check(context.Background(), &p, testColumns, &fakeTypes{err: err}, ordersTable); got == nil {
			t.Fatalf("Check with engine error %v returned nil", err)
		}
		if p.Validation != ValidationPending || p.Compiled != "" {
			t.Fatalf("an engine error changed the policy: %+v", p)
		}
	}
}

func TestCheck_WrongTypeCount(t *testing.T) {
	t.Parallel()
	p := Policy{Kind: KindRowFilter, Expression: "region = 'EU'"}
	if err := Check(context.Background(), &p, testColumns, &fakeTypes{types: []string{}}, ordersTable); err == nil {
		t.Fatal("Check accepted an answer without types")
	}
}
