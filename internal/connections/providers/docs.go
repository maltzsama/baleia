package providers

import (
	"fmt"
	"strings"

	"github.com/galileostudio/baleia/internal/connections"
)

// Markdown renders docs/connections.md from the registry. Since ADR 0021 the
// registry is the single source of truth for the static type metadata: access
// modes, category, Trino catalog mappings and status all come from here.
func Markdown(reg *connections.Registry) string {
	var b strings.Builder
	b.WriteString("# Connection providers\n\n")
	b.WriteString("<!-- Generated from internal/connections/providers. Do not edit by hand:\n")
	b.WriteString("     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->\n\n")
	b.WriteString("Every connection kind Baleia knows, what it can be used for, and its configuration.\n")
	b.WriteString("See [ADR 0002](adr/0002-connections-engines-catalogs-bindings.md) for the model,\n")
	b.WriteString("[ADR 0011](adr/0011-direct-connections-security.md) for network safety and\n")
	b.WriteString("[ADR 0021](adr/0021-unified-connection-model.md) for access modes and categories.\n\n")
	b.WriteString("*Access modes* lists how the connection reaches its data; *Trino catalog types*\n")
	b.WriteString("lists the internal catalog implementations it is exposed through. Neither is a\n")
	b.WriteString("user-facing connector selection.\n\n")

	b.WriteString("| Kind | Label | Category | Access modes | Roles | Engine kind | Trino catalog types | Status |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
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
		meta := p.Meta()
		modes := make([]string, 0, len(meta.AccessModes))
		for _, m := range meta.AccessModes {
			modes = append(modes, string(m))
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s | %s |\n",
			p.Kind(), p.Label(), dash(string(meta.Category)), joinOrDash(modes),
			joinOrDash(roles), engine, joinOrDash(meta.TrinoCatalogTypes), status)
	}

	for _, p := range reg.All() {
		fmt.Fprintf(&b, "\n## `%s` — %s\n\n", p.Kind(), p.Label())
		if d := p.Meta().Description; d != "" {
			b.WriteString(d + "\n\n")
		}
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

func dash(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

func joinOrDash(v []string) string {
	if len(v) == 0 {
		return "—"
	}
	return strings.Join(v, ", ")
}
