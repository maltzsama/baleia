package repo

import (
	"testing"

	"github.com/galileostudio/baleia/internal/store/model"
)

// TestAliasReference: an alias shared by two references of one statement cannot
// identify a table, so a stored join on it is skipped instead of paired with
// either reference (#1089).
func TestAliasReference(t *testing.T) {
	t.Parallel()
	orders := model.StatementReference{Alias: "o", TableNameValue: "orders"}
	customers := model.StatementReference{Alias: "o", TableNameValue: "customers"}
	cases := []struct {
		name    string
		aliases map[string][]model.StatementReference
		alias   string
		want    model.StatementReference
		ok      bool
	}{
		{
			name:    "unique alias",
			aliases: map[string][]model.StatementReference{"o": {orders}, "c": {customers}},
			alias:   "O",
			want:    orders,
			ok:      true,
		},
		{
			name:    "alias shared by two references",
			aliases: map[string][]model.StatementReference{"o": {orders, customers}},
			alias:   "o",
			ok:      false,
		},
		{
			name:    "unknown alias",
			aliases: map[string][]model.StatementReference{"c": {customers}},
			alias:   "o",
			ok:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := aliasReference(tc.aliases, tc.alias)
			if ok != tc.ok || (ok && got.TableNameValue != tc.want.TableNameValue) {
				t.Fatalf("aliasReference = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
