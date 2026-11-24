// Package viewsql resolves what a Trino view reads, for the data policy
// checks over SECURITY DEFINER views (ADR 0028).
package viewsql

import (
	"strings"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/datapolicy"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/sqlref"
)

var trinoSQL = dialect.NewRegistry().MustGet(dialect.Trino)

// Resolve returns v, held by the catalog named catalogs on the engines, with
// the tables and views its SQL reads resolved against its default catalog and
// schema. A view without a default catalog resolves against the catalog that
// holds it. SQL that cannot be parsed marks the view unreadable: the checks
// then take it to read every policied table.
func Resolve(catalogs []string, v connections.View) datapolicy.CatalogView {
	out := datapolicy.CatalogView{Catalogs: catalogs, View: v}
	if _, err := dialect.ParseStatement(trinoSQL, v.SQL); err != nil {
		out.Unreadable = true
		return out
	}
	defaultSchema := strings.Join(v.DefaultSchema, ".")
	if defaultSchema == "" {
		defaultSchema = strings.Join(v.Namespace, ".")
	}
	defaults := catalogs
	if v.DefaultCatalog != "" {
		defaults = []string{v.DefaultCatalog}
	}
	ref := sqlref.ExtractResolved(trinoSQL, defaultSchema, v.SQL)
	for _, t := range ref.Tables {
		switch len(t.Path) {
		case 3:
			out.Reads = append(out.Reads, datapolicy.NewTableName(t.Path[0], t.Path[1], t.Path[2]))
		case 2:
			for _, catalog := range defaults {
				out.Reads = append(out.Reads, datapolicy.NewTableName(catalog, t.Path[0], t.Path[1]))
			}
		case 1:
			for _, catalog := range defaults {
				out.Reads = append(out.Reads, datapolicy.NewTableName(catalog, defaultSchema, t.Path[0]))
			}
		}
	}
	return out
}
