package search

import (
	"sort"
	"testing"
)

func TestCompile(t *testing.T) {
	t.Parallel()
	if !Compile("  ").IsEmpty() {
		t.Error("a blank query is empty")
	}
	if Compile("ab").IsEmpty() {
		t.Error("a query with text is not empty")
	}
}

func TestPattern_Score(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
		text  string
		tier  float64 // the tier the score falls in; 0 for no match
	}{
		{name: "exact in another case", query: "ORDERS", text: "orders", tier: TierExact},
		{name: "exact without accents", query: "regiao", text: "Região", tier: TierExact},
		{name: "prefix", query: "ord", text: "orders", tier: TierPrefix},
		{name: "every word", query: "lifetime customer", text: "customerLifetimeValue", tier: TierTokens},
		{name: "spaced name", query: "order note", text: "order_note", tier: TierTokens},
		{name: "word prefixes", query: "orders mod", text: "Orders by modality", tier: TierTokenPrefix},
		{name: "singular of a plural", query: "sessao", text: "sessões_web", tier: TierTokens},
		{name: "plural of a singular", query: "categories", text: "product_category", tier: TierTokens},
		{name: "contains", query: "rder", text: "orders", tier: TierContains},
		{name: "misspelled", query: "custmer", text: "customer", tier: tierFuzzy},
		{name: "misspelled word of a name", query: "lifetme", text: "customer_lifetime_value", tier: tierFuzzy},
		{name: "half of the words", query: "orders refunds", text: "orders", tier: tierPartial},
		{name: "unrelated", query: "invoice", text: "orders", tier: 0},
		{name: "empty text", query: "orders", text: "", tier: 0},
		{name: "empty query", query: "", text: "orders", tier: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Compile(tt.query).Score(tt.text)
			if tt.tier == 0 {
				if got != 0 {
					t.Errorf("Score(%q, %q) = %v, want no match", tt.query, tt.text, got)
				}
				return
			}
			if got <= 0 || got > tt.tier+tieBreak {
				t.Errorf("Score(%q, %q) = %v, want in tier %v", tt.query, tt.text, got, tt.tier)
			}
			if tt.tier >= TierContains && got < tt.tier {
				t.Errorf("Score(%q, %q) = %v, want at least %v", tt.query, tt.text, got, tt.tier)
			}
		})
	}
}

func TestPattern_ScoreRanksTiers(t *testing.T) {
	t.Parallel()
	p := Compile("order")
	names := []string{"customer_order_history", "orders", "order", "reorder_points", "ordr_x"}
	sort.SliceStable(names, func(i, j int) bool { return p.Score(names[i]) > p.Score(names[j]) })
	want := []string{"order", "orders", "customer_order_history", "reorder_points", "ordr_x"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("ranking = %q, want %q", names, want)
		}
	}
	if p.Score("orders") <= p.Score("orders_archive_2019") {
		t.Error("a shorter name ranks first inside a tier")
	}
}

func TestPattern_Best(t *testing.T) {
	t.Parallel()
	p := Compile("faturados")
	got := p.Best(
		Field{Text: "invoices", Weight: 1},
		Field{Text: "Pedidos de venda já faturados", Weight: 0.6},
	)
	if got <= 0 || got > 0.6*(TierExact+tieBreak) {
		t.Errorf("Best = %v, want a weighted comment match", got)
	}
	if p.Best(Field{Text: "invoices", Weight: 1}) != 0 {
		t.Error("no field matches")
	}
}
