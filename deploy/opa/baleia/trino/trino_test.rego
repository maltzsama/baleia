package baleia.trino_test

import rego.v1

import data.baleia.trino

# Inputs recorded from Trino 484 by tests/opa (OPA_SPIKE_RECORD=1), each with
# the rule it was sent to and the result OPA returned with the recorded bundle
# (testdata/bundle). Re-evaluating them pins the adapter to the real plugin
# contract: a Trino upgrade that changes the input shows up here.
deployment := {"trusted_identities": ["baleia_admin", "baleia"]}

normalize(x) := sort(x) if is_set(x)

normalize(x) := sort(x) if is_array(x)

normalize(x) := x if {
	not is_set(x)
	not is_array(x)
}

evaluate(example) := normalize(result) if {
	result := trino[example.rule] with input as example.input
		with data.baleia.bundle as data.testdata.bundle
		with data.baleia_deployment as deployment
}

defined := {name: e | some name, e in data.testdata.trino484; e.result != null}

undefined := {name: e | some name, e in data.testdata.trino484; e.result == null}

test_recorded_inputs_reproduce_recorded_results if {
	count(defined) > 0
	every example in defined {
		evaluate(example) == normalize(example.result)
	}
}

test_recorded_undefined_results_stay_undefined if {
	every example in undefined {
		not evaluate(example)
	}
}

test_policied_table_always_gets_a_filter if {
	example := data.testdata.trino484.row_filters_GetRowFilters
	trino.row_filters == {{"expression": "((region = 'BR')) AND (status <> 'void')"}} with input as example.input
		with data.baleia.bundle as data.testdata.bundle
		with data.baleia_deployment as deployment
}

test_without_a_bundle_the_user_is_denied_and_filtered_to_nothing if {
	example := data.testdata.trino484.row_filters_GetRowFilters
	not trino.allow with input as data.testdata.trino484.allow_SelectFromColumns.input
		with data.baleia_deployment as deployment
	trino.batch == set() with input as data.testdata.trino484.batch_FilterTables.input
		with data.baleia_deployment as deployment
	trino.row_filters == {{"expression": "false"}} with input as example.input
		with data.baleia_deployment as deployment
}

test_trusted_identity_bypasses_filters_and_masks if {
	filters := json.patch(data.testdata.trino484.row_filters_GetRowFilters.input, [{"op": "replace", "path": "/context/identity/user", "value": "baleia"}])
	masks := json.patch(data.testdata.trino484.batch_column_masks_GetColumnMask.input, [{"op": "replace", "path": "/context/identity/user", "value": "baleia_admin"}])
	trino.row_filters == set() with input as filters
		with data.baleia.bundle as data.testdata.bundle
		with data.baleia_deployment as deployment
	trino.batch_column_masks == set() with input as masks
		with data.baleia.bundle as data.testdata.bundle
		with data.baleia_deployment as deployment
}

test_metadata_tables_of_a_policied_table_are_denied if {
	select := data.testdata.trino484.allow_SelectFromColumns.input
	files := json.patch(select, [{"op": "replace", "path": "/action/resource/table/tableName", "value": "orders_contract$files"}])
	other := json.patch(select, [{"op": "replace", "path": "/action/resource/table/tableName", "value": "items$files"}])
	admin := json.patch(files, [{"op": "replace", "path": "/context/identity/user", "value": "baleia_admin"}])
	not trino.allow with input as files
		with data.baleia.bundle as data.testdata.bundle
		with data.baleia_deployment as deployment
	trino.allow with input as other
		with data.baleia.bundle as data.testdata.bundle
		with data.baleia_deployment as deployment
	trino.allow with input as admin
		with data.baleia.bundle as data.testdata.bundle
		with data.baleia_deployment as deployment
}

test_table_changes_is_denied_in_a_policied_catalog if {
	call := {
		"action": {"operation": "ExecuteTableFunction", "resource": {"function": {"catalogName": "iceberg", "schemaName": "system", "functionName": "table_changes"}}},
		"context": {"identity": {"user": "u_ana", "groups": []}, "softwareStack": {"trinoVersion": "484"}},
	}
	not trino.allow with input as call
		with data.baleia.bundle as data.testdata.bundle
		with data.baleia_deployment as deployment
	trino.allow with input as json.patch(call, [{"op": "replace", "path": "/action/resource/function/catalogName", "value": "tpch"}])
		with data.baleia.bundle as data.testdata.bundle
		with data.baleia_deployment as deployment
}
