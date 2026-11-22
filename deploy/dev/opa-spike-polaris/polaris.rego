# Spike policy for Polaris's OPA authorizer (#994 Q8): a table with data
# policies may be loaded, or its credentials vended, only from a Baleia
# session, recognized by the baleia_session principal role that the identity
# provider puts only on tokens issued to Baleia's client. Everything else is
# allowed, so the spike isolates that one rule.
package polaris.authz

import rego.v1

policied_tables := {"orders"}

default allow := false

allow if input.actor.principal == "root"

allow if {
	input.actor.principal != "root"
	not touches_policied_table
}

allow if {
	touches_policied_table
	"baleia_session" in input.actor.roles
}

touches_policied_table if {
	some target in input.resource.targets
	target.name in policied_tables
}
