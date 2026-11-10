package search

import (
	"slices"
	"testing"
)

func TestFold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "case", input: "Orders", expected: "orders"},
		{name: "accents", input: "Região Comercial", expected: "regiao comercial"},
		{name: "cedilla and tilde", input: "AÇÃO", expected: "acao"},
		{name: "letters without a mark", input: "Straße Øl", expected: "strasse ol"},
		{name: "whitespace", input: "  sales \t  by\nmonth ", expected: "sales by month"},
		{name: "decomposed input", input: "região", expected: "regiao"},
		{name: "empty", input: "   ", expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Fold(tt.input); got != tt.expected {
				t.Errorf("Fold(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{name: "snake case", input: "customer_lifetime_value", expected: []string{"customer", "lifetime", "value"}},
		{name: "camel case", input: "customerLifetimeValue", expected: []string{"customer", "lifetime", "value"}},
		{name: "acronym", input: "HTTPServer", expected: []string{"http", "server"}},
		{name: "dotted", input: "sales.orders_2024", expected: []string{"sales", "orders", "2024"}},
		{name: "accented word stays whole", input: "vendas_por_região", expected: []string{"vendas", "por", "regiao"}},
		{name: "prose", input: "Pedidos de venda, já faturados.", expected: []string{"pedidos", "de", "venda", "ja", "faturados"}},
		{name: "upper case word", input: "ORDERS", expected: []string{"orders"}},
		{name: "nothing", input: " _-. ", expected: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Tokens(tt.input); !slices.Equal(got, tt.expected) {
				t.Errorf("Tokens(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSimilarity(t *testing.T) {
	t.Parallel()
	if got := Similarity("orders", "ORDERS"); got != 1 {
		t.Errorf("identical folded texts = %v, want 1", got)
	}
	if got := Similarity("", "orders"); got != 0 {
		t.Errorf("empty side = %v, want 0", got)
	}
	near, far := Similarity("orders", "ordrs"), Similarity("orders", "customers")
	if near <= far {
		t.Errorf("misspelling %v must be closer than another word %v", near, far)
	}
}

func TestSingulars(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string // one of the singulars
	}{
		{name: "portuguese oes", input: "sessoes", expected: "sessao"},
		{name: "portuguese res", input: "valores", expected: "valor"},
		{name: "portuguese ais", input: "canais", expected: "canal"},
		{name: "english s", input: "stores", expected: "store"},
		{name: "english ies", input: "categories", expected: "category"},
		{name: "itself", input: "order", expected: "order"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := singulars(tt.input); !slices.Contains(got, tt.expected) {
				t.Errorf("singulars(%q) = %q, want it to hold %q", tt.input, got, tt.expected)
			}
		})
	}
	if got := singulars("bus"); len(got) != 1 {
		t.Errorf("a short word has no other form: %q", got)
	}
}
