package providers

import (
	"fmt"
	"slices"
	"strings"

	"github.com/galileostudio/baleia/internal/connections"
)

// Markdown renders docs/connections.md from the registry. Since ADR 0021 the
// registry is the single source of truth for the static type metadata: access
// modes, category, Trino catalog mappings and status all come from here.
func Markdown(reg *connections.Registry) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("description: \"Every connection provider Baleia supports, from PostgreSQL to Iceberg catalogs " +
		"like Polaris and Nessie: what each is used for and how to configure it.\"\n")
	b.WriteString("---\n\n")
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
		writeFieldTable(&b, p.ConfigSchema())
	}
	return b.String()
}

// writeFieldTable renders a provider's configuration schema.
func writeFieldTable(b *strings.Builder, schema []connections.Field) {
	b.WriteString("| Field | Type | Required | Default | Notes |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, f := range schema {
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
		fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s |\n", f.Key, f.Type, req, def, strings.ReplaceAll(notes, "|", `\|`))
	}
}

// pageStates are the provider states that get a page of their own. planned and
// experimental kinds are not offered to users, and a page for them would
// advertise what Baleia cannot do yet; not_applicable is reserved. A kind that
// is only unsupported until the operator enables it (SQLite needs
// BALEIA_SQLITE_ROOT) is documented.
func hasPage(p connections.Provider) bool {
	switch p.Status().State {
	case connections.StatePlanned, connections.StateExperimental, connections.StateNotApplicable:
		return false
	}
	return true
}

// Pages renders one docs page per documented provider, keyed by kind. They are
// written to docs/connections/<kind>.md, a folder that holds only these files.
// Every sentence comes from the registry (description, roles, access modes,
// configuration schema), so a page cannot claim more than the provider declares.
func Pages(reg *connections.Registry) map[connections.Kind]string {
	pages := map[connections.Kind]string{}
	for _, p := range reg.All() {
		if hasPage(p) {
			pages[p.Kind()] = page(p)
		}
	}
	return pages
}

func page(p connections.Provider) string {
	meta := p.Meta()
	var b strings.Builder
	fmt.Fprintf(&b, "---\nsidebar_label: %s\n", p.Label())
	fmt.Fprintf(&b, "description: \"Connect %s to Baleia. %s See its settings and what Baleia does with it.\"\n---\n\n",
		p.Label(), strings.ReplaceAll(meta.Description, `"`, `\"`))
	fmt.Fprintf(&b, "# Connect %s to Baleia\n\n", p.Label())
	b.WriteString("<!-- Generated from internal/connections/providers. Do not edit by hand:\n")
	b.WriteString("     BALEIA_UPDATE_DOCS=1 go test ./internal/connections/providers/ -->\n\n")
	b.WriteString(meta.Description + "\n\n")

	b.WriteString("## What Baleia does with it\n\n")
	for _, r := range p.Roles() {
		switch r {
		case connections.RoleMetadata:
			b.WriteString("- Reads its catalogs, schemas, tables and columns into Baleia's stored metadata, which the [data explorer](../guides/data.md) browses without querying the source.\n")
		case connections.RoleDirect:
			fmt.Fprintf(&b, "- Runs SQL on it directly from the [workbench](../guides/workbench.md), on its own connection (engine kind `%s`).\n", p.EngineKind())
		case connections.RoleFederated:
			b.WriteString("- Runs SQL as a federated engine, so one query can join across the sources registered with it.\n")
		}
	}
	if slices.Contains(meta.AccessModes, connections.AccessModeFederated) && len(meta.TrinoCatalogTypes) > 0 {
		fmt.Fprintf(&b, "- Sets up and keeps the matching Trino catalog (type `%s`), so Trino can query it.\n",
			strings.Join(meta.TrinoCatalogTypes, "`, `"))
	}
	b.WriteString("\n## Configuration\n\n")
	b.WriteString("Create it under **Connections**. Credentials are sealed with `BALEIA_ENCRYPTION_KEY` before they are stored, and every outbound connection goes through the same [network policy](../adr/0011-direct-connections-security.md).\n\n")
	writeFieldTable(&b, p.ConfigSchema())

	b.WriteString("\n## Next steps\n\n")
	b.WriteString("- [Quick start](../quick-start.md)\n")
	b.WriteString("- [Every connection provider](../connections.md)\n")
	b.WriteString("- [Concepts](../concepts.md)\n")
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
