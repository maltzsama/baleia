// Error catalog (#105 / FD7): every API, engine, lifecycle and auth code maps
// to a plain-language title, explanation and remediation. The UI never shows a
// raw engine message as the headline.

export type ErrorEntry = {
	title: string;
	explanation: string;
	remediation?: string;
	docsAnchor?: string;
};

export type DescribedError = ErrorEntry & {
	code: string;
	requestId?: string;
	native?: string;
};

export const errorCatalog: Record<string, ErrorEntry> = {
	// API
	unauthorized: {
		title: 'Not signed in',
		explanation: 'Your session is missing or has expired.',
		remediation: 'Sign in again and retry.'
	},
	forbidden: {
		title: 'Not allowed',
		explanation: 'You do not have permission to perform this action.',
		remediation: 'Ask a workspace owner or administrator for access.'
	},
	not_found: {
		title: 'Not found',
		explanation: 'The resource no longer exists or you cannot see it.',
		remediation: 'Refresh the page and check the resource was not deleted.'
	},
	conflict: {
		title: 'Conflict',
		explanation: 'The resource changed while you were editing it.',
		remediation: 'Reload the latest version and apply your change again.'
	},
	validation_error: {
		title: 'Invalid input',
		explanation: 'One or more fields are invalid.',
		remediation: 'Fix the highlighted fields and retry.'
	},
	invalid_definition: {
		title: 'The query cannot be compiled',
		explanation:
			'The model or the explore query uses a field, join or feature the compiler cannot resolve for this engine.',
		remediation: 'Check the highlighted fields, the model joins and the engine, then retry.'
	},
	rate_limited: {
		title: 'Too many requests',
		explanation: 'You have made too many requests in a short period.',
		remediation: 'Wait for the retry window before trying again.'
	},
	service_unavailable: {
		title: 'Service unavailable',
		explanation: 'A dependency is not reachable right now.',
		remediation: 'Retry in a moment; if it persists, check the system status page.'
	},
	provider_unsupported: {
		title: 'Provider not supported',
		explanation: 'This connection kind is not supported by the server.',
		remediation: 'Choose a different provider.'
	},
	connection_denied_network: {
		title: 'Connection blocked',
		explanation: 'The server refused to dial that host.',
		remediation: 'Check the host allow-list and the connection URL.'
	},
	target_required: {
		title: 'Choose a target',
		explanation: 'This workspace has more than one place to run queries.',
		remediation: 'Pick an engine, catalog and schema before running.'
	},
	engine_required: {
		title: 'Choose an engine',
		explanation: 'The selected catalog is not available on an enabled engine.',
		remediation: 'Pick another target or enable an engine for this catalog.'
	},
	target_unavailable: {
		title: 'Target unavailable',
		explanation: 'The selected engine, catalog or binding is no longer available.',
		remediation: 'Pick another target or ask an administrator to restore the binding.'
	},
	read_only_unenforceable: {
		title: 'Read-only cannot be enforced',
		explanation: 'The engine cannot enforce read-only sessions for this connection.',
		remediation: 'Grant write instead, or assert read-only credentials on the connection.'
	},
	name_conflict: {
		title: 'Name already in use',
		explanation: 'Another item in this workspace already has that name.',
		remediation: 'Choose a different name.'
	},
	slug_conflict_other_space: {
		title: 'Slug used in another space',
		explanation: 'Slugs are unique per workspace; this one belongs to another space.',
		remediation: 'Rename the slug in the bundle.'
	},
	property_not_overridable: {
		title: 'Property cannot be overridden',
		explanation: 'Baleia sets this property itself.',
		remediation: 'Remove the property or change it in the connection settings.'
	},
	invalid_time_range: {
		title: 'Invalid time range',
		explanation: 'The selected window is missing or larger than 93 days.',
		remediation: 'Choose a range of at most 93 days.'
	},
	not_supported: {
		title: 'Not supported',
		explanation: 'This operation is not available for the selected resource.',
		remediation: 'Choose a supported option.'
	},
	version_conflict: {
		title: 'Changed elsewhere',
		explanation: 'Someone else saved a newer version while you were editing.',
		remediation: 'Compare the versions, then overwrite or save a copy.'
	},

	// Connection test
	unreachable: {
		title: 'Host unreachable',
		explanation: 'Baleia could not reach the configured host.',
		remediation: 'Check the host, port and network access.'
	},
	tls: {
		title: 'TLS error',
		explanation: 'The TLS handshake failed.',
		remediation: 'Check the certificate and the TLS settings.'
	},
	auth_failed: {
		title: 'Authentication failed',
		explanation: 'The provider rejected the supplied credentials.',
		remediation: 'Check the username, password or client credentials.'
	},
	timeout: {
		title: 'Timed out',
		explanation: 'The connection test did not finish in time.',
		remediation: 'Check the network and retry.'
	},
	engine_unavailable: {
		title: 'Engine unavailable',
		explanation: 'The engine did not answer the test.',
		remediation: 'Check engine health and retry.'
	},
	internal: {
		title: 'Internal error',
		explanation: 'Baleia hit an unexpected error.',
		remediation: 'Retry; if it persists, check the API logs with the request id.'
	},

	// Lifecycle
	plugin_not_confirmed: {
		title: 'Catalog plugin not confirmed',
		explanation: 'The Trino catalog plugin has not reported back yet.',
		remediation: 'Wait for the next reconciliation or check the plugin version.'
	},
	secret_mismatch: {
		title: 'Secret mismatch',
		explanation: 'The stored secret does not match the configured value.',
		remediation: 'Update the credentials on the connection.'
	},
	credentials_unreadable: {
		title: 'Credentials unreadable',
		explanation: 'Baleia cannot decrypt the stored credentials.',
		remediation: 'Re-enter the credentials; the encryption key may have changed.'
	},
	version_mismatch: {
		title: 'Version mismatch',
		explanation: 'The installed component version does not match the expected one.',
		remediation: 'Upgrade the component or update the configured version.'
	},

	// Auth
	invalid_credentials: {
		title: 'Invalid credentials',
		explanation: 'The e-mail and password do not match an account.',
		remediation: 'Check the values and try again.'
	},
	not_provisioned: {
		title: 'Account not provisioned',
		explanation: 'Your identity provider authenticated you, but Baleia has no account for you.',
		remediation: 'Ask an administrator to invite you.'
	},
	account_suspended: {
		title: 'Account suspended',
		explanation: 'An administrator suspended this account.',
		remediation: 'Contact an administrator to reinstate it.'
	},
	idp_unavailable: {
		title: 'Identity provider unavailable',
		explanation: 'The identity provider did not answer.',
		remediation: 'Retry; if it persists, contact your administrator.'
	},
	sso_failed: {
		title: 'Single sign-on failed',
		explanation: 'The identity provider could not complete the sign-in.',
		remediation: 'Try again; if it persists, contact your administrator.'
	},
	network_error: {
		title: 'Connection error',
		explanation: 'Baleia could not reach the server.',
		remediation: 'Check that the server is running and retry.'
	},
	agent_token_not_allowed: {
		title: 'Agent token not allowed',
		explanation: 'This token cannot be used for this operation.',
		remediation: 'Use a human session or an allowed agent scope.'
	},

	// Agent gate reasons
	policy: {
		title: 'Blocked by policy',
		explanation: 'The agent capability policy denies this action.',
		remediation: 'Ask the agent owner to widen the policy.'
	},
	scope: {
		title: 'Outside the agent scope',
		explanation: 'The action is outside what this agent token may do.',
		remediation: 'Use a narrower target or a different token.'
	},
	rate: {
		title: 'Agent rate limited',
		explanation: 'The agent exceeded its request budget.',
		remediation: 'Wait for the window to reset.'
	},
	quota: {
		title: 'Agent quota reached',
		explanation: 'The agent reached its usage quota.',
		remediation: 'Raise the quota or wait for the next period.'
	},
	budget: {
		title: 'Agent budget reached',
		explanation: 'The agent reached its configured budget.',
		remediation: 'Raise the budget in the agent settings.'
	},
	statement: {
		title: 'Statement rejected',
		explanation: 'The statement is not allowed for this agent.',
		remediation: 'Use a read-only statement.'
	},

	// Attention center (#268). The catalog key is `attention.<kind>`.
	'attention.binding_error': {
		title: 'Catalog binding error',
		explanation: 'A catalog binding needs attention.',
		remediation: 'Open the catalog bindings tab and retry the binding.'
	},
	'attention.metadata_sync_failed': {
		title: 'Metadata sync failed',
		explanation: 'The last metadata sync for this catalog failed.',
		remediation: 'Open the catalog and retry the sync.'
	},
	'attention.metadata_stale': {
		title: 'Metadata is stale',
		explanation: 'This catalog has not synced within its expected interval.',
		remediation: 'Open the catalog and run a metadata sync.'
	},
	'attention.engine_unreachable': {
		title: 'Engine unreachable',
		explanation: 'Baleia cannot reach this engine.',
		remediation: 'Check the engine health and credentials.'
	},
	'attention.retriever_error': {
		title: 'Retriever failed',
		explanation: 'This retriever stopped with an error.',
		remediation: 'Open the retriever and review the last run.'
	},
	'attention.reconcile_drift': {
		title: 'Catalog drift detected',
		explanation: 'The last reconcile found changes that need to be applied.',
		remediation: 'Open the engine reconcile tab and apply the changes.'
	},
	'attention.schedule_missing': {
		title: 'Scheduled job missing',
		explanation: 'A recurring Temporal schedule is missing or paused.',
		remediation: 'Open the system status page and restore the schedule.'
	},
	'attention.audit_export_lagging': {
		title: 'Audit export is lagging',
		explanation: 'Audit events are not reaching the export sink in time.',
		remediation: 'Open the system status page and check the export lag.'
	},
	'attention.agent_token_expiring': {
		title: 'Agent token expiring',
		explanation: 'An agent token expires within seven days.',
		remediation: 'Open the agent and rotate its token.'
	}
};

export function describeError(error: unknown): DescribedError {
	let code = 'internal';
	let requestId: string | undefined;
	let native: string | undefined;

	if (typeof error === 'string') {
		code = error;
	} else if (error && typeof error === 'object') {
		const value = error as Record<string, unknown>;
		if (typeof value.code === 'string') code = value.code;
		if (typeof value.request_id === 'string') requestId = value.request_id;
		if (typeof value.error_native === 'string') native = value.error_native;
	}

	const entry = errorCatalog[code] ?? {
		title: 'Unexpected error',
		explanation: 'Baleia could not classify this error.',
		remediation: 'Retry; if it persists, contact an administrator.'
	};

	return { code, ...entry, requestId, native };
}
