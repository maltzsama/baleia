<script lang="ts">
	import CodeBlock from './CodeBlock.svelte';
	import ConfirmDialog from './ConfirmDialog.svelte';
	import CopyButton from './CopyButton.svelte';
	import DataTable from './DataTable.svelte';
	import DiffView from './DiffView.svelte';
	import Duration from './Duration.svelte';
	import EngineBadge from './EngineBadge.svelte';
	import ErrorPanel from './ErrorPanel.svelte';
	import FilterBar from './FilterBar.svelte';
	import KeyValueEditor from './KeyValueEditor.svelte';
	import MarkdownEditor from './MarkdownEditor.svelte';
	import MarkdownView from './MarkdownView.svelte';
	import OneTimeSecret from './OneTimeSecret.svelte';
	import RelativeTime from './RelativeTime.svelte';
	import StateTimeline from './StateTimeline.svelte';
	import StatusBadge from './StatusBadge.svelte';

	let kv = $state({ region: 'eu-west-1', endpoint: 'https://s3.example' });
	let markdown = $state('# Orders\n\nThe table stores **one row per order**.');
	let filters = $state<Record<string, string | string[] | boolean>>({ status: 'active' });
	let secretOpen = $state(false);
	let confirmOpen = $state(false);
</script>

<div class="space-y-8 p-6">
	<h1 class="text-xl font-semibold">UI patterns</h1>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">Status badges</h2>
		<div class="flex flex-wrap gap-2">
			{#each ['active', 'provisioning', 'error'] as status (status)}
				<StatusBadge kind="binding" {status} />
			{/each}
			{#each ['running', 'finished', 'failed', 'skipped'] as status (status)}
				<StatusBadge kind="statement" {status} />
			{/each}
		</div>
	</section>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">Engine badge / relative time / duration</h2>
		<div class="flex items-center gap-4">
			<EngineBadge engine={{ name: 'Trino', kind: 'trino', class: 'federated' }} />
			<RelativeTime at={new Date(Date.now() - 120_000)} live />
			<Duration ms={1234} />
		</div>
	</section>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">Filter bar</h2>
		<FilterBar
			fields={[
				{ key: 'q', label: 'Search', type: 'text' },
				{
					key: 'status',
					label: 'Status',
					type: 'select',
					options: [
						{ value: 'active', label: 'Active' },
						{ value: 'error', label: 'Error' }
					]
				},
				{ key: 'certified', label: 'Certified only', type: 'boolean' }
			]}
			bind:value={filters}
		/>
	</section>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">Table</h2>
		<DataTable
			columns={[
				{ key: 'name', label: 'Name' },
				{ key: 'status', label: 'Status' }
			]}
			rows={[{ id: '1', name: 'orders', status: 'active' }]}
			rowKey={(row) => String(row.id)}
		/>
	</section>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">Key/value editor</h2>
		<KeyValueEditor bind:value={kv} protectedKeys={['region']} />
	</section>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">Code / copy / diff</h2>
		<div class="grid gap-4 lg:grid-cols-2">
			<CodeBlock code="SELECT * FROM tpch.orders LIMIT 10" language="sql" />
			<DiffView
				language="sql"
				before="SELECT id, total FROM orders"
				after="SELECT id, total, status FROM orders"
			/>
		</div>
		<div class="flex items-center gap-2 text-xs text-text-muted">
			<span>bal_example_secret</span>
			<CopyButton value="bal_example_secret" label="API key" />
		</div>
	</section>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">Confirm dialog</h2>
		<button
			type="button"
			class="text-xs font-semibold text-primary"
			onclick={() => (confirmOpen = true)}
		>
			Delete table…
		</button>
		<ConfirmDialog
			bind:open={confirmOpen}
			title="Delete table"
			resourceName="orders"
			confirmLabel="Delete"
			typeToConfirm
			consequences={[
				'The table is dropped from the engine.',
				'Saved queries that use it stop working.'
			]}
			onconfirm={async () => {}}
		/>
	</section>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">Markdown</h2>
		<div class="grid gap-4 lg:grid-cols-2">
			<MarkdownView source={markdown} />
			<MarkdownEditor label="Description" bind:value={markdown} />
		</div>
	</section>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">Errors</h2>
		<ErrorPanel error={{ code: 'target_unavailable', request_id: 'req-123' }} />
	</section>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">Timeline</h2>
		<StateTimeline
			steps={[
				{ label: 'Provisioning', status: 'finished', at: new Date().toISOString() },
				{ label: 'Active', status: 'running' }
			]}
		/>
	</section>

	<section class="space-y-2">
		<h2 class="text-sm font-semibold">One-time secret</h2>
		<button
			type="button"
			class="text-xs font-semibold text-primary"
			onclick={() => (secretOpen = true)}
		>
			Show secret
		</button>
		<OneTimeSecret bind:open={secretOpen} secret="bal_example_secret" />
	</section>
</div>
