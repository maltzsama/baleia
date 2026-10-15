package providers

import (
	"fmt"
	"github.com/galileostudio/baleia/internal/bindings"
	"strings"

	"github.com/galileostudio/baleia/internal/connections"
)

// Markdown renders docs/connections.md from the registry.
func Markdown(reg *connections.Registry, binders ...bindings.Binder) string {
	var b strings.Builder
	b.WriteString("# Connection providers\n\n")
	b.WriteString("<!-- Generated from internal/connections/providers. Do not edit by hand:\n")
	b.WriteString("     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->\n\n")
	b.WriteString("Every connection kind Baleia knows, what it can be used for, and its configuration.\n")
	b.WriteString("See [ADR 0002](adr/0002-connections-engines-catalogs-bindings.md) for the model and\n")
	b.WriteString("[ADR 0011](adr/0011-direct-connections-security.md) for network safety.\n\n")
	b.WriteString("*Bindable into* lists the federated engines whose binder can bind the kind, with the\n")
	b.WriteString("connectors it may use (the first is the default; `internal/bindings`).\n\n")

	b.WriteString("| Kind | Label | Family | Roles | Engine kind | Bindable into | Status |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, p := range reg.All() {
		roles := make([]string, 0, len(p.Roles()))
		for _, r := range p.Roles() {
			roles = append(roles, string(r))
		}
		engine := p.EngineKind()
		if engine == "" {
			engine = "—"
		}
		status := string(p.Status().State)
		if r := p.Status().Reason; r != "" {
			status += " (" + r + ")"
		}
		var bindable []string
		for _, bd := range binders {
			if connectors, ok := bd.Supports()[p.Kind()]; ok {
				bindable = append(bindable, bd.EngineKind()+" ("+strings.Join(connectors, ", ")+")")
			}
		}
		into := "—"
		if len(bindable) > 0 {
			into = strings.Join(bindable, "; ")
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s |\n", p.Kind(), p.Label(), p.Family(), strings.Join(roles, ", "), engine, into, status)
	}

	for _, p := range reg.All() {
		fmt.Fprintf(&b, "\n## `%s` — %s\n\n", p.Kind(), p.Label())
		b.WriteString("| Field | Type | Required | Default | Notes |\n")
		b.WriteString("|---|---|---|---|---|\n")
		for _, f := range p.ConfigSchema() {
			req := ""
			if f.Required {
				req = "yes"
			}
			notes := f.Help
			if len(f.Options) > 0 {
				opts := "one of `" + strings.Join(f.Options, "`, `") + "`"
				if notes != "" {
					notes = opts + ". " + notes
				} else {
					notes = opts
				}
			}
			def := ""
			if f.Default != "" {
				def = "`" + f.Default + "`"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", f.Key, f.Type, req, def, strings.ReplaceAll(notes, "|", `\|`))
		}
	}
	return b.String()
}
