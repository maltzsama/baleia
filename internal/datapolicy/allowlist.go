package datapolicy

import (
	"fmt"
	"strings"

	sqlglot "github.com/maltzsama/sqlglot-go"
	"github.com/maltzsama/sqlglot-go/expressions"
)

// The allow-list of ADR 0028 is security-relevant code: adding to it needs
// review. A function belongs here only when it is a deterministic scalar
// function that reads nothing but its arguments: no session state, no clock,
// no randomness, no other table.

// allowedFunctionCalls are the Trino functions a policy expression may call,
// written as sample calls. The parser maps most of them to a typed node and
// the rest to an Anonymous node carrying the name; both sets are derived from
// these samples, and TestAllowedFunctionKinds pins the result so a parser
// upgrade that changes the mapping shows up in review.
var allowedFunctionCalls = []string{
	// numbers
	"abs(c)", "ceil(c)", "ceiling(c)", "floor(c)", "round(c)", "round(c, 2)", "truncate(c)",
	"sign(c)", "greatest(c, c)", "least(c, c)",
	// strings
	"lower(c)", "upper(c)", "trim(c)", "ltrim(c)", "rtrim(c)", "length(c)",
	"substr(c, 1)", "substr(c, 1, 2)", "substring(c, 1, 2)", "concat(c, c)",
	"replace(c, 'a')", "replace(c, 'a', 'b')", "lpad(c, 1, 'a')", "rpad(c, 1, 'a')",
	"reverse(c)", "strpos(c, 'a')", "starts_with(c, 'a')", "split_part(c, 'a', 1)",
	"regexp_like(c, 'a')", "regexp_replace(c, 'a')", "regexp_replace(c, 'a', 'b')",
	"regexp_extract(c, 'a')", "regexp_extract(c, 'a', 1)",
	// hashing for masks (ADR 0028: no built-in hash type, no HMAC secret)
	"to_utf8(c)", "to_hex(c)", "sha256(c)", "sha512(c)",
	// dates
	"year(c)", "quarter(c)", "month(c)", "day(c)", "date_trunc('day', c)",
	// The parser reads date_trunc over a DATE as its own node.
	"date_trunc('day', CAST(c AS DATE))",
	"date_add('day', 1, c)", "date_diff('day', c, c)",
	// conditionals
	"nullif(c, c)", "coalesce(c, c)", "if(c, c)", "if(c, c, c)",
}

// structuralKinds are the operators and literals of the language. Column,
// Anonymous, DataType and Var are judged by walk itself.
var structuralKinds = kindSet(
	expressions.KindLiteral,
	expressions.KindNull,
	expressions.KindBoolean,
	expressions.KindParen,
	expressions.KindNeg,
	expressions.KindNot,
	expressions.KindAnd,
	expressions.KindOr,
	expressions.KindEQ,
	expressions.KindNEQ,
	expressions.KindGT,
	expressions.KindGTE,
	expressions.KindLT,
	expressions.KindLTE,
	expressions.KindNullSafeEQ,
	expressions.KindNullSafeNEQ,
	expressions.KindIs,
	expressions.KindIn,
	expressions.KindBetween,
	expressions.KindLike,
	expressions.KindEscape,
	expressions.KindAdd,
	expressions.KindSub,
	expressions.KindMul,
	expressions.KindDiv,
	expressions.KindMod,
	expressions.KindDPipe,
	expressions.KindCase,
	expressions.KindIf,
	expressions.KindCoalesce,
	expressions.KindCast,
	expressions.KindTryCast,
)

// castKinds may hold a DataType.
var castKinds = kindSet(expressions.KindCast, expressions.KindTryCast)

// sessionKeywords are Trino's niladic special forms. The parser reads some of
// them as a bare column; Trino evaluates them from the session, so they are
// refused even when a column has that name.
var sessionKeywords = map[string]bool{
	"current_user": true, "current_date": true, "current_time": true, "current_timestamp": true,
	"localtime": true, "localtimestamp": true, "current_catalog": true, "current_schema": true,
	"current_path": true, "current_role": true,
}

// allowedFunctionKinds and allowedAnonymous are derived from
// allowedFunctionCalls once, at start-up.
var allowedFunctionKinds, allowedAnonymous = deriveAllowList(allowedFunctionCalls)

func kindSet(kinds ...expressions.Kind) map[expressions.Kind]bool {
	out := make(map[expressions.Kind]bool, len(kinds))
	for _, k := range kinds {
		out[k] = true
	}
	return out
}

// deriveAllowList parses each sample call and collects the function nodes it
// produces. A sample that does not parse is a programming error.
func deriveAllowList(calls []string) (map[expressions.Kind]bool, map[string]bool) {
	kinds := map[expressions.Kind]bool{}
	anonymous := map[string]bool{}
	for _, call := range calls {
		stmt, err := sqlglot.ParseOne("SELECT "+call, trinoDialect)
		if err != nil {
			panic(fmt.Sprintf("datapolicy: allow-listed call %q does not parse: %v", call, err))
		}
		tree, err := soleProjection(stmt)
		if err != nil {
			panic(fmt.Sprintf("datapolicy: allow-listed call %q is not one expression", call))
		}
		for _, node := range tree.Walk() {
			switch {
			case node.Kind() == expressions.KindAnonymous:
				anonymous[strings.ToLower(node.Name())] = true
			case node.Is(expressions.TraitFunc) && !structuralKinds[node.Kind()]:
				kinds[node.Kind()] = true
			}
		}
	}
	return kinds, anonymous
}
