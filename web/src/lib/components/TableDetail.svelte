<script lang="ts">
	import { tableFromIPC } from 'apache-arrow';
	import { apiFetch, client } from '$lib/api/client';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { editor } from '$lib/stores/editor.svelte';
	import { toasts } from '$lib/ui/toast.svelte';
	import { poll } from '$lib/util/poll';
	import { loadTable, type MetadataTableDetail, type TableRef } from '$lib/metadata/client';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Loader from '$lib/ui/Skeleton.svelte';
	import MarkdownView from '$lib/ui/patterns/MarkdownView.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import CopyButton from '$lib/ui/patterns/CopyButton.svelte';

	let { table }: { table: TableRef | null } = $props();

	const workspaceId = $derived(workspaceStore.current?.id ?? '');

	let detail = $state<MetadataTableDetail | null>(null);
	let loading = $state(false);
	let error = $state('');
	let bindingId = $state('');
	let colSearch = $state('');
	let expandedCol = $state<string | null>(null);
	let lastTableId = '';
	let retryTick = $state(0);

	// A new table selects its first binding; picking an engine keeps the table
	// and only changes the rendering binding (types, qualified name, preview).
	$effect(() => {
		const id = table?.tableId ?? '';
		if (id === lastTableId) return;
		lastTableId = id;
		bindingId = table?.bindings[0]?.binding_id ?? '';
		colSearch = '';
		expandedCol = null;
	});

	$effect(() => {
		const ref = table;
		const binding = bindingId;
		// Retry is driven by a tick so re-running the same read re-issues it.
		void retryTick;
		if (!ref) {
			detail = null;
			error = '';
			loading = false;
			return;
		}
		// Wait for the reset effect when the table just changed.
		if (ref.tableId !== lastTableId) return;
		if (!workspaceId) return;

		let cancelled = false;
		loading = true;
		error = '';
		loadTable(workspaceId, ref.tableId, binding || undefined)
			.then((loaded) => {
				if (!cancelled) detail = loaded;
			})
			.catch((e) => {
				if (!cancelled) error = e instanceof Error ? e.message : String(e);
			})
			.finally(() => {
				if (!cancelled) loading = false;
			});
		return () => {
			cancelled = true;
		};
	});

	const filteredColumns = $derived(
		(detail?.columns ?? []).filter((column) => {
			if (!colSearch) return true;
			return column.name.toLowerCase().includes(colSearch.toLowerCase());
		})
	);

	const hasBindings = $derived((table?.bindings.length ?? 0) > 1);

	function insertName() {
		if (!detail?.qualified_name) return;
		editor.insert(detail.qualified_name);
		toasts.success('Inserted into the editor');
	}

	// ── Preview ──────────────────────────────────────────────────────────
	let previewOpen = $state(false);
	let previewLoading = $state(false);
	let previewError = $state('');
	let previewColumns = $state<string[]>([]);
	let previewRows = $state<unknown[][]>([]);

	async function runPreview() {
		if (!detail?.preview_sql || !table) return;
		previewOpen = true;
		previewLoading = true;
		previewError = '';
		previewColumns = [];
		previewRows = [];
		try {
			const { data, error: startError } = await client.POST('/workspaces/{id}/executions', {
				params: { path: { id: workspaceId } },
				body: {
					workspace_id: workspaceId,
					sql: detail.preview_sql,
					target: detail.binding_id ? { binding_id: detail.binding_id } : undefined
				}
			});
			if (startError || !data) throw new Error('Could not start the preview');

			const executionId = data.id;
			const controller = poll(
				async () => {
					const { data: execution } = await client.GET(
						'/workspaces/{id}/executions/{execution_id}',
						{ params: { path: { id: workspaceId, execution_id: executionId } } }
					);
					return execution;
				},
				{
					until: (execution) =>
						!!execution && ['finished', 'failed', 'cancelled'].includes(execution.status),
					intervals: [400, 800, 1500, 2500]
				}
			);
			const execution = await controller.done;
			if (!execution || typeof execution === 'string') {
				throw new Error('The preview did not finish in time');
			}
			if (execution.status !== 'finished') {
				throw new Error(execution.status === 'cancelled' ? 'Preview cancelled' : 'Preview failed');
			}

			const response = await apiFetch(
				`/workspaces/${workspaceId}/executions/${executionId}/results`
			);
			if (!response.ok) throw new Error(`Could not load the preview (${response.status})`);
			const buffer = await response.arrayBuffer();
			if (buffer.byteLength === 0) {
				previewColumns = [];
				previewRows = [];
				return;
			}
			const decoded = tableFromIPC(new Uint8Array(buffer));
			previewColumns = decoded.schema.fields.map((field) => field.name);
			previewRows = decoded
				.toArray()
				.slice(0, 100)
				.map((row) => previewColumns.map((name) => (row as Record<string, unknown>)[name]));
		} catch (e) {
			previewError = e instanceof Error ? e.message : String(e);
		} finally {
			previewLoading = false;
		}
	}

	function typeClass(type: string): string {
		const t = type.toLowerCase();
		if (t.includes('char') || t.includes('text') || t.includes('uuid') || t.includes('json'))
			return 'type-string';
		if (
			t.includes('int') ||
			t.includes('float') ||
			t.includes('double') ||
			t.includes('decimal') ||
			t.includes('numeric')
		)
			return 'type-numeric';
		if (t.includes('time') || t.includes('date')) return 'type-temporal';
		if (t.includes('bool')) return 'type-bool';
		return 'type-other';
	}
</script>

<div class="flex h-full flex-col overflow-hidden" data-testid="table-detail">
	{#if !table}
		<EmptyState
			title="Select a table"
			description="Browse the catalogs to inspect a table, its columns and its documentation."
		/>
	{:else if loading && !detail}
		<div class="space-y-3 p-4" aria-label="Loading table">
			<Loader class="h-5 w-1/2" />
			<Loader class="h-4 w-2/3" />
			<Loader class="h-4 w-3/4" />
		</div>
	{:else if error}
		<ErrorState title="Could not load the table" message={error} onretry={() => retryTick++} />
	{:else if detail}
		<div class="shrink-0 border-b border-border px-4 py-3">
			<div class="flex flex-wrap items-center gap-2">
				<span class="font-mono text-[15px] font-bold">{detail.name}</span>
				<Badge tone="neutral">{detail.kind.replace('_', ' ')}</Badge>
				{#if detail.removed_at}
					<Badge tone="warning">removed from the source</Badge>
				{/if}
			</div>
			<div class="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-text-muted">
				<span class="font-mono">{[table.catalogName, ...table.namespace].join('.')}</span>
				<span aria-hidden="true">·</span>
				<span>
					{#if detail.freshness.metadata_synced_at}
						synced <RelativeTime at={detail.freshness.metadata_synced_at} />
					{:else}
						never synced
					{/if}
				</span>
			</div>

			<div class="mt-3 flex flex-wrap items-center gap-2">
				{#if hasBindings}
					<label class="flex items-center gap-1.5 text-[11px] text-text-muted">
						<span>Engine</span>
						<select
							aria-label="Engine binding"
							class="rounded border border-border bg-bg-panel px-1.5 py-1 text-[11px]"
							value={bindingId}
							onchange={(e) => (bindingId = (e.currentTarget as HTMLSelectElement).value)}
						>
							{#each table.bindings as binding (binding.binding_id)}
								<option value={binding.binding_id}>
									{binding.engine_name} · {binding.engine_catalog_name}
								</option>
							{/each}
						</select>
					</label>
				{/if}

				<Button variant="secondary" size="sm" disabled={!detail.preview_sql} onclick={runPreview}
					>Preview</Button
				>
				<Button variant="secondary" size="sm" disabled={!detail.qualified_name} onclick={insertName}
					>Insert name</Button
				>
				{#if detail.qualified_name}
					<span class="inline-flex items-center gap-1 text-[11px] text-text-muted">
						<code class="font-mono">{detail.qualified_name}</code>
						<CopyButton value={detail.qualified_name} label="qualified name" />
					</span>
				{/if}
			</div>
		</div>

		<div class="flex-1 overflow-y-auto">
			{#if detail.doc?.description}
				<div class="border-b border-border px-4 py-3">
					<MarkdownView source={detail.doc.description} />
				</div>
			{/if}

			{#if detail.doc?.tags?.length}
				<div class="flex flex-wrap gap-1 border-b border-border px-4 py-2">
					{#each detail.doc.tags as tag (tag)}
						<Badge tone="info">{tag}</Badge>
					{/each}
				</div>
			{/if}

			{#if Object.keys(detail.properties).length > 0}
				<div class="border-b border-border px-4 py-3">
					<p class="mb-2 text-[10px] font-bold uppercase tracking-wide text-text-muted">
						Properties
					</p>
					<dl class="grid grid-cols-[minmax(90px,auto)_1fr] gap-x-3 gap-y-1 text-[12px]">
						{#each Object.entries(detail.properties) as [key, value] (key)}
							<dt class="text-text-muted">{key}</dt>
							<dd class="break-all font-mono">{value}</dd>
						{/each}
					</dl>
				</div>
			{/if}

			<div class="px-3 py-2">
				<div class="mb-2 flex items-center gap-2 px-1">
					<p class="text-[10px] font-bold uppercase tracking-wide text-text-muted">
						Columns <span class="font-normal">({detail.columns.length})</span>
					</p>
					<input
						type="text"
						placeholder="Filter columns..."
						aria-label="Filter columns"
						bind:value={colSearch}
						class="ml-auto w-40 rounded border border-border bg-bg-panel px-2 py-1 text-[11px] outline-none focus:border-primary"
					/>
				</div>

				{#if filteredColumns.length === 0}
					<EmptyState title="No columns" description="This table has no columns in the snapshot." />
				{:else}
					{#each filteredColumns as column (column.ordinal)}
						<button
							class="flex w-full flex-col gap-1 border-b border-border px-1 py-2 text-left last:border-b-0 hover:bg-slate-50 dark:hover:bg-[#1c2128]"
							onclick={() => (expandedCol = expandedCol === column.name ? null : column.name)}
						>
							<div class="flex items-center gap-1.5">
								<span
									class="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap font-mono text-xs font-semibold"
									>{column.name}</span
								>
								{#if column.nullable === false}
									<Badge tone="neutral">not null</Badge>
								{/if}
								<span
									class="type-chip inline-flex shrink-0 items-center rounded border border-transparent px-1.5 py-px font-mono text-[10px] font-semibold {typeClass(
										column.display_type || column.native_type
									)}"
								>
									{column.display_type || column.native_type}
								</span>
							</div>
							{#if column.comment}
								<span class="text-[11px] text-text-muted">{column.comment}</span>
							{/if}
							{#if expandedCol === column.name && column.doc?.description}
								<div class="pt-1">
									<MarkdownView source={column.doc.description} />
								</div>
							{/if}
						</button>
					{/each}
				{/if}
			</div>
		</div>
	{/if}
</div>

{#if previewOpen}
	<div
		class="fixed inset-0 z-[100] flex items-center justify-center bg-black/30 p-6 backdrop-blur-[2px]"
		role="dialog"
		aria-modal="true"
		aria-label="Table preview"
		tabindex="-1"
		onclick={(e) => e.target === e.currentTarget && (previewOpen = false)}
	>
		<div
			class="flex max-h-[85vh] w-full max-w-[900px] flex-col rounded-xl border border-border bg-bg-panel shadow-[0_24px_64px_rgba(0,0,0,.2)]"
		>
			<div class="flex items-center gap-2 border-b border-border px-5 py-[14px]">
				<h2 class="flex-1 text-sm font-semibold">Preview · {detail?.name}</h2>
				<button
					type="button"
					class="flex h-[26px] w-[26px] items-center justify-center rounded-md text-text-muted hover:bg-bg-toolbar hover:text-text-main"
					aria-label="Close preview"
					onclick={() => (previewOpen = false)}>×</button
				>
			</div>
			<div class="flex-1 overflow-auto p-4">
				{#if previewLoading}
					<div class="space-y-2" aria-label="Running preview">
						<Loader class="h-4 w-2/3" />
						<Loader class="h-4 w-3/4" />
						<Loader class="h-4 w-1/2" />
					</div>
				{:else if previewError}
					<ErrorState title="Preview failed" message={previewError} onretry={runPreview} />
				{:else if previewColumns.length === 0}
					<EmptyState title="No rows" description="The preview returned no rows." />
				{:else}
					<table class="w-full border-separate border-spacing-0 text-left text-xs">
						<thead>
							<tr>
								{#each previewColumns as column (column)}
									<th
										class="sticky top-0 border-b border-border bg-bg-toolbar px-3 py-2 text-[11px] font-semibold uppercase text-text-muted"
										>{column}</th
									>
								{/each}
							</tr>
						</thead>
						<tbody>
							{#each previewRows as row, index (index)}
								<tr class="hover:[&>td]:bg-bg-toolbar">
									{#each row as cell, cellIndex (cellIndex)}
										<td class="whitespace-nowrap border-b border-border px-3 py-1.5 font-mono">
											{cell === null ? 'NULL' : String(cell)}
										</td>
									{/each}
								</tr>
							{/each}
						</tbody>
					</table>
				{/if}
			</div>
		</div>
	</div>
{/if}

<style>
	.type-string {
		color: #0f766e;
		background: #f0fdf9;
		border-color: #99f6e4;
	}
	.type-numeric {
		color: #7c3aed;
		background: #f5f3ff;
		border-color: #ddd6fe;
	}
	.type-temporal {
		color: #b45309;
		background: #fffbeb;
		border-color: #fde68a;
	}
	.type-bool {
		color: #dc2626;
		background: #fef2f2;
		border-color: #fecaca;
	}
	.type-other {
		color: #475569;
		background: #f8fafc;
		border-color: #e2e8f0;
	}
	.dark .type-other {
		color: #94a3b8;
		background: #1c2128;
		border-color: #30363d;
	}
</style>
