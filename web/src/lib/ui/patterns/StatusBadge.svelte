<script lang="ts">
	import Badge from '../Badge.svelte';

	type Tone = 'neutral' | 'info' | 'success' | 'warning' | 'danger';
	type Kind =
		| 'binding'
		| 'execution'
		| 'statement'
		| 'sync'
		| 'reconcile'
		| 'retriever'
		| 'maintenance'
		| 'agent'
		| 'engine';
	type Look = { tone: Tone; label: string; spinning?: boolean };

	let { status, kind, title }: { status: string; kind: Kind; title?: string } = $props();

	const mappings: Record<Kind, Record<string, Look>> = {
		binding: {
			active: { tone: 'success', label: 'Active' },
			provisioning: { tone: 'info', label: 'Provisioning', spinning: true },
			updating: { tone: 'info', label: 'Updating', spinning: true },
			deprovisioning: { tone: 'warning', label: 'Deprovisioning', spinning: true },
			error: { tone: 'danger', label: 'Error' },
			pending: { tone: 'neutral', label: 'Pending' },
			disabled: { tone: 'neutral', label: 'Disabled' }
		},
		execution: {
			pending: { tone: 'neutral', label: 'Pending', spinning: true },
			running: { tone: 'info', label: 'Running', spinning: true },
			finished: { tone: 'success', label: 'Finished' },
			failed: { tone: 'danger', label: 'Failed' },
			cancelled: { tone: 'warning', label: 'Cancelled' }
		},
		statement: {
			pending: { tone: 'neutral', label: 'Pending', spinning: true },
			running: { tone: 'info', label: 'Running', spinning: true },
			finished: { tone: 'success', label: 'Finished' },
			failed: { tone: 'danger', label: 'Failed' },
			cancelled: { tone: 'warning', label: 'Cancelled' },
			skipped: { tone: 'neutral', label: 'Skipped' }
		},
		sync: {
			ok: { tone: 'success', label: 'Synced' },
			running: { tone: 'info', label: 'Syncing', spinning: true },
			failed: { tone: 'danger', label: 'Failed' },
			stale: { tone: 'warning', label: 'Stale' }
		},
		reconcile: {
			ok: { tone: 'success', label: 'In sync' },
			drift: { tone: 'warning', label: 'Drift' },
			failed: { tone: 'danger', label: 'Failed' },
			running: { tone: 'info', label: 'Reconciling', spinning: true }
		},
		retriever: {
			active: { tone: 'success', label: 'Active' },
			paused: { tone: 'neutral', label: 'Paused' },
			error: { tone: 'danger', label: 'Error' }
		},
		maintenance: {
			running: { tone: 'info', label: 'Running', spinning: true },
			finished: { tone: 'success', label: 'Finished' },
			failed: { tone: 'danger', label: 'Failed' },
			scheduled: { tone: 'neutral', label: 'Scheduled' }
		},
		agent: {
			active: { tone: 'success', label: 'Active' },
			expired: { tone: 'warning', label: 'Expired' },
			revoked: { tone: 'danger', label: 'Revoked' },
			pending: { tone: 'neutral', label: 'Pending' }
		},
		engine: {
			ok: { tone: 'success', label: 'Healthy' },
			unreachable: { tone: 'danger', label: 'Unreachable' },
			unknown: { tone: 'neutral', label: 'Unknown' },
			disabled: { tone: 'neutral', label: 'Disabled' }
		}
	};

	const look: Look = $derived(mappings[kind]?.[status] ?? { tone: 'neutral', label: status });
</script>

<span title={title ?? look.label}>
	<Badge tone={look.tone}>
		{#if look.spinning}
			<span
				class="h-2.5 w-2.5 animate-spin rounded-full border border-current border-t-transparent"
				aria-hidden="true"
			></span>
		{/if}
		{look.label}
	</Badge>
</span>
