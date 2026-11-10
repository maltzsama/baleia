package search

import (
	"slices"
	"testing"
)

func TestGlossary_Variants(t *testing.T) {
	t.Parallel()
	g := NewGlossary([][]string{
		{"Receita", "faturamento", "Revenue"},
		{"churn", "cancelamento de clientes"},
		{"x"}, // a set of one has nothing to add
	})
	tests := []struct {
		name     string
		query    string
		variants []string
		added    []string
	}{
		{name: "a word of the query", query: "receita mensal", variants: []string{"faturamento mensal", "revenue mensal"}, added: []string{"faturamento", "revenue"}},
		{name: "case and accents", query: "FATURAMENTO", variants: []string{"receita", "revenue"}, added: []string{"receita", "revenue"}},
		{name: "a phrase member", query: "churn", variants: []string{"cancelamento de clientes"}, added: []string{"cancelamento de clientes"}},
		{name: "the whole query is a phrase member", query: "Cancelamento de Clientes", variants: []string{"churn"}, added: []string{"churn"}},
		{name: "no set applies", query: "orders", variants: []string{}, added: []string{}},
		{name: "a set of one is ignored", query: "x", variants: []string{}, added: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			variants, added := g.Variants(tt.query, nil)
			if !slices.Equal(variants, tt.variants) || !slices.Equal(added, tt.added) {
				t.Errorf("Variants(%q) = %q, %q; want %q, %q", tt.query, variants, added, tt.variants, tt.added)
			}
		})
	}
}

func TestGlossary_VariantsAreBounded(t *testing.T) {
	t.Parallel()
	members := []string{"a0"}
	for i := range 30 {
		members = append(members, "synonym"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	variants, _ := NewGlossary([][]string{members}).Variants("a0 orders", nil)
	if len(variants) != MaxVariants {
		t.Errorf("%d variants, want %d", len(variants), MaxVariants)
	}
}

func TestGlossary_VariantsDropsWhatCannotBeSearched(t *testing.T) {
	t.Parallel()
	g := NewGlossary([][]string{{"orders", "x", "sales"}})
	atLeastTwo := func(q string) bool { return len([]rune(q)) >= 2 }
	variants, added := g.Variants("orders", atLeastTwo)
	if !slices.Equal(variants, []string{"sales"}) || !slices.Equal(added, []string{"sales"}) {
		t.Errorf("Variants = %q, %q; want only the usable synonym reported", variants, added)
	}
}
