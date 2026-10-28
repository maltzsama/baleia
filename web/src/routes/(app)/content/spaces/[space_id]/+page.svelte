<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { savedQueries, type QueryFolder, type SavedQuery } from '$lib/stores/savedQueries.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import {
		createFolder,
		deleteFolder,
		listMetrics,
		updateFolder,
		type SemanticMetric
	} from '$lib/content/client';
	import HistoryDrawer from '$lib/content/HistoryDrawer.svelte';
	import { kindEntry } from '$lib/content/kinds';
	import { listModels, type SemanticModel } from '$lib/semantic/explore';
	import { exportSpace, importSpace, type ImportReport } from '$lib/content/history';
	import MoveToSpaceDialog from '$lib/components/MoveToSpaceDialog.svelte';
	import SpaceAccessDialog from '$lib/components/SpaceAccessDialog.svelte';
	import SpaceDialog from '$lib/components/SpaceDialog.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const spaceId = $derived(page.params.space_id ?? '');
	let currentFolder = $state<string | null>(page.url.searchParams.get('folder'));
	const space = $derived(savedQueries.space(spaceId));
	const folders = $derived(savedQueries.foldersIn(spaceId));
	let metrics = $state<SemanticMetric[]>([]);
	let models = $state<SemanticModel[]>([]);
	// Metrics and models load on their own (#460): a failure is shown with a
	// retry and never reads as an empty space; whatever did load stays.
	let metricsError = $state('');
	let modelsError = $state('');
	let semanticLoaded = $state(false);
	const loading = $derived(savedQueries.loading);
	const error = $derived(savedQueries.error);

	const canEdit = $derived(space ? savedQueries.canEdit(space.id) : false);
	const canManage = $derived(space ? savedQueries.canManage(space.id) : false);

	const tree = $derived.by(() => {
		const out: { folder: QueryFolder; depth: number }[] = [];
		const walk = (parent: string | null, depth: number) => {
			for (const folder of folders.filter((f) => f.parent_id === parent)) {
				out.push({ folder, depth });
				walk(folder.id, depth + 1);
			}
		};
		walk(null, 0);
		return out;
	});

	const queries = $derived(
		savedQueries
			.queriesIn(spaceId)
			.filter((query) => (query.folder_id ?? null) === (currentFolder ?? null))
	);
	const spaceModels = $derived(currentFolder ? [] : models);
	const spaceMetrics = $derived(
		currentFolder ? [] : metrics.filter((metric) => metric.space_id === spaceId)
	);

	let folderOpen = $state(false);
	let folderName = $state('');
	let folderSaving = $state(false);
	let folderError = $state('');

	let renameFolder = $state<QueryFolder | null>(null);
	let renameFolderOpen = $state(false);
	let renameFolderName = $state('');
	let moveFolder = $state<QueryFolder | null>(null);
	let moveFolderOpen = $state(false);
	let moveFolderTarget = $state('');
	let folderBusy = $state(false);

	let renameQuery = $state<SavedQuery | null>(null);
	let renameQueryOpen = $state(false);
	let renameQueryName = $state('');

	let movingQueryOpen = $state(false);
	let movingQuery = $state<SavedQuery | null>(null);

	let accessOpen = $state(false);
	let editOpen = $state(false);
	let deleteOpen = $state(false);
	let pending = $state<{ kind: 'query' | 'folder' | 'space'; id: string; name: string } | null>(
		null
	);

	let historyOpen = $state(false);
	let historyKind = $state<'saved_query' | 'semantic_metric'>('saved_query');
	let historyId = $state('');
	let exportBusy = $state(false);
	let importOpen = $state(false);
	let importError = $state('');
	let importName = $state('');
	let importBundle = $state<unknown>(null);
	let importReport = $state<ImportReport | null>(null);
	let importBusy = $state(false);
	let importApplying = $state(false);

	async function load() {
		await workspaceStore.load();
		if (!workspaceId) return;
		await savedQueries.reload(workspaceId);
		await loadSemantic();
	}

	async function loadSemantic() {
		metricsError = '';
		modelsError = '';
		const [metricRows, modelRows] = await Promise.allSettled([
			listMetrics(workspaceId),
			listModels(workspaceId, spaceId)
		]);
		if (metricRows.status === 'fulfilled') metrics = metricRows.value;
		else metricsError = errorText(metricRows.reason);
		if (modelRows.status === 'fulfilled') models = modelRows.value;
		else modelsError = errorText(modelRows.reason);
		semanticLoaded = true;
	}

	function errorText(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	function openQuery(query: SavedQuery) {
		window.location.href = `/sql/q/${query.id}`;
	}

	function openMetric(metric: SemanticMetric) {
		window.location.href = `/metrics/${metric.id}`;
	}

	function selectFolder(id: string | null) {
		currentFolder = id;
		const base = page.url.pathname;
		window.history.replaceState(null, '', id ? `${base}?folder=${id}` : base);
	}

	async function submitFolder() {
		if (!folderName.trim() || !workspaceId) return;
		folderSaving = true;
		folderError = '';
		try {
			await createFolder(workspaceId, {
				name: folderName.trim(),
				space_id: spaceId,
				parent_id: currentFolder ?? null
			});
			folderOpen = false;
			await savedQueries.reload(workspaceId);
		} catch (e) {
			folderError = e instanceof Error ? e.message : String(e);
		} finally {
			folderSaving = false;
		}
	}

	async function submitRenameFolder() {
		if (!renameFolder || !renameFolderName.trim() || !workspaceId) return;
		folderBusy = true;
		try {
			await updateFolder(workspaceId, renameFolder.id, { name: renameFolderName.trim() });
			renameFolder = null;
			renameFolderOpen = false;
			await savedQueries.reload(workspaceId);
		} catch (e) {
			folderError = e instanceof Error ? e.message : String(e);
		} finally {
			folderBusy = false;
		}
	}

	async function submitMoveFolder() {
		if (!moveFolder || !moveFolderTarget || !workspaceId) return;
		folderBusy = true;
		try {
			await updateFolder(workspaceId, moveFolder.id, {
				space_id: moveFolderTarget,
				parent_id: null
			});
			moveFolder = null;
			moveFolderOpen = false;
			await savedQueries.reload(workspaceId);
		} catch (e) {
			folderError = e instanceof Error ? e.message : String(e);
		} finally {
			folderBusy = false;
		}
	}

	async function submitRenameQuery() {
		if (!renameQuery || !renameQueryName.trim()) return;
		folderBusy = true;
		const res = await savedQueries.renameQuery(renameQuery, renameQueryName.trim());
		folderBusy = false;
		if (res.error) folderError = res.error;
		else {
			renameQuery = null;
			renameQueryOpen = false;
		}
	}

	async function confirmDelete() {
		const target = pending;
		if (!target || !workspaceId) return;
		if (target.kind === 'space') {
			const res = await savedQueries.deleteSpace(target.id, true);
			if (res.error) throw new Error(res.error);
			window.location.href = '/content';
			return;
		}
		if (target.kind === 'query') {
			const query = savedQueries.list.find((item) => item.id === target.id);
			if (query) {
				const res = await savedQueries.deleteQuery(query);
				if (res.error) throw new Error(res.error);
			}
			return;
		}
		await deleteFolder(workspaceId, target.id);
		await savedQueries.reload(workspaceId);
	}

	function ask(kind: 'query' | 'folder' | 'space', id: string, name: string) {
		pending = { kind, id, name };
		deleteOpen = true;
	}

	function openHistory(kind: 'saved_query' | 'semantic_metric', id: string) {
		historyKind = kind;
		historyId = id;
		historyOpen = true;
	}

	async function doExport() {
		if (!workspaceId || !space) return;
		exportBusy = true;
		folderError = '';
		try {
			const bundle = await exportSpace(workspaceId, spaceId);
			const blob = new Blob([JSON.stringify(bundle, null, 2)], { type: 'application/json' });
			const url = URL.createObjectURL(blob);
			const anchor = document.createElement('a');
			anchor.href = url;
			anchor.download = `${space.slug}.baleia.json`;
			anchor.click();
			URL.revokeObjectURL(url);
		} catch (e) {
			folderError = e instanceof Error ? e.message : String(e);
		} finally {
			exportBusy = false;
		}
	}

	function openImport() {
		importError = '';
		importReport = null;
		importBundle = null;
		importName = '';
		importOpen = true;
	}

	async function readImport(event: Event) {
		const input = event.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		importError = '';
		importReport = null;
		importBundle = null;
		importName = file.name;
		if (file.size > 10 * 1024 * 1024) {
			importError = m.content_spaces_the_bundle_is_larger_than_10_mib();
			return;
		}
		try {
			const parsed = JSON.parse(await file.text());
			if (parsed.format !== 'baleia.bundle' || parsed.format_version !== 1) {
				importError = m.content_spaces_unsupported_bundle_format_or_version();
				return;
			}
			importBundle = parsed;
			await dryRun();
		} catch (e) {
			importError = e instanceof Error ? e.message : String(e);
		}
	}

	async function dryRun() {
		if (!importBundle) return;
		importBusy = true;
		importError = '';
		try {
			const { report } = await importSpace(workspaceId, spaceId, 'dry_run', importBundle);
			importReport = report ?? null;
		} catch (e) {
			importError = e instanceof Error ? e.message : String(e);
		} finally {
			importBusy = false;
		}
	}

	async function applyImport() {
		if (!importBundle || !importReport?.ok) return;
		importApplying = true;
		importError = '';
		try {
			const { report } = await importSpace(workspaceId, spaceId, 'apply', importBundle);
			importReport = report ?? null;
			if (report?.ok) {
				importOpen = false;
				await savedQueries.reload(workspaceId);
			}
		} catch (e) {
			importError = e instanceof Error ? e.message : String(e);
		} finally {
			importApplying = false;
		}
	}

	onMount(load);
</script>

<div class="flex-1 overflow-y-auto p-7">
	{#if loading && !space}
		<Skeleton class="h-24 w-full" />
	{:else if error && !space}
		<ErrorState title="Could not load the space" message={error} onretry={load} />
	{:else if space}
		<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
			<div>
				<p class="text-[11px] text-slate-600 dark:text-slate-300">
					Content › <span class="font-semibold">{space.name}</span>
					{#if currentFolder && folders.find((f) => f.id === currentFolder)}› {folders.find(
							(f) => f.id === currentFolder
						)?.name}{/if}
				</p>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{space.name}</h1>
				<p class="mt-1 text-[12px] text-slate-600 dark:text-slate-300">
					{space.description ?? m.content_no_description()} · {space.visibility === 'workspace'
						? m.content_everyone_in_the_workspace()
						: m.content_restricted()} · {space.my_role}
				</p>
			</div>
			<div class="flex flex-wrap gap-2">
				{#if canManage}
					<Button onclick={() => (accessOpen = true)}>Access</Button>
					<Button onclick={() => (editOpen = true)}>Edit</Button>
				{/if}
				{#if canEdit}
					<Button variant="primary" onclick={() => (folderOpen = true)}>New folder</Button>
				{/if}
				<Button loading={exportBusy} disabled={exportBusy} onclick={doExport}>Export</Button>
				{#if canEdit}
					<Button onclick={openImport}>Import…</Button>
				{/if}
				<Button onclick={() => (window.location.href = '/content/trash')}>Trash</Button>
				{#if canManage && !space.is_default}
					<Button variant="danger" onclick={() => ask('space', space.id, space.name)}>Delete</Button
					>
				{/if}
			</div>
		</div>

		{#if error}
			<p class="mb-2 text-[11px] text-danger" role="alert">{error}</p>
		{/if}
		{#if folderError}
			<p class="mb-2 text-[11px] text-danger" role="alert">{folderError}</p>
		{/if}

		<div class="grid gap-4 lg:grid-cols-[220px_1fr]">
			<nav class="rounded-lg border border-border bg-bg-panel p-2 text-[11px]" aria-label="Folders">
				<button
					type="button"
					class="mb-1 block w-full rounded px-1.5 py-1 text-left {!currentFolder
						? 'bg-bg-toolbar font-semibold'
						: 'hover:bg-bg-toolbar'}"
					onclick={() => selectFolder(null)}
				>
					Space root
				</button>
				{#each tree as node (node.folder.id)}
					<div
						class="flex items-center gap-1 rounded px-1.5 py-1 {currentFolder === node.folder.id
							? 'bg-bg-toolbar font-semibold'
							: 'hover:bg-bg-toolbar'}"
						style="padding-left: {6 + node.depth * 12}px"
					>
						<button
							type="button"
							class="min-w-0 flex-1 truncate text-left"
							onclick={() => selectFolder(node.folder.id)}
						>
							{node.folder.name}
						</button>
						{#if canEdit}
							<button
								type="button"
								class="text-slate-500 hover:text-text-main"
								title="Rename folder"
								onclick={() => {
									renameFolder = node.folder;
									renameFolderName = node.folder.name;
									renameFolderOpen = true;
								}}
							>
								✎
							</button>
							<button
								type="button"
								class="text-slate-500 hover:text-text-main"
								title="Move folder"
								onclick={() => {
									moveFolder = node.folder;
									moveFolderTarget = '';
									moveFolderOpen = true;
								}}
							>
								↗
							</button>
							<button
								type="button"
								class="text-danger"
								title="Delete folder"
								onclick={() => ask('folder', node.folder.id, node.folder.name)}
							>
								×
							</button>
						{/if}
					</div>
				{/each}
			</nav>

			<section class="min-w-0">
				{#if metricsError || modelsError}
					<div class="mb-3">
						<ErrorState
							title={metricsError && modelsError
								? m.content_spaces_could_not_load_metrics_and_models()
								: metricsError
									? m.content_spaces_could_not_load_metrics()
									: m.content_spaces_could_not_load_models()}
							message={metricsError || modelsError}
							onretry={loadSemantic}
						/>
					</div>
				{/if}
				{#if !semanticLoaded && queries.length === 0}
					<Skeleton class="h-24 w-full" />
				{:else if queries.length === 0 && spaceMetrics.length === 0 && spaceModels.length === 0 && !metricsError && !modelsError}
					<EmptyState
						title="Nothing here"
						description={canEdit
							? m.content_spaces_create_a_query_in_the_workbench_or_a_metri()
							: m.content_spaces_this_folder_is_empty()}
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
									<th class="px-3 py-2">Slug</th>
									<th class="px-3 py-2">Updated</th>
									<th class="px-3 py-2 text-right">Actions</th>
								</tr>
							</thead>
							<tbody>
								{#each spaceModels as model (model.id)}
									<tr class="border-b border-border/60 last:border-b-0">
										<td class="px-3 py-1.5"
											><Badge tone="info">{kindEntry('semantic_model').label}</Badge></td
										>
										<td class="px-3 py-1.5">
											<a class="font-medium hover:underline" href={resolve(`/models/${model.id}`)}>
												{model.label}
											</a>
											{#if model.certified}<Badge tone="success">Certified</Badge>{/if}
										</td>
										<td class="px-3 py-1.5 font-mono">{model.slug}</td>
										<td class="px-3 py-1.5"><RelativeTime at={model.updated_at} /></td>
										<td class="px-3 py-1.5 text-right">
											<a class="text-primary hover:underline" href={resolve(`/models/${model.id}`)}
												>Open</a
											>
										</td>
									</tr>
								{/each}
								{#each spaceMetrics as metric (metric.id)}
									<tr class="border-b border-border/60 last:border-b-0">
										<td class="px-3 py-1.5"><Badge tone="info">Metric</Badge></td>
										<td class="px-3 py-1.5">
											<button
												type="button"
												class="font-medium hover:underline"
												onclick={() => openMetric(metric)}
											>
												{metric.label}
											</button>
											{#if metric.certified}<Badge tone="success">Certified</Badge>{/if}
										</td>
										<td class="px-3 py-1.5 font-mono">{metric.slug}</td>
										<td class="px-3 py-1.5"><RelativeTime at={metric.updated_at} /></td>
										<td class="px-3 py-1.5 text-right">
											<button
												type="button"
												class="text-primary hover:underline"
												onclick={() => openMetric(metric)}
											>
												Open
											</button>
										</td>
									</tr>
								{/each}
								{#each queries as query (query.id)}
									<tr class="border-b border-border/60 last:border-b-0">
										<td class="px-3 py-1.5"><Badge tone="neutral">Query</Badge></td>
										<td class="px-3 py-1.5">
											<button
												type="button"
												class="font-medium hover:underline"
												onclick={() => openQuery(query)}
											>
												{query.name}
											</button>
										</td>
										<td class="px-3 py-1.5 font-mono">{query.slug}</td>
										<td class="px-3 py-1.5"
											>{#if query.updated_at}<RelativeTime at={query.updated_at} />{:else}—{/if}</td
										>
										<td class="px-3 py-1.5 text-right">
											<button
												type="button"
												class="text-primary hover:underline"
												onclick={() => openHistory('saved_query', query.id)}
											>
												History
											</button>
											{#if canEdit}
												<button
													type="button"
													class="text-primary hover:underline"
													onclick={() => {
														renameQuery = query;
														renameQueryName = query.name;
													}}
												>
													Rename
												</button>
												<button
													type="button"
													class="ml-2 text-primary hover:underline"
													onclick={() => {
														movingQuery = query;
														movingQueryOpen = true;
													}}
												>
													Move
												</button>
												<button
													type="button"
													class="ml-2 text-danger hover:underline"
													onclick={() => ask('query', query.id, query.name)}
												>
													Delete
												</button>
											{/if}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			</section>
		</div>
	{/if}
</div>

<Modal bind:open={folderOpen} title={m.content_spaces_new_folder()}>
	<div class="flex flex-col gap-3 text-[12px]">
		<Input label={m.common_name()} bind:value={folderName} />
		{#if folderError}<p class="text-danger" role="alert">{folderError}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (folderOpen = false)} disabled={folderSaving}
				>{m.common_cancel()}</Button
			>
			<Button
				variant="primary"
				loading={folderSaving}
				disabled={folderSaving}
				onclick={submitFolder}
			>
				{m.content_spaces_create()}
			</Button>
		</div>
	{/snippet}
</Modal>

<Modal
	bind:open={renameFolderOpen}
	title={m.content_spaces_rename_folder()}
	onclose={() => (renameFolder = null)}
>
	<div class="flex flex-col gap-3 text-[12px]">
		<Input label={m.common_name()} bind:value={renameFolderName} />
		{#if folderError}<p class="text-danger" role="alert">{folderError}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button
				onclick={() => {
					renameFolder = null;
					renameFolderOpen = false;
				}}
				disabled={folderBusy}>{m.common_cancel()}</Button
			>
			<Button
				variant="primary"
				loading={folderBusy}
				disabled={folderBusy}
				onclick={submitRenameFolder}
			>
				{m.common_save()}
			</Button>
		</div>
	{/snippet}
</Modal>

<Modal
	bind:open={moveFolderOpen}
	title={m.content_spaces_move_folder()}
	onclose={() => (moveFolder = null)}
>
	<div class="flex flex-col gap-3 text-[12px]">
		<p class="text-slate-600 dark:text-slate-300">
			{m.content_spaces_the_folder_and_its_subtree_move_to_the_des()}
		</p>
		<Select
			label={m.content_spaces_destination_space()}
			bind:value={moveFolderTarget}
			options={savedQueries.spaces
				.filter((item) => item.id !== spaceId && savedQueries.canEdit(item.id))
				.map((item) => ({ value: item.id, label: item.name }))}
		/>
		{#if folderError}<p class="text-danger" role="alert">{folderError}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button
				onclick={() => {
					moveFolder = null;
					moveFolderOpen = false;
				}}
				disabled={folderBusy}>{m.common_cancel()}</Button
			>
			<Button
				variant="primary"
				loading={folderBusy}
				disabled={folderBusy}
				onclick={submitMoveFolder}
			>
				{m.common_move()}
			</Button>
		</div>
	{/snippet}
</Modal>

<Modal
	bind:open={renameQueryOpen}
	title={m.content_spaces_rename_query()}
	onclose={() => (renameQuery = null)}
>
	<div class="flex flex-col gap-3 text-[12px]">
		<Input label={m.common_name()} bind:value={renameQueryName} />
		{#if folderError}<p class="text-danger" role="alert">{folderError}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button
				onclick={() => {
					renameQuery = null;
					renameQueryOpen = false;
				}}
				disabled={folderBusy}>{m.common_cancel()}</Button
			>
			<Button
				variant="primary"
				loading={folderBusy}
				disabled={folderBusy}
				onclick={submitRenameQuery}
			>
				{m.common_save()}
			</Button>
		</div>
	{/snippet}
</Modal>

<MoveToSpaceDialog bind:open={movingQueryOpen} query={movingQuery} />
<SpaceAccessDialog bind:open={accessOpen} {space} />
<SpaceDialog bind:open={editOpen} {space} ondone={() => savedQueries.reload(workspaceId)} />

<HistoryDrawer
	bind:open={historyOpen}
	kind={historyKind}
	entityId={historyId}
	{workspaceId}
	onrestored={() => savedQueries.reload(workspaceId)}
/>

<Modal bind:open={importOpen} title={m.content_spaces_import_space_bundle()}>
	<div class="flex flex-col gap-3 text-[12px]">
		<input
			type="file"
			accept=".json,application/json"
			class="block w-full text-[11px]"
			onchange={readImport}
		/>
		{#if importName}
			<p class="font-mono text-[11px]">{importName}</p>
		{/if}
		{#if importBusy}
			<p class="text-[11px] text-slate-600 dark:text-slate-300">
				{m.content_spaces_running_the_dry_run()}
			</p>
		{/if}
		{#if importError}
			<p class="text-danger" role="alert">{importError}</p>
		{/if}
		{#if importReport}
			<div class="flex flex-wrap gap-1">
				{#each ['CREATE', 'UPDATE', m.content_spaces_no_changes(), 'ERROR'] as action (action)}
					<Badge tone={action === 'ERROR' ? 'danger' : action === 'CREATE' ? 'success' : 'neutral'}>
						{importReport.items.filter((item) => item.action === action).length}
						{action}
					</Badge>
				{/each}
			</div>
			<div class="max-h-64 overflow-y-auto rounded border border-border">
				<table class="w-full text-left text-[11px]">
					<thead class="text-[10px] uppercase text-slate-600 dark:text-slate-300">
						<tr>
							<th class="px-2 py-1">{m.common_kind()}</th>
							<th class="px-2 py-1">{m.content_spaces_title()}</th>
							<th class="px-2 py-1">{m.common_action()}</th>
							<th class="px-2 py-1">{m.content_spaces_message()}</th>
						</tr>
					</thead>
					<tbody>
						{#each importReport.items as item (item.kind + item.slug)}
							<tr class="border-t border-border/60">
								<td class="px-2 py-1">{item.kind}</td>
								<td class="px-2 py-1">{item.title ?? item.slug}</td>
								<td class="px-2 py-1">
									<Badge
										tone={item.action === 'ERROR'
											? 'danger'
											: item.action === 'CREATE'
												? 'success'
												: 'neutral'}
									>
										{item.action}
									</Badge>
								</td>
								<td class="px-2 py-1">{item.message ?? (item.warnings ?? []).join(', ')}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<p class="text-[10px] text-slate-600 dark:text-slate-300">
				{m.content_spaces_items_in_this_space_that_are_not_in_the_fi()}
			</p>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (importOpen = false)} disabled={importApplying}
				>{m.common_cancel()}</Button
			>
			<Button
				variant="primary"
				loading={importApplying}
				disabled={importApplying ||
					!importReport?.ok ||
					importReport.items.some((item) => item.action === 'ERROR')}
				onclick={applyImport}
			>
				{m.common_apply()}
			</Button>
		</div>
	{/snippet}
</Modal>

<ConfirmDialog
	bind:open={deleteOpen}
	title={pending?.kind === 'space'
		? m.components_savedquerieslist_delete_space()
		: pending?.kind === 'folder'
			? m.content_spaces_delete_folder()
			: m.content_spaces_delete_query()}
	resourceName={pending?.name ?? ''}
	confirmLabel="Delete"
	consequences={[
		pending?.kind === 'space'
			? m.content_spaces_the_space_content_and_its_access_list_are()
			: pending?.kind === 'folder'
				? m.content_spaces_subfolders_are_deleted_and_their_items_mov()
				: m.content_spaces_deleted_items_can_be_restored_from_trash()
	]}
	onconfirm={confirmDelete}
/>
