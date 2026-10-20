<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { getMetric, type SemanticMetric } from '$lib/metrics/client';
	import MetricBuilder from '$lib/metrics/MetricBuilder.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const metricId = $derived(page.params.metric_id ?? '');
	let metric = $state<SemanticMetric | null>(null);
	let loading = $state(true);
	let error = $state('');

	onMount(async () => {
		await workspaceStore.load();
		try {
			metric = await getMetric(workspaceId, metricId);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.metrics_edit_edit_metric()}</h1>
	</div>
	{#if loading}
		<Skeleton class="h-32 w-full" />
	{:else if error}
		<ErrorState title="Could not load the metric" message={error} />
	{:else if metric}
		<MetricBuilder {metric} />
	{/if}
</div>
