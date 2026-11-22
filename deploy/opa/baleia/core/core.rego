# METADATA
# title: Baleia data policies (PEP-neutral core)
# description: |
#   Decides row filters and column masks over Baleia's own terms: a subject
#   (the engine identity), a table and a column. It reads only the data bundle
#   that Baleia publishes under data.baleia.bundle and the deployment's trusted
#   identities under data.baleia_deployment. Every policy decision belongs to
#   the user; this package fixes only the semantics, copied from PostgreSQL
#   row-level security:
#     - permissive row policies are OR-ed, restrictive ones are AND-ed;
#     - a table with any row policy is default-deny;
#     - the matching column mask with the highest priority wins;
#     - trusted identities are never filtered or masked.
#   Enforcement points (Trino today) reach it through a thin adapter package.
package baleia.core

import rego.v1

# The bundle data contract this package reads. A bundle with another
# format_version is not active, so user identities are denied.
format_version := 1

bundle := data.baleia.bundle

# trusted(identity) is true for the deployment's admin and service identities
# (Trino's baleia.admin-users and baleia.service-users). They come from the
# deployment configuration, never from the data bundle.
trusted(identity) if identity in data.baleia_deployment.trusted_identities

# active is true while a supported data bundle revision is loaded. It is
# undefined when no bundle was ever activated, when the bundle is invalid and
# when the persisted copy was lost.
active if {
	bundle.format_version == format_version
	is_string(bundle.revision)
	bundle.revision != ""
}

# allow(identity) is the only coarse decision: data policies are not a second
# RBAC. Trusted identities always pass; user identities pass only while a
# bundle is active. Catalog and table access stay with the catalog guard and
# the catalog manager.
allow(identity) if trusted(identity)

allow(identity) if active

# subject(identity) is the identity's entry in the bundle's subject map, or an
# empty object for an identity Baleia does not know (it then matches only
# "everyone" references).
subject(identity) := object.get(bundle, ["subjects", identity], {})

# named(identity, ref) matches a reference that names a user, team,
# identity-provider group or service account.
named(identity, ref) if {
	is_string(ref.user)
	ref.user == subject(identity).user
}

named(identity, ref) if {
	is_string(ref.team)
	ref.team in object.get(subject(identity), "teams", [])
}

named(identity, ref) if {
	is_string(ref.group)
	ref.group in object.get(subject(identity), "groups", [])
}

named(identity, ref) if {
	is_string(ref.service_account)
	ref.service_account == subject(identity).service_account
}

# matches(identity, ref) adds "everyone except <named references>".
matches(identity, ref) if named(identity, ref)

matches(identity, ref) if {
	ref.everyone == true
	not excepted(identity, ref)
}

excepted(identity, ref) if {
	some other in object.get(ref, "except", [])
	named(identity, other)
}

applies(identity, policy) if {
	some ref in policy.to
	matches(identity, ref)
}

table_entry(table) := object.get(bundle, ["tables", table.catalog, table.schema, table.name], {})

row_policies(table) := object.get(table_entry(table), "row_policies", [])

column_masks(table, column) := object.get(table_entry(table), ["column_masks", column], [])

# policied(table) is true when the table has any row policy or column mask.
policied(table) if count(row_policies(table)) > 0

policied(table) if count(object.get(table_entry(table), "column_masks", {})) > 0

# catalog_has_policies(catalog) is true when any table of the catalog is
# policied.
catalog_has_policies(catalog) if {
	some schema, tables in object.get(bundle, ["tables", catalog], {})
	some name, _ in tables
	policied({"catalog": catalog, "schema": schema, "name": name})
}

valid_mode(policy) if policy.mode in {"permissive", "restrictive"}

wrap(expression) := sprintf("(%s)", [expression])

# row_filter(identity, table) is the single boolean expression the engine must
# apply to the table for this identity, or undefined when the table has no row
# policy. The expression is built here, (p1 OR p2 ...) AND r1 AND r2 ..., so
# the engine's own combination of several filters never changes the meaning.
row_filter(identity, table) := "false" if {
	not trusted(identity)
	not active
}

row_filter(identity, table) := "false" if {
	not trusted(identity)
	active
	some policy in row_policies(table)
	not valid_mode(policy)
}

row_filter(identity, table) := expression if {
	not trusted(identity)
	active
	policies := row_policies(table)
	count(policies) > 0
	every policy in policies {
		valid_mode(policy)
	}
	permissive := [wrap(p.using) | some p in policies; p.mode == "permissive"; applies(identity, p)]
	restrictive := [wrap(p.using) | some p in policies; p.mode == "restrictive"; applies(identity, p)]
	expression := concat(" AND ", array.concat([grant(permissive)], restrictive))
}

grant(permissive) := "false" if count(permissive) == 0

grant(permissive) := wrap(concat(" OR ", permissive)) if count(permissive) > 0

# column_mask(identity, table, column) is the mask expression for the column,
# or undefined when no mask matches (the column is then returned unmasked).
# Priorities are unique within a column; a bundle that breaks this makes the
# function conflict, and the engine fails the query instead of picking one.
column_mask(identity, table, column) := mask.expression if {
	not trusted(identity)
	active
	matching := [m | some m in column_masks(table, column); applies(identity, m)]
	count(matching) > 0
	top := max([m.priority | some m in matching])
	some mask in matching
	mask.priority == top
}
