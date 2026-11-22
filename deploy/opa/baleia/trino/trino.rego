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

allow if core.allow(identity)

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
