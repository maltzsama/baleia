<script lang="ts">
	import { m } from '$lib/paraglide/messages';
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
			toasts.success(m.retrievers_refreshing({ name: retriever.name }));
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
		toasts.success(m.retrievers_retriever_removed());
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
			<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.common_retrievers()}</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				{m.common_run_a_select_on_a_schedule_and_keep_the_re()}
			</p>
		</div>
		{#if enabled}
			<Button variant="primary" onclick={() => goto(resolve('/retrievers/new'))}>
				{m.common_new_retriever()}
			</Button>
		{/if}
	</div>

	{#if !enabled}
		<EmptyState
			title={m.retrievers_retrievers_are_disabled()}
			description={m.retrievers_an_administrator_can_enable_the_retrievers()}
		/>
	{:else if loading}
		<div class="flex flex-col gap-2" aria-label={m.retrievers_loading_retrievers()}>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-12 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title={m.retrievers_could_not_load_retrievers()} message={error} onretry={load} />
	{:else if retrievers.length === 0}
		<EmptyState
			title={m.retrievers_no_retrievers()}
			description={m.retrievers_a_retriever_needs_a_destination_an()}
		>
			<Button variant="primary" onclick={() => goto(resolve('/retrievers/new'))}>
				{m.retrievers_new_retriever()}
			</Button>
		</EmptyState>
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
			<table class="w-full text-left text-[12px]">
				<thead
					class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
				>
					<tr>
						<th class="px-3 py-2">{m.retrievers_name()}</th>
						<th class="px-3 py-2">{m.retrievers_destination()}</th>
						<th class="px-3 py-2">{m.retrievers_schedule()}</th>
						<th class="px-3 py-2">{m.retrievers_status()}</th>
						<th class="px-3 py-2">{m.retrievers_last_refresh()}</th>
						<th class="px-3 py-2 text-right">{m.retrievers_actions()}</th>
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
										{m.retrievers_next()}
										<RelativeTime at={retriever.next_refresh_at} />
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
										{m.retrievers_refresh_now()}
									</Button>
									<Button
										size="sm"
										disabled={busyId === retriever.id}
										onclick={() => togglePause(retriever)}
									>
										{retriever.status === 'paused' ? m.retrievers_resume() : m.retrievers_pause()}
									</Button>
									<Button size="sm" onclick={() => goto(resolve(`/retrievers/${retriever.id}`))}>
										{m.retrievers_edit()}
									</Button>
									<Button
										size="sm"
										variant="danger"
										onclick={() => {
											deleting = retriever;
											deleteOpen = true;
										}}
									>
										{m.retrievers_delete()}
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
		title={m.retrievers_remove_retriever()}
		resourceName={deleting.name}
		consequences={[
			'The destination table is not dropped.',
			m.retrievers_refresh_history_is_removed()
		]}
		confirmLabel={m.retrievers_remove_retriever()}
		onconfirm={remove}
	/>
{/if}
