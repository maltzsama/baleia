package policy

import (
	"strings"
	"unicode"

	"github.com/galileostudio/baleia/internal/dialect"
	sqlglot "github.com/maltzsama/sqlglot-go"
	"github.com/maltzsama/sqlglot-go/tokens"
)

// InvokerViews returns statement with SECURITY INVOKER added when it is a Trino
// CREATE [OR REPLACE] VIEW that names no security mode, and statement
// unchanged otherwise (#1152).
//
// Trino makes such a view SECURITY DEFINER and evaluates its body as
// Identity.ofUser(owner): an identity without the caller's extra credentials.
// The SQL editor runs as the user's own identity, which the catalog guard does
// not trust, so the guard refuses that body on every catalog (ADR 0018), and an
// Iceberg REST catalog with per-user identity gets no token for it (ADR 0015).
// A DEFINER view made in the editor is therefore unreadable, while an INVOKER
// view runs as each caller and the catalog manager decides as for any query.
// An explicit SECURITY DEFINER is left as written.
//
// The clause goes before the view's WITH properties or, without them, before
// AS: Trino's order is name, COMMENT, SECURITY, WITH, AS. The statement is
// edited in place rather than regenerated, so the user's text is kept.
func InvokerViews(d dialect.Dialect, statement string) string {
	if d.Name() != dialect.Trino {
		return statement
	}
	name, err := dialect.SqlglotDialect(d)
	if err != nil {
		return statement
	}
	lexemes, err := sqlglot.Tokenize(statement, name)
	if err != nil {
		return statement
	}
	at, ok := viewSecurityInsertion(lexemes)
	if !ok {
		return statement
	}
	runes := []rune(statement)
	clause := "SECURITY INVOKER "
	if at > 0 && !unicode.IsSpace(runes[at-1]) {
		clause = " " + clause
	}
	return string(runes[:at]) + clause + string(runes[at:])
}

// viewSecurityInsertion returns the rune offset where a CREATE VIEW without a
// security mode takes one, and false for any other statement.
func viewSecurityInsertion(lexemes []tokens.Token) (int, bool) {
	i := 0
	if !hasToken(lexemes, i, tokens.CREATE) {
		return 0, false
	}
	i++
	if hasToken(lexemes, i, tokens.OR) && hasToken(lexemes, i+1, tokens.REPLACE) {
		i += 2
	}
	if !hasToken(lexemes, i, tokens.VIEW) {
		// CREATE MATERIALIZED VIEW and every other CREATE.
		return 0, false
	}
	i++
	if hasText(lexemes, i, "IF") && hasText(lexemes, i+1, "NOT") && hasText(lexemes, i+2, "EXISTS") {
		i += 3
	}
	// The view's name: one part, then ".part" for each qualifier. A view may be
	// named "security", so the name is skipped before the clause is looked for.
	i++
	for hasToken(lexemes, i, tokens.DOT) {
		i += 2
	}
	depth := 0
	for _, lexeme := range lexemes[min(i, len(lexemes)):] {
		switch lexeme.TokenType {
		case tokens.L_PAREN:
			depth++
		case tokens.R_PAREN:
			depth--
		case tokens.WITH, tokens.ALIAS:
			if depth == 0 {
				return lexeme.Start, true
			}
		case tokens.VAR:
			if depth == 0 && strings.EqualFold(lexeme.Text, "SECURITY") {
				return 0, false
			}
		}
	}
	return 0, false
}

func hasToken(lexemes []tokens.Token, i int, kind tokens.TokenType) bool {
	return i < len(lexemes) && lexemes[i].TokenType == kind
}

func hasText(lexemes []tokens.Token, i int, text string) bool {
	return i < len(lexemes) && strings.EqualFold(lexemes[i].Text, text)
}
