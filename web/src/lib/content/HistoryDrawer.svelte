<script lang="ts">
	import Button from '$lib/ui/Button.svelte';
	import Drawer from '$lib/ui/Drawer.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import DiffView from '$lib/ui/patterns/DiffView.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import {
		ContentHistoryError,
		getVersion,
		listVersions,
		restoreVersion,
		snapshotMetricJSON,
		snapshotSQL,
		type ContentVersion,
		type ContentVersionDetail
	} from '$lib/content/history';

	let {
		open = $bindable(false),
		kind,
		entityId,
		workspaceId,
		onrestored
	}: {
		open?: boolean;
		kind: 'saved_query' | 'semantic_metric';
		entityId: string;
		workspaceId: string;
		onrestored?: () => void;
	} = $props();

	let versions = $state<ContentVersion[]>([]);
	let selected = $state<ContentVersion | null>(null);
	let detail = $state<ContentVersionDetail | null>(null);
	let previous = $state<ContentVersionDetail | null>(null);
	let loading = $state(false);
	let restoring = $state(false);
	let error = $state('');
	let conflict = $state(false);
	let newName = $state('');

	$effect(() => {
		if (!open || !entityId || !workspaceId) return;
		void load();
	});

	async function load() {
		loading = true;
		error = '';
		conflict = false;
		try {
			versions = await listVersions(workspaceId, kind, entityId);
			const first = versions[0];
			if (first) await select(first);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function select(version: ContentVersion) {
		selected = version;
		error = '';
		try {
			detail = await getVersion(workspaceId, kind, entityId, version.version);
			previous =
				version.version > 1
					? await getVersion(workspaceId, kind, entityId, version.version - 1)
					: null;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	const before = $derived(
		kind === 'saved_query'
			? snapshotSQL(previous?.snapshot)
			: snapshotMetricJSON(previous?.snapshot)
	);
	const after = $derived(
		kind === 'saved_query' ? snapshotSQL(detail?.snapshot) : snapshotMetricJSON(detail?.snapshot)
	);

	async function restore() {
		if (!selected) return;
		restoring = true;
		error = '';
		try {
			await restoreVersion(workspaceId, kind, entityId, selected.version, {
				new_name: newName.trim() || undefined
			});
			conflict = false;
			onrestored?.();
			await load();
		} catch (e) {
			if (
				e instanceof ContentHistoryError &&
				(e.code === 'name_conflict' || e.code === 'slug_conflict')
			) {
				conflict = true;
			}
			error = e instanceof Error ? e.message : String(e);
		} finally {
			restoring = false;
		}
	}
</script>

<Drawer bind:open title="History">
	{#if loading}
		<Skeleton class="h-40 w-full" />
	{:else if error}
		<p class="mb-2 text-[11px] text-danger" role="alert">{error}</p>
	{/if}
	<div class="grid gap-3 sm:grid-cols-[160px_1fr]">
		<ul class="flex flex-col gap-1 text-[11px]">
			{#each versions as version (version.version)}
				<li>
					<button
						type="button"
						class="w-full rounded px-2 py-1 text-left {selected?.version === version.version
							? 'bg-bg-toolbar font-semibold'
							: 'hover:bg-bg-toolbar'}"
						onclick={() => select(version)}
					>
						<span class="block">v{version.version} · {version.change_kind}</span>
						<span class="block text-[10px] text-slate-600 dark:text-slate-300">
							{version.author?.name ?? 'Unknown'} · <RelativeTime at={version.created_at} />
						</span>
						{#if version.restored_from != null}
							<span class="block text-[10px] text-slate-600 dark:text-slate-300">
								from v{version.restored_from}
							</span>
						{/if}
					</button>
				</li>
			{:else}
				<li class="text-slate-600 dark:text-slate-300">No versions yet.</li>
			{/each}
		</ul>
		<div class="min-w-0">
			{#if selected && detail}
				<div class="mb-2 flex items-center justify-between gap-2">
					<p class="text-[11px] text-slate-600 dark:text-slate-300">
						v{selected.version} vs v{selected.version > 1 ? selected.version - 1 : selected.version}
					</p>
					<Button size="sm" loading={restoring} disabled={restoring} onclick={restore}>
						Restore this version
					</Button>
				</div>
				{#if conflict}
					<div class="mb-2 flex items-end gap-2">
						<Input label="Name conflict — restore as" bind:value={newName} />
						<Button size="sm" onclick={restore} disabled={restoring}>Retry</Button>
					</div>
				{/if}
				{#if kind === 'saved_query'}
					<DiffView {before} {after} language="sql" />
				{:else}
					<DiffView {before} {after} language="json" />
				{/if}
			{:else}
				<p class="text-[11px] text-slate-600 dark:text-slate-300">Choose a version.</p>
			{/if}
		</div>
	</div>
	{#snippet footer()}
		<div class="flex justify-end">
			<Button onclick={() => (open = false)}>Close</Button>
		</div>
	{/snippet}
</Drawer>
