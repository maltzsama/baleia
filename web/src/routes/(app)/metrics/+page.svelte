<script lang="ts">
	import { onMount } from 'svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import { listMetrics, type MetricDefinition, type SemanticMetric } from '$lib/metrics/client';
	import { describeDefinition } from '$lib/metrics/definition';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const spaces = $derived(savedQueries.spaces);

	let metrics = $state<SemanticMetric[]>([]);
	let loading = $state(true);
	let error = $state('');
	let q = $state('');
	let certified = $state<'' | 'yes' | 'no'>('');
	let spaceId = $state('');

	async function load() {
		if (!workspaceId) return;
		loading = true;
		error = '';
		try {
			await savedQueries.reload(workspaceId);
			metrics = await listMetrics(workspaceId, {
				q: q.trim() || undefined,
				space_id: spaceId || undefined,
				certified: certified === '' ? undefined : certified === 'yes'
			});
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	function definition(metric: SemanticMetric): MetricDefinition {
		return metric.definition as unknown as MetricDefinition;
	}

	function openMetric(metric: SemanticMetric) {
		window.location.href = `/metrics/${metric.id}`;
	}

	onMount(async () => {
		await workspaceStore.load();
		await load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4 flex flex-wrap items-end justify-between gap-3">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">Metrics</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				Named aggregations over one table, rendered for whichever engine you run on.
			</p>
		</div>
		<Button variant="primary" onclick={() => (window.location.href = '/metrics/new')}
			>New metric</Button
		>
	</div>

	<div
		class="mb-3 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-2 lg:grid-cols-4"
	>
		<Input label="Search" bind:value={q} placeholder="orders" />
		<Select
			label="Space"
			bind:value={spaceId}
			options={[
				{ value: '', label: 'All' },
				...spaces.map((space) => ({ value: space.id, label: space.name }))
			]}
		/>
		<Select
			label="Certified"
			bind:value={certified}
			options={[
				{ value: '', label: 'Any' },
				{ value: 'yes', label: 'Certified only' },
				{ value: 'no', label: 'Not certified' }
			]}
		/>
		<div class="flex items-end">
			<Button onclick={load}>Apply</Button>
		</div>
	</div>

	{#if loading}
		<div class="flex flex-col gap-2" aria-label="Loading metrics">
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-12 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title="Could not load metrics" message={error} onretry={load} />
	{:else if metrics.length === 0}
		<EmptyState
			title="No metrics"
			description="Create a metric from a table to reuse it in queries."
		/>
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
			<table class="w-full text-left text-[11px]">
				<thead
					class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
				>
					<tr>
						<th class="px-3 py-2">Metric</th>
						<th class="px-3 py-2">Definition</th>
						<th class="px-3 py-2">Space</th>
						<th class="px-3 py-2">Owner</th>
						<th class="px-3 py-2">Updated</th>
					</tr>
				</thead>
				<tbody>
					{#each metrics as metric (metric.id)}
						<tr class="border-b border-border/60 last:border-b-0 hover:bg-bg-toolbar">
							<td class="px-3 py-2">
								<button
									type="button"
									class="text-left font-medium hover:underline"
									onclick={() => openMetric(metric)}
								>
									{metric.label}
								</button>
								{#if metric.certified}<Badge tone="success">Certified</Badge>{/if}
								<div class="font-mono text-[10px] text-slate-600 dark:text-slate-300">
									{metric.slug}
								</div>
							</td>
							<td class="max-w-[420px] px-3 py-2 text-slate-600 dark:text-slate-300">
								{describeDefinition(definition(metric), {
									catalog: metric.catalog_id,
									namespace: metric.namespace,
									table: metric.table
								})}
							</td>
							<td class="px-3 py-2">
								{spaces.find((space) => space.id === metric.space_id)?.name ?? '—'}
							</td>
							<td class="px-3 py-2 font-mono text-[10px]">{metric.owner_user_id ?? '—'}</td>
							<td class="px-3 py-2"><RelativeTime at={metric.updated_at} /></td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
