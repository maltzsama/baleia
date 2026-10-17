<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import RetrieverForm from '$lib/retrievers/RetrieverForm.svelte';
	import {
		getRetriever,
		listHistory,
		refreshRetriever,
		type Retriever,
		type RetrieverHistory
	} from '$lib/retrievers/client';
	import { poll } from '$lib/util/poll';
	import { formatDuration } from '$lib/util/time';
	import Button from '$lib/ui/Button.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const retrieverId = $derived(page.params.retriever_id ?? '');

	let retriever = $state<Retriever | null>(null);
	let history = $state<RetrieverHistory[]>([]);
	let loading = $state(true);
	let error = $state('');
	let refreshing = $state(false);

	async function load() {
		if (!workspaceId || !retrieverId) return;
		loading = true;
		error = '';
		try {
			retriever = await getRetriever(workspaceId, retrieverId);
			history = await listHistory(workspaceId, retrieverId);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function refresh() {
		if (!retriever) return;
		refreshing = true;
		try {
			await refreshRetriever(workspaceId, retriever.id);
			const controller = poll(
				async () => {
					const rows = await listHistory(workspaceId, retriever!.id);
					history = rows;
					return rows;
				},
				{
					until: (rows) => (rows[0]?.status ?? 'running') !== 'running',
					intervals: [1000, 2000, 5000]
				}
			);
			await controller.done;
			retriever = await getRetriever(workspaceId, retriever.id);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			refreshing = false;
		}
	}

	onMount(async () => {
		await workspaceStore.load();
		await load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	{#if loading}
		<Skeleton class="h-48 w-full" />
	{:else if error}
		<ErrorState title="Could not load the retriever" message={error} onretry={load} />
	{:else if retriever}
		<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{retriever.name}</h1>
				<p class="mt-1 text-[12px] text-slate-600 dark:text-slate-300">
					{retriever.destination
						? `${retriever.destination.engine.name} · ${retriever.destination.catalog.name}.${retriever.dest_namespace}.${retriever.dest_table}`
						: `${retriever.dest_catalog}.${retriever.dest_namespace}.${retriever.dest_table}`}
				</p>
			</div>
			<div class="flex items-center gap-2">
				<StatusBadge kind="retriever" status={retriever.status} />
				<Button loading={refreshing} disabled={refreshing} onclick={refresh}>Refresh now</Button>
			</div>
		</div>

		{#if retriever.error_code}
			<div
				class="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-[12px] text-red-800 dark:border-[#4d1111] dark:bg-[#1f0f0f] dark:text-red-300"
			>
				<span class="font-mono">{retriever.error_code}</span>
				{#if retriever.status === 'error'}
					— choose a new destination or fix the schedule, then save.
				{/if}
			</div>
		{/if}

		<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_380px]">
			<RetrieverForm
				{retriever}
				ondone={(saved) => {
					retriever = saved;
					void load();
				}}
			/>

			<section class="min-w-0">
				<h2 class="text-[13px] font-semibold">Refresh history</h2>
				<div class="mt-2 overflow-x-auto rounded-lg border border-border bg-bg-panel">
					<table class="w-full text-left text-[11px]">
						<thead
							class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
						>
							<tr>
								<th class="px-2 py-2">Status</th>
								<th class="px-2 py-2">Started</th>
								<th class="px-2 py-2">Duration</th>
								<th class="px-2 py-2">Rows</th>
							</tr>
						</thead>
						<tbody>
							{#each history as run (run.id)}
								<tr class="border-b border-border/60 last:border-b-0">
									<td class="px-2 py-1.5">
										<StatusBadge
											kind="sync"
											status={run.status === 'finished'
												? 'ok'
												: run.status === 'running'
													? 'running'
													: 'failed'}
											title={run.status}
										/>
										{#if run.error_code}
											<span class="block font-mono text-[10px] text-danger">{run.error_code}</span>
										{/if}
									</td>
									<td class="px-2 py-1.5"><RelativeTime at={run.started_at} /></td>
									<td class="px-2 py-1.5">
										{run.duration_ms != null ? formatDuration(run.duration_ms) : '—'}
									</td>
									<td class="px-2 py-1.5">{run.rows_processed ?? '—'}</td>
								</tr>
							{:else}
								<tr>
									<td class="px-2 py-3 text-slate-600 dark:text-slate-300" colspan="4">
										No refreshes yet.
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			</section>
		</div>
	{/if}
</div>
