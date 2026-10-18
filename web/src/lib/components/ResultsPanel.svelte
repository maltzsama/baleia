<script lang="ts">
	import { onDestroy } from 'svelte';
	import { apiErrorMessage, apiFetch, client } from '$lib/api/client';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import type { components } from '$lib/api/schema';
	import { ResultsStore } from '$lib/results/store.svelte';
	import { toasts } from '$lib/ui/toast.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import ResultGrid from './ResultGrid.svelte';

	type QueryStatement = components['schemas']['QueryStatement'];

	let {
		executionId,
		enabled,
		initialPosition
	}: { executionId: string; enabled: boolean; initialPosition?: number } = $props();

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const store = new ResultsStore();

	let statements = $state<QueryStatement[]>([]);
	let loading = $state(false);
	let loadError = $state('');
	let activePosition = $state<number | null>(null);
	let activeResultIndex = $state(0);
	let retryTick = $state(0);
	let resultRetry = $state(0);
	let lastResultKey = '';
	let lastResultRetry = -1;
	let exportFormat = $state<'csv' | 'tsv' | 'jsonl' | 'arrow'>('csv');
	let exporting = $state(false);

	function errorMessage(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	$effect(() => {
		const id = executionId;
		const on = enabled;
		const ws = workspaceId;
		void retryTick;
		if (!on || !id || !ws) {
			statements = [];
			loading = false;
			loadError = '';
			activePosition = null;
			return;
		}
		let cancelled = false;
		loading = true;
		loadError = '';
		client
			.GET('/workspaces/{id}/executions/{execution_id}/statements', {
				params: { path: { id: ws, execution_id: id } }
			})
			.then(({ data, error }) => {
				if (cancelled) return;
				if (error || !data) throw new Error(apiErrorMessage(error, 'Failed to load statements'));
				statements = data;
				const withResults = data.filter((statement) => (statement.result_sets?.length ?? 0) > 0);
				activePosition =
					withResults.find((statement) => statement.position === initialPosition)?.position ??
					withResults[0]?.position ??
					data[0]?.position ??
					null;
				activeResultIndex = 0;
			})
			.catch((e) => {
				if (!cancelled) loadError = errorMessage(e);
			})
			.finally(() => {
				if (!cancelled) loading = false;
			});
		return () => {
			cancelled = true;
		};
	});

	const activeStatement = $derived(
		statements.find((statement) => statement.position === activePosition) ?? null
	);
	const resultSets = $derived(activeStatement?.result_sets ?? []);
	const activeSet = $derived(resultSets[activeResultIndex]);
	const resultUrl = $derived(
		activePosition != null && workspaceId && executionId
			? `/workspaces/${workspaceId}/executions/${executionId}/statements/${activePosition}/results?result_index=${activeResultIndex}`
			: ''
	);

	$effect(() => {
		const url = resultUrl;
		const retry = resultRetry;
		if (!url) {
			store.reset();
			lastResultKey = '';
			lastResultRetry = retry;
			return;
		}
		// A new URL or an explicit retry re-issues the stream.
		if (url === lastResultKey && retry === lastResultRetry) return;
		lastResultKey = url;
		lastResultRetry = retry;
		void store.load(url, activeSet?.rows ?? 0);
	});

	onDestroy(() => store.cancel());

	function statementTitle(statement: QueryStatement): string {
		return statement.sql.trim().split('\n')[0]?.slice(0, 60) || `Statement ${statement.position}`;
	}

	async function download() {
		if (activePosition == null || !workspaceId) return;
		exporting = true;
		try {
			const url = `/workspaces/${workspaceId}/executions/${executionId}/statements/${activePosition}/results?result_index=${activeResultIndex}&format=${exportFormat}`;
			const response = await apiFetch(url);
			if (!response.ok) throw new Error(`HTTP ${response.status}`);
			const blob = await response.blob();
			const href = URL.createObjectURL(blob);
			const link = document.createElement('a');
			link.href = href;
			link.download = `${executionId}-${activePosition}.${exportFormat}`;
			document.body.appendChild(link);
			link.click();
			link.remove();
			URL.revokeObjectURL(href);
		} catch (e) {
			toasts.error('Export failed: ' + errorMessage(e));
		} finally {
			exporting = false;
		}
	}
</script>

<div class="flex min-h-0 flex-1 flex-col" data-testid="results-panel">
	{#if !enabled}
		<div class="space-y-2 p-4" aria-label="Waiting for results">
			<Skeleton class="h-4 w-1/3" />
			<Skeleton class="h-4 w-2/3" />
		</div>
	{:else if loading}
		<div class="space-y-2 p-4" aria-label="Loading statements">
			<Skeleton class="h-4 w-1/3" />
			<Skeleton class="h-4 w-2/3" />
		</div>
	{:else if loadError}
		<ErrorState
			title="Could not load the results"
			message={loadError}
			onretry={() => retryTick++}
		/>
	{:else if statements.length === 0}
		<EmptyState title="No statements" description="This execution did not record any statements." />
	{:else}
		<div
			class="flex shrink-0 items-stretch overflow-x-auto border-b border-border bg-bg-toolbar px-2"
		>
			{#each statements as statement (statement.id)}
				{@const hasResults = (statement.result_sets?.length ?? 0) > 0}
				<button
					type="button"
					class="flex items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-2 text-[11px] font-semibold {statement.position ===
					activePosition
						? 'border-b-primary text-primary'
						: 'border-b-transparent text-text-muted hover:text-text-main'} {hasResults
						? ''
						: 'opacity-60'}"
					title={statementTitle(statement)}
					onclick={() => {
						activePosition = statement.position;
						activeResultIndex = 0;
					}}
				>
					<span>Statement {statement.position}</span>
					<StatusBadge kind="statement" status={statement.status} />
				</button>
			{/each}
			<span class="flex-1"></span>
			<div class="flex shrink-0 items-center gap-1 pr-1 text-[11px] text-text-muted">
				<select
					bind:value={exportFormat}
					aria-label="Export format"
					class="rounded border border-border bg-bg-panel px-1.5 py-1 text-[11px]"
				>
					<option value="csv">CSV</option>
					<option value="tsv">TSV</option>
					<option value="jsonl">JSON Lines</option>
					<option value="arrow">Arrow</option>
				</select>
				<button
					type="button"
					class="rounded border border-border px-2 py-1 font-medium text-text-muted hover:border-border-hover hover:text-text-main disabled:opacity-50"
					disabled={activePosition == null || exporting}
					onclick={download}
				>
					{exporting ? 'Exporting…' : 'Export'}
				</button>
			</div>
		</div>

		{#if resultSets.length > 1}
			<div class="flex shrink-0 items-center gap-1 border-b border-border px-3 py-1">
				{#each resultSets as set (set.result_index)}
					<button
						type="button"
						class="rounded px-2 py-0.5 text-[11px] font-medium {set.result_index ===
						activeResultIndex
							? 'bg-bg-panel text-primary'
							: 'text-text-muted hover:text-text-main'}"
						onclick={() => (activeResultIndex = set.result_index)}
					>
						Result {set.result_index + 1}
					</button>
				{/each}
			</div>
		{/if}

		{#if activeSet?.truncated}
			<div
				class="border-b border-amber-200 bg-amber-50 px-3 py-1.5 text-[11px] text-amber-700 dark:border-[#4d3600] dark:bg-[#241a06] dark:text-amber-300"
				role="status"
			>
				Showing the first {activeSet.rows.toLocaleString()} rows; the limit is configured by the administrator.
			</div>
		{/if}

		{#if resultSets.length === 0}
			<EmptyState title="No rows" description="This statement returned no result set." />
		{:else if store.error}
			<ErrorState
				title="Could not load the result"
				message={store.error}
				onretry={() => resultRetry++}
			/>
		{:else if store.done && store.rowCount === 0 && store.columns.length === 0}
			<EmptyState title="No rows" description="The statement returned an empty result." />
		{:else if store.columns.length === 0}
			<div class="space-y-2 p-4" aria-label="Loading results">
				<Skeleton class="h-4 w-1/3" />
				<Skeleton class="h-4 w-2/3" />
			</div>
		{:else}
			<ResultGrid {store} />
		{/if}
	{/if}
</div>
