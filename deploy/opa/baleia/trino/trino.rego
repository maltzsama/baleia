# METADATA
# title: Baleia data policies (Trino adapter)
# description: |
#   Translates the input of Trino's OPA access control (plugin trino-opa,
#   Trino 484) into baleia.core decisions. Point the plugin at:
#     opa.policy.uri                      /v1/data/baleia/trino/allow
#     opa.policy.batched-uri              /v1/data/baleia/trino/batch
#     opa.policy.row-filters-uri          /v1/data/baleia/trino/row_filters
#     opa.policy.column-masking-uri       /v1/data/baleia/trino/column_mask
#     opa.policy.batch-column-masking-uri /v1/data/baleia/trino/batch_column_masks
#   All five are required: Trino treats an unset row-filters URI as "no
#   filter".
package baleia.trino

import rego.v1

import data.baleia.core

identity := input.context.identity.user

default allow := false

allow if {
	core.allow(identity)
	not side_door
}

# side_door is a statement that reads a policied table's values under another
# name, where no filter or mask applies. Trusted identities are exempt, so
# table health and maintenance keep them.
#   - Metadata tables that carry column values: file and manifest bounds,
#     partition values and statistics, readable metrics. The structural ones
#     ($history, $refs, $snapshots, $properties, $metadata_log_entries) stay
#     readable, so time travel keeps working; $snapshots reveals only the
#     total row count.
#   - Iceberg's table_changes table function, which returns changed rows.
value_metadata_tables := {"files", "entries", "all_files", "all_entries", "partitions", "manifests", "all_manifests"}

side_door if {
	not core.trusted(identity)
	table := input.action.resource.table
	parts := split(table.tableName, "$")
	count(parts) == 2
	parts[1] in value_metadata_tables
	core.policied({"catalog": table.catalogName, "schema": table.schemaName, "name": parts[0]})
}

side_door if {
	not core.trusted(identity)
	input.action.operation == "ExecuteTableFunction"
	function := input.action.resource.function
	function.functionName == "table_changes"
	core.catalog_has_policies(function.catalogName)
}

# batch lists the indexes of input.action.filterResources the identity may
# see. Visibility is the catalog guard's and the catalog manager's decision,
# so it is all of them or, without an active bundle, none.
batch contains index if {
	core.allow(identity)
	some index, _ in input.action.filterResources
}

table_of(resource) := {
	"catalog": resource.catalogName,
	"schema": resource.schemaName,
	"name": resource.tableName,
}

# row_filters is always defined (an empty set means no filter), so a missing
# rule can never be mistaken for "no filter" on a policied table.
row_filters contains {"expression": expression} if {
	expression := core.row_filter(identity, table_of(input.action.resource.table))
}

column_mask := {"expression": expression} if {
	column := input.action.resource.column
	expression := core.column_mask(identity, table_of(column), column.columnName)
}

batch_column_masks contains {"index": index, "viewExpression": {"expression": expression}} if {
	some index, resource in input.action.filterResources
	expression := core.column_mask(identity, table_of(resource.column), resource.column.columnName)
}
