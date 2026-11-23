# Spike policy for Polaris's OPA authorizer (#994 Q8): a table with data
# policies may be loaded, or its credentials vended, only from a Baleia
# session, recognized by the baleia_session principal role that the identity
# provider puts only on tokens issued to Baleia's client. Everything else is
# allowed, so the spike isolates that one rule.
package polaris.authz

import rego.v1

policied_tables := {"orders"}

default allow := false

# Polaris's own root credential (bootstrap). The name alone is not enough: an
# OIDC token whose preferred_username is "root" would match it, so the
# service_admin principal role that only the root principal holds is
# required too.
polaris_root if {
	input.actor.principal == "root"
	"service_admin" in input.actor.roles
}

allow if polaris_root

allow if {
	not polaris_root
	not touches_policied_table
}

allow if {
	touches_policied_table
	"baleia_session" in input.actor.roles
}

# A request touches a policied table when it names one as a target or as a
# secondary (the other table of a rename, for example).
touches_policied_table if {
	some target in input.resource.targets
	target.name in policied_tables
}

touches_policied_table if {
	some secondary in input.resource.secondaries
	secondary.name in policied_tables
}
