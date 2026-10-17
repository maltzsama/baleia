<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { authStore } from '$lib/stores/auth.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { toasts } from '$lib/ui/toast.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import EngineBadge from '$lib/ui/patterns/EngineBadge.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import {
		deleteRetriever,
		listRetrievers,
		refreshRetriever,
		updateRetriever,
		type Retriever
	} from '$lib/retrievers/client';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const enabled = $derived(authStore.user?.features?.retrievers ?? false);

	let retrievers = $state<Retriever[]>([]);
	let loading = $state(true);
	let error = $state('');
	let busyId = $state('');
	let deleting = $state<Retriever | null>(null);
	let deleteOpen = $state(false);

	async function load() {
		if (!workspaceId) return;
		loading = true;
		error = '';
		try {
			retrievers = await listRetrievers(workspaceId);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function refresh(retriever: Retriever) {
		busyId = retriever.id;
		try {
			await refreshRetriever(workspaceId, retriever.id);
			toasts.success(`Refreshing ${retriever.name}`);
			await load();
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		} finally {
			busyId = '';
		}
	}

	async function togglePause(retriever: Retriever) {
		busyId = retriever.id;
		try {
			await updateRetriever(workspaceId, retriever.id, {
				status: retriever.status === 'paused' ? 'active' : 'paused'
			});
			await load();
		} catch (e) {
			toasts.error(e instanceof Error ? e.message : String(e));
		} finally {
			busyId = '';
		}
	}

	async function remove() {
		if (!deleting) return;
		const target = deleting;
		await deleteRetriever(workspaceId, target.id);
		retrievers = retrievers.filter((item) => item.id !== target.id);
		deleting = null;
		toasts.success('Retriever removed');
	}

	onMount(() => {
		void (async () => {
			await workspaceStore.load();
			if (enabled) await load();
			else loading = false;
		})();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-5 flex items-end justify-between">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">Retrievers</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				Run a SELECT on a schedule and keep the result as an Iceberg table.
			</p>
		</div>
		{#if enabled}
			<Button variant="primary" onclick={() => goto(resolve('/retrievers/new'))}>
				New retriever
			</Button>
		{/if}
	</div>

	{#if !enabled}
		<EmptyState
			title="Retrievers are disabled"
			description="An administrator can enable the retrievers feature flag for this instance."
		/>
	{:else if loading}
		<div class="flex flex-col gap-2" aria-label="Loading retrievers">
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-12 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title="Could not load retrievers" message={error} onretry={load} />
	{:else if retrievers.length === 0}
		<EmptyState
			title="No retrievers"
			description="A retriever needs a destination: an Iceberg catalog bound to a federated engine that this workspace may write to."
		>
			<Button variant="primary" onclick={() => goto(resolve('/retrievers/new'))}>
				New retriever
			</Button>
		</EmptyState>
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
			<table class="w-full text-left text-[12px]">
				<thead
					class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
				>
					<tr>
						<th class="px-3 py-2">Name</th>
						<th class="px-3 py-2">Destination</th>
						<th class="px-3 py-2">Schedule</th>
						<th class="px-3 py-2">Status</th>
						<th class="px-3 py-2">Last refresh</th>
						<th class="px-3 py-2 text-right">Actions</th>
					</tr>
				</thead>
				<tbody>
					{#each retrievers as retriever (retriever.id)}
						<tr class="border-b border-border/60 last:border-b-0">
							<td class="px-3 py-2">
								<a
									class="font-semibold text-primary hover:underline"
									href={resolve(`/retrievers/${retriever.id}`)}
								>
									{retriever.name}
								</a>
							</td>
							<td class="px-3 py-2">
								{#if retriever.destination}
									<div class="flex flex-wrap items-center gap-1.5">
										<EngineBadge engine={retriever.destination.engine} />
										<span class="font-mono text-[11px]">
											{retriever.destination.catalog
												.name}.{retriever.dest_namespace}.{retriever.dest_table}
										</span>
									</div>
								{:else}
									<span class="text-slate-600 dark:text-slate-300">
										{retriever.dest_catalog}.{retriever.dest_namespace}.{retriever.dest_table}
									</span>
								{/if}
							</td>
							<td class="px-3 py-2">
								<span class="font-mono text-[11px]">{retriever.cron_schedule}</span>
								{#if retriever.next_refresh_at}
									<span class="block text-[10px] text-slate-600 dark:text-slate-300">
										next <RelativeTime at={retriever.next_refresh_at} />
									</span>
								{/if}
							</td>
							<td class="px-3 py-2">
								<StatusBadge kind="retriever" status={retriever.status} />
								{#if retriever.error_code}
									<span class="block font-mono text-[10px] text-danger">{retriever.error_code}</span
									>
								{/if}
							</td>
							<td class="px-3 py-2">
								{#if retriever.last_refresh_status}
									<StatusBadge
										kind="retriever"
										status={retriever.last_refresh_status === 'failed'
											? 'error'
											: retriever.last_refresh_status === 'running'
												? 'active'
												: 'active'}
										title={retriever.last_refresh_status}
									/>
								{/if}
								{#if retriever.last_refresh_at}
									<span class="block text-[10px] text-slate-600 dark:text-slate-300">
										<RelativeTime at={retriever.last_refresh_at} />
									</span>
								{/if}
							</td>
							<td class="px-3 py-2">
								<div class="flex flex-wrap justify-end gap-1">
									<Button
										size="sm"
										loading={busyId === retriever.id}
										disabled={busyId === retriever.id}
										onclick={() => refresh(retriever)}
									>
										Refresh now
									</Button>
									<Button
										size="sm"
										disabled={busyId === retriever.id}
										onclick={() => togglePause(retriever)}
									>
										{retriever.status === 'paused' ? 'Resume' : 'Pause'}
									</Button>
									<Button size="sm" onclick={() => goto(resolve(`/retrievers/${retriever.id}`))}>
										Edit
									</Button>
									<Button
										size="sm"
										variant="danger"
										onclick={() => {
											deleting = retriever;
											deleteOpen = true;
										}}
									>
										Delete
									</Button>
								</div>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>

{#if deleting}
	<ConfirmDialog
		bind:open={deleteOpen}
		title="Remove retriever"
		resourceName={deleting.name}
		consequences={['The destination table is not dropped.', 'Refresh history is removed.']}
		confirmLabel="Remove retriever"
		onconfirm={remove}
	/>
{/if}
