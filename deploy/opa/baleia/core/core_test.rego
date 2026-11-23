package baleia.core_test

import rego.v1

import data.baleia.core

deployment := {"trusted_identities": ["baleia_admin", "baleia"]}

orders := {"catalog": "lake", "schema": "sales", "name": "orders"}

bundle := {
	"format_version": 1,
	"revision": "r1",
	"not_after": "2200-01-01T00:00:00Z",
	"subjects": {
		"u_ana": {"user": "ana", "teams": ["br"], "groups": ["analysts"]},
		"u_bob": {"user": "bob", "teams": ["us"], "groups": []},
		"u_cid": {"user": "cid", "teams": ["br", "us"], "groups": ["auditors"]},
		"sa_etl": {"service_account": "etl", "teams": []},
	},
	"tables": {"lake": {"sales": {"orders": {
		"row_policies": [
			{"name": "br", "mode": "permissive", "to": [{"team": "br"}], "using": "region = 'BR'"},
			{"name": "us", "mode": "permissive", "to": [{"team": "us"}], "using": "region = 'US'"},
			{"name": "open", "mode": "restrictive", "to": [{"everyone": true, "except": [{"group": "auditors"}]}], "using": "status <> 'void'"},
		],
		"column_masks": {"email": [
			{"name": "hide", "priority": 1, "to": [{"everyone": true}], "expression": "'***'"},
			{"name": "analysts", "priority": 5, "to": [{"group": "analysts"}], "expression": "substr(email, 1, 2) || '***'"},
			{"name": "auditors", "priority": 9, "to": [{"group": "auditors"}], "expression": "email"},
		]},
	}}}},
}

filter(identity) := expression if {
	expression := core.row_filter(identity, orders) with data.baleia.bundle as bundle
		with data.baleia_deployment as deployment
}

mask(identity, column) := expression if {
	expression := core.column_mask(identity, orders, column) with data.baleia.bundle as bundle
		with data.baleia_deployment as deployment
}

test_permissive_policies_are_ored if {
	filter("u_cid") == "((region = 'BR') OR (region = 'US'))"
}

test_restrictive_policies_are_anded if {
	filter("u_ana") == "((region = 'BR')) AND (status <> 'void')"
}

test_no_permissive_match_is_default_deny if {
	filter("sa_etl") == "false AND (status <> 'void')"
}

test_unknown_identity_is_default_deny if {
	filter("u_stranger") == "false AND (status <> 'void')"
}

test_table_without_policies_has_no_filter if {
	not core.row_filter("u_ana", {"catalog": "lake", "schema": "sales", "name": "items"}) with data.baleia.bundle as bundle
		with data.baleia_deployment as deployment
}

test_trusted_identity_is_never_filtered if {
	not filter("baleia_admin")
	not filter("baleia")
}

test_without_bundle_users_get_false if {
	core.row_filter("u_ana", orders) == "false" with data.baleia_deployment as deployment
}

test_unknown_format_is_not_active if {
	not core.active with data.baleia.bundle as object.union(bundle, {"format_version": 2})
	core.row_filter("u_ana", orders) == "false" with data.baleia.bundle as object.union(bundle, {"format_version": 2})
		with data.baleia_deployment as deployment
}

test_invalid_mode_denies_every_row if {
	broken := json.patch(bundle, [{"op": "replace", "path": "/tables/lake/sales/orders/row_policies/0/mode", "value": "maybe"}])
	core.row_filter("u_ana", orders) == "false" with data.baleia.bundle as broken
		with data.baleia_deployment as deployment
}

test_highest_priority_mask_wins if {
	mask("u_ana", "email") == "substr(email, 1, 2) || '***'"
	mask("u_cid", "email") == "email"
	mask("u_bob", "email") == "'***'"
}

test_column_without_masks_is_unmasked if {
	not mask("u_ana", "region")
}

test_trusted_identity_is_never_masked if {
	not mask("baleia_admin", "email")
}

test_allow if {
	core.allow("u_ana") with data.baleia.bundle as bundle with data.baleia_deployment as deployment
	core.allow("baleia_admin") with data.baleia_deployment as deployment
	not core.allow("u_ana") with data.baleia_deployment as deployment
}

test_expired_lease_is_not_active if {
	expired := object.union(bundle, {"not_after": "2000-01-01T00:00:00Z"})
	not core.active with data.baleia.bundle as expired
	core.row_filter("u_ana", orders) == "false" with data.baleia.bundle as expired
		with data.baleia_deployment as deployment
	not core.allow("u_ana") with data.baleia.bundle as expired
		with data.baleia_deployment as deployment
	core.allow("baleia") with data.baleia.bundle as expired
		with data.baleia_deployment as deployment
}

test_expired_lease_nulls_masked_columns if {
	expired := object.union(bundle, {"not_after": "2000-01-01T00:00:00Z"})

	# A mask-only table: no row policy would hide its rows.
	masks_only := object.union(expired, {"tables": {"lake": {"sales": {"orders": {"row_policies": []}}}}})
	core.column_mask("u_cid", orders, "email") == "NULL" with data.baleia.bundle as masks_only
		with data.baleia_deployment as deployment
	not core.column_mask("u_cid", orders, "region") with data.baleia.bundle as masks_only
		with data.baleia_deployment as deployment
	not core.column_mask("u_cid", {"catalog": "lake", "schema": "sales", "name": "other"}, "email") with data.baleia.bundle as masks_only
		with data.baleia_deployment as deployment
	not core.column_mask("baleia", orders, "email") with data.baleia.bundle as masks_only
		with data.baleia_deployment as deployment
}

test_missing_lease_is_not_active if {
	not core.active with data.baleia.bundle as object.remove(bundle, ["not_after"])
}
