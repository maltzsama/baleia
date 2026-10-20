<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { editor } from '$lib/stores/editor.svelte';
	import { savedQueries, type SavedQuery, type Space } from '$lib/stores/savedQueries.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import MoveToSpaceDialog from './MoveToSpaceDialog.svelte';
	import SpaceAccessDialog from './SpaceAccessDialog.svelte';
	import SpaceDialog from './SpaceDialog.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const spaces = $derived(savedQueries.spaces);
	const loading = $derived(savedQueries.loading);
	const loadError = $derived(savedQueries.error);

	let actionError = $state('');
	let collapsed = $state<Record<string, boolean>>({});
	let menuOpen = $state<string | null>(null);
	let renameId = $state<string | null>(null);
	let renameValue = $state('');

	let spaceDialogOpen = $state(false);
	let editingSpace = $state<Space | null>(null);
	let accessSpace = $state<Space | null>(null);
	let accessSpaceOpen = $state(false);
	let movingQuery = $state<SavedQuery | null>(null);
	let movingQueryOpen = $state(false);
	let deletingQuery = $state<SavedQuery | null>(null);
	let deleteQueryOpen = $state(false);
	let deletingSpace = $state<Space | null>(null);
	let deleteSpaceOpen = $state(false);

	$effect(() => {
		if (workspaceId) void savedQueries.reload(workspaceId);
	});

	function toggle(spaceId: string) {
		collapsed = { ...collapsed, [spaceId]: !collapsed[spaceId] };
	}

	function openQuery(q: SavedQuery) {
		editor.openSavedQuery(q, !savedQueries.canEdit(q.space_id));
	}

	async function renameQuery(q: SavedQuery) {
		const res = await savedQueries.renameQuery(q, renameValue.trim());
		renameId = null;
		if (res.error) actionError = res.error;
	}

	function startRename(q: SavedQuery) {
		renameId = q.id;
		renameValue = q.name;
		menuOpen = null;
	}

	async function deleteQuery() {
		if (!deletingQuery) return;
		const res = await savedQueries.deleteQuery(deletingQuery);
		if (res.error) throw new Error(res.error);
		deletingQuery = null;
		menuOpen = null;
	}

	async function deleteSpace() {
		if (!deletingSpace) return;
		const res = await savedQueries.deleteSpace(deletingSpace.id, true);
		if (res.error) throw new Error(res.error);
		deletingSpace = null;
		menuOpen = null;
	}

	function focusOnMount(node: HTMLInputElement) {
		node.focus();
	}

	function startSpaceCreate() {
		editingSpace = null;
		spaceDialogOpen = true;
	}

	function startSpaceRename(space: Space) {
		editingSpace = space;
		spaceDialogOpen = true;
		menuOpen = null;
	}
</script>

<div class="flex min-h-0 flex-1 flex-col">
	<div class="flex items-center gap-2 border-b border-border px-2 py-1.5">
		<span class="flex-1 text-[10px] font-bold tracking-wide text-text-muted uppercase"
			>{m.common_content()}</span
		>
		<Button variant="ghost" size="sm" onclick={startSpaceCreate}
			>{m.components_savedquerieslist_space()}</Button
		>
	</div>

	<div class="flex-1 overflow-y-auto px-1.5 pt-1.5 pb-2">
		{#if loading}
			<p class="px-2 py-4 text-xs text-text-muted">{m.common_loading()}</p>
		{:else if loadError || actionError}
			<p class="px-2 py-4 text-xs text-red-500" role="alert">{loadError || actionError}</p>
		{:else if spaces.length === 0}
			<p class="px-2 py-6 text-center text-xs text-text-muted">No spaces yet.</p>
		{:else}
			{#each spaces as space (space.id)}
				{@const spaceQueries = savedQueries.queriesIn(space.id)}
				{@const editable = savedQueries.canEdit(space.id)}
				<section class="mb-1.5">
					<div class="group relative flex items-center gap-1">
						<button
							class="flex min-w-0 flex-1 items-center gap-1.5 rounded-md border-none bg-none px-2 py-1.5 text-left text-inherit hover:bg-slate-100 dark:hover:bg-[#1c2128]"
							onclick={() => toggle(space.id)}
							aria-expanded={!collapsed[space.id]}
						>
							<svg
								viewBox="0 0 24 24"
								class="h-3 w-3 shrink-0 text-text-muted transition-transform {collapsed[space.id]
									? '-rotate-90'
									: ''}"
							>
								<path d="M6 9l6 6 6-6" />
							</svg>
							<span class="truncate text-xs font-semibold">{space.name}</span>
							{#if space.is_default}<Badge>Default</Badge>{/if}
							{#if space.visibility === 'restricted'}
								<svg
									viewBox="0 0 24 24"
									class="h-3 w-3 shrink-0 text-text-muted"
									aria-label="Restricted"
								>
									<rect x="4" y="11" width="16" height="10" rx="2" /><path
										d="M8 11V7a4 4 0 0 1 8 0v4"
									/>
								</svg>
							{/if}
							<span class="ml-auto text-[10px] text-text-muted">{spaceQueries.length}</span>
							{#if !editable}
								<svg viewBox="0 0 24 24" class="h-3 w-3 text-text-muted" aria-label="Read-only">
									<rect x="4" y="11" width="16" height="10" rx="2" /><path
										d="M8 11V7a4 4 0 0 1 8 0v4"
									/>
								</svg>
							{/if}
						</button>
						{#if savedQueries.canManage(space.id)}
							<button
								class="flex h-5 w-5 items-center justify-center rounded border-none bg-none text-text-muted opacity-0 group-hover:opacity-100 hover:bg-border hover:text-text-main"
								onclick={(e) => {
									e.stopPropagation();
									menuOpen = menuOpen === space.id ? null : space.id;
								}}
								aria-label={`Manage ${space.name}`}
							>
								<svg viewBox="0 0 24 24" class="h-3 w-3"
									><circle cx="12" cy="5" r="1.5" /><circle cx="12" cy="12" r="1.5" /><circle
										cx="12"
										cy="19"
										r="1.5"
									/></svg
								>
							</button>
							{#if menuOpen === space.id}
								<div
									class="absolute right-0 top-7 z-50 min-w-[120px] overflow-hidden rounded-lg border border-border bg-bg-panel shadow-[0_8px_24px_rgba(0,0,0,.1)]"
								>
									<button
										class="flex w-full items-center gap-2 border-none bg-none px-3 py-[7px] text-left text-xs font-medium text-text-main hover:bg-slate-50 dark:hover:bg-[#1c2128]"
										onclick={() => startSpaceRename(space)}
									>
										Rename
									</button>
									<button
										class="flex w-full items-center gap-2 border-none bg-none px-3 py-[7px] text-left text-xs font-medium text-text-main hover:bg-slate-50 dark:hover:bg-[#1c2128]"
										onclick={() => {
											accessSpace = space;
											accessSpaceOpen = true;
											menuOpen = null;
										}}
									>
										Access
									</button>
									{#if !space.is_default}
										<button
											class="flex w-full items-center gap-2 border-none bg-none px-3 py-[7px] text-left text-xs font-medium text-red-600 hover:bg-red-50 dark:hover:bg-[#1f0f0f]"
											onclick={() => {
												deletingSpace = space;
												deleteSpaceOpen = true;
												menuOpen = null;
											}}
										>
											Delete
										</button>
									{/if}
								</div>
							{/if}
						{/if}
					</div>

					{#if !collapsed[space.id]}
						{#if spaceQueries.length === 0}
							<p class="px-5 py-2 text-[11px] text-text-muted">No saved queries.</p>
						{:else}
							{#each spaceQueries as q (q.id)}
								{@const canEdit = savedQueries.canEdit(space.id)}
								<div class="group relative">
									<button
										class="mb-px flex w-full items-center gap-2 rounded-md border-none bg-none px-2 py-1.5 pl-6 text-left text-inherit hover:bg-slate-100 dark:hover:bg-[#1c2128]"
										onclick={() => openQuery(q)}
									>
										<svg viewBox="0 0 24 24" class="h-3.5 w-3.5 shrink-0 text-text-muted"
											><path
												d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"
											/><polyline points="14 2 14 8 20 8" /></svg
										>
										<div class="min-w-0 flex-1">
											{#if renameId === q.id}
												<input
													class="h-6 w-full rounded border border-primary bg-bg-toolbar px-1.5 text-xs outline-none"
													type="text"
													bind:value={renameValue}
													aria-label={`Rename ${q.name}`}
													onkeydown={(e) => e.key === 'Enter' && void renameQuery(q)}
													onblur={() => (renameId = null)}
													use:focusOnMount
												/>
											{:else}
												<div
													class="overflow-hidden text-ellipsis whitespace-nowrap text-xs font-medium"
												>
													{q.name}
												</div>
												<div class="mt-px text-[10px] text-text-muted">
													{q.sql.substring(0, 40)}{q.sql.length > 40 ? '...' : ''}
												</div>
											{/if}
										</div>
										{#if !canEdit}
											<svg
												viewBox="0 0 24 24"
												class="h-3 w-3 text-text-muted"
												aria-label="Read-only"
											>
												<rect x="4" y="11" width="16" height="10" rx="2" /><path
													d="M8 11V7a4 4 0 0 1 8 0v4"
												/>
											</svg>
										{/if}
									</button>
									{#if canEdit}
										<button
											class="absolute right-0 top-1 flex h-5 w-5 items-center justify-center rounded border-none bg-none text-text-muted opacity-0 group-hover:opacity-100 hover:bg-border hover:text-text-main"
											onclick={(e) => {
												e.stopPropagation();
												menuOpen = menuOpen === q.id ? null : q.id;
											}}
											aria-label={`Actions for ${q.name}`}
										>
											<svg viewBox="0 0 24 24" class="h-3 w-3"
												><circle cx="12" cy="5" r="1.5" /><circle cx="12" cy="12" r="1.5" /><circle
													cx="12"
													cy="19"
													r="1.5"
												/></svg
											>
										</button>
										{#if menuOpen === q.id}
											<div
												class="absolute right-0 top-7 z-50 min-w-[130px] overflow-hidden rounded-lg border border-border bg-bg-panel shadow-[0_8px_24px_rgba(0,0,0,.1)]"
											>
												<button
													class="flex w-full items-center gap-2 border-none bg-none px-3 py-[7px] text-left text-xs font-medium text-text-main hover:bg-slate-50 dark:hover:bg-[#1c2128]"
													onclick={() => startRename(q)}
												>
													Rename
												</button>
												{#if spaces.length > 1}
													<button
														class="flex w-full items-center gap-2 border-none bg-none px-3 py-[7px] text-left text-xs font-medium text-text-main hover:bg-slate-50 dark:hover:bg-[#1c2128]"
														onclick={() => {
															movingQuery = q;
															movingQueryOpen = true;
															menuOpen = null;
														}}
													>
														Move to space...
													</button>
												{/if}
												<button
													class="flex w-full items-center gap-2 border-none bg-none px-3 py-[7px] text-left text-xs font-medium text-red-600 hover:bg-red-50 dark:hover:bg-[#1f0f0f]"
													onclick={() => {
														deletingQuery = q;
														deleteQueryOpen = true;
														menuOpen = null;
													}}
												>
													Delete
												</button>
											</div>
										{/if}
									{/if}
								</div>
							{/each}
						{/if}
					{/if}
				</section>
			{/each}
		{/if}
	</div>
</div>

<SpaceDialog bind:open={spaceDialogOpen} space={editingSpace} />
<SpaceAccessDialog bind:open={accessSpaceOpen} space={accessSpace} />
<MoveToSpaceDialog bind:open={movingQueryOpen} query={movingQuery} />
<ConfirmDialog
	bind:open={deleteQueryOpen}
	title={m.components_savedquerieslist_delete_saved_query()}
	resourceName={deletingQuery?.name ?? ''}
	consequences={[
		'The query and its edit history are removed.',
		m.components_savedquerieslist_executions_already_run_keep_their_history()
	]}
	confirmLabel="Delete query"
	onconfirm={deleteQuery}
/>
<ConfirmDialog
	bind:open={deleteSpaceOpen}
	title={m.components_savedquerieslist_delete_space()}
	resourceName={deletingSpace?.name ?? ''}
	consequences={[
		'The space and its access list are removed.',
		m.components_savedquerieslist_every_folder_and_saved_query_in_it_is_dele(),
		m.components_savedquerieslist_this_cannot_be_undone()
	]}
	confirmLabel="Delete space"
	onconfirm={deleteSpace}
/>
