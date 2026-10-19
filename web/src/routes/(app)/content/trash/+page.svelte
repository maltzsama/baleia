<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import HistoryDrawer from '$lib/content/HistoryDrawer.svelte';
	import { listTrash, restoreVersion, type TrashItem } from '$lib/content/history';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	let items = $state<TrashItem[]>([]);
	let loading = $state(true);
	let error = $state('');
	let kind = $state<'' | 'saved_query' | 'semantic_metric'>('');
	let busy = $state('');
	let actionError = $state('');

	let historyOpen = $state(false);
	let historyKind = $state<'saved_query' | 'semantic_metric'>('saved_query');
	let historyId = $state('');

	async function load() {
		if (!workspaceId) return;
		loading = true;
		error = '';
		try {
			items = await listTrash(workspaceId, kind || undefined);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function restore(item: TrashItem) {
		busy = item.entity_id;
		actionError = '';
		try {
			await restoreVersion(workspaceId, item.entity_type, item.entity_id, item.restore_version);
			items = items.filter((row) => row.entity_id !== item.entity_id);
		} catch (e) {
			actionError = e instanceof Error ? e.message : String(e);
		} finally {
			busy = '';
		}
	}

	function openHistory(item: TrashItem) {
		historyKind = item.entity_type;
		historyId = item.entity_id;
		historyOpen = true;
	}

	onMount(async () => {
		await workspaceStore.load();
		await load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4 flex flex-wrap items-end justify-between gap-3">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.content_trash_trash()}</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				{m.content_trash_deleted_queries_and_metrics_can_be_restore()}
			</p>
		</div>
		<div class="w-44">
			<Select
				label={m.common_kind()}
				bind:value={kind}
				options={[
					{ value: '', label: 'All' },
					{ value: 'saved_query', label: 'Queries' },
					{ value: 'semantic_metric', label: 'Metrics' }
				]}
				onchange={() => load()}
			/>
		</div>
	</div>

	{#if actionError}
		<p class="mb-3 text-[11px] text-danger" role="alert">{actionError}</p>
	{/if}

	{#if loading}
		<Skeleton class="h-32 w-full" />
	{:else if error}
		<ErrorState title="Could not load the trash" message={error} onretry={load} />
	{:else if items.length === 0}
		<EmptyState
			title="Trash is empty"
			description="Deleted queries and metrics appear here and can be restored."
		/>
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
			<table class="w-full text-left text-[11px]">
				<thead
					class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
				>
					<tr>
						<th class="px-3 py-2">Kind</th>
						<th class="px-3 py-2">Name</th>
						<th class="px-3 py-2">Space</th>
						<th class="px-3 py-2">Deleted</th>
						<th class="px-3 py-2 text-right">Actions</th>
					</tr>
				</thead>
				<tbody>
					{#each items as item (item.entity_type + item.entity_id)}
						<tr class="border-b border-border/60 last:border-b-0">
							<td class="px-3 py-1.5">
								<Badge tone="neutral"
									>{item.entity_type === 'saved_query' ? 'Query' : 'Metric'}</Badge
								>
							</td>
							<td class="px-3 py-1.5">{item.name ?? item.slug}</td>
							<td class="px-3 py-1.5">{item.space?.name ?? '—'}</td>
							<td class="px-3 py-1.5"><RelativeTime at={item.deleted_at} /></td>
							<td class="px-3 py-1.5 text-right">
								<button
									type="button"
									class="text-primary hover:underline"
									onclick={() => openHistory(item)}
								>
									History
								</button>
								<Button
									size="sm"
									class="ml-2"
									loading={busy === item.entity_id}
									disabled={busy === item.entity_id}
									onclick={() => restore(item)}
								>
									Restore
								</Button>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>

<HistoryDrawer
	bind:open={historyOpen}
	kind={historyKind}
	entityId={historyId}
	{workspaceId}
	onrestored={load}
/>
