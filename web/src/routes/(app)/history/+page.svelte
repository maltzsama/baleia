<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiErrorMessage, client } from '$lib/api/client';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { editor } from '$lib/stores/editor.svelte';
	import { historyQuery, parseHistoryFilters } from '$lib/history/filters';
	import { toasts } from '$lib/ui/toast.svelte';
	import type { components } from '$lib/api/schema';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Drawer from '$lib/ui/Drawer.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import CodeBlock from '$lib/ui/patterns/CodeBlock.svelte';
	import CopyButton from '$lib/ui/patterns/CopyButton.svelte';
	import Duration from '$lib/ui/patterns/Duration.svelte';
	import EngineBadge from '$lib/ui/patterns/EngineBadge.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import ResultsPanel from '$lib/components/ResultsPanel.svelte';

	type Execution = components['schemas']['Execution'];
	type QueryStatement = components['schemas']['QueryStatement'];

	const workspaceId = $derived(workspaceStore.current?.id ?? '');

	// Filters live in the URL so a reload or a shared link restores the view.
	const filters = $derived(parseHistoryFilters(page.url));
	const status = $derived(filters.status);
	const q = $derived(filters.q);
	const from = $derived(filters.from);
	const to = $derived(filters.to);

	let searchTimer: ReturnType<typeof setTimeout> | undefined;

	function setParam(key: string, value: string) {
		const url = new URL(page.url);
		if (value) url.searchParams.set(key, value);
		else url.searchParams.delete(key);
		// The URL is dynamic (query string), so resolve() cannot type it (#29).
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(url, { replaceState: true, keepFocus: true, noScroll: true });
	}

	function onSearchInput(value: string) {
		if (searchTimer) clearTimeout(searchTimer);
		searchTimer = setTimeout(() => setParam('q', value.trim()), 300);
	}

	// ── Page data ────────────────────────────────────────────────────────
	let items = $state<Execution[]>([]);
	let nextCursor = $state<string | null>(null);
	let loading = $state(false);
	let loadingMore = $state(false);
	let error = $state('');
	let requestSeq = 0;

	const filterKey = $derived(`${workspaceId}|${status}|${q}|${from}|${to}`);

	$effect(() => {
		const key = filterKey;
		void key;
		if (!workspaceId) return;
		const seq = ++requestSeq;
		items = [];
		nextCursor = null;
		loading = true;
		error = '';
		void fetchPage(undefined, seq);
	});

	async function fetchPage(cursor: string | undefined, seq: number) {
		const query = historyQuery({ status, q, from, to }, cursor);
		try {
			const { data, error: readError } = await client.GET('/workspaces/{id}/executions', {
				params: { path: { id: workspaceId }, query }
			});
			if (seq !== requestSeq) return;
			if (readError || !data) {
				throw new Error(apiErrorMessage(readError, 'Failed to load the history'));
			}
			items = cursor ? [...items, ...data.data] : data.data;
			nextCursor = data.next_cursor ?? null;
		} catch (e) {
			if (seq === requestSeq) error = e instanceof Error ? e.message : String(e);
		} finally {
			if (seq === requestSeq) {
				loading = false;
				loadingMore = false;
			}
		}
	}

	async function loadMore() {
		if (!nextCursor || loadingMore) return;
		loadingMore = true;
		await fetchPage(nextCursor, requestSeq);
	}

	// ── Detail drawer ────────────────────────────────────────────────────
	let selected = $state<Execution | null>(null);
	let drawerOpen = $state(false);
	let statements = $state<QueryStatement[]>([]);
	let statementsLoading = $state(false);
	let statementsError = $state('');
	let resultsOpen = $state(false);
	let resultsPosition = $state<number | undefined>(undefined);

	async function openDetails(execution: Execution) {
		selected = execution;
		drawerOpen = true;
		statements = [];
		statementsError = '';
		statementsLoading = true;
		try {
			const { data, error: readError } = await client.GET(
				'/workspaces/{id}/executions/{execution_id}/statements',
				{ params: { path: { id: workspaceId, execution_id: execution.id } } }
			);
			if (readError || !data) {
				throw new Error(apiErrorMessage(readError, 'Failed to load the statements'));
			}
			statements = data;
		} catch (e) {
			statementsError = e instanceof Error ? e.message : String(e);
		} finally {
			statementsLoading = false;
		}
	}

	function firstLine(sql: string): string {
		return sql.trim().split('\n')[0]?.slice(0, 60) || 'Untitled';
	}

	function openInEditor(run: boolean) {
		if (!selected) return;
		const target = selected.target;
		const name = `History · ${firstLine(selected.sql)}`;
		if (run) {
			if (!target?.binding_id) {
				toasts.error('This execution has no target to re-run on');
				return;
			}
			editor.openExecution({
				name,
				sql: selected.sql,
				bindingId: target.binding_id,
				schema: target.schema ?? ''
			});
		} else {
			editor.addTab({
				name,
				sql: selected.sql,
				bindingId: target?.binding_id ?? '',
				schema: target?.schema ?? ''
			});
		}
		void goto(resolve('/sql'));
	}

	function showResults(position: number) {
		resultsPosition = position;
		resultsOpen = true;
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mx-auto max-w-6xl">
		<div class="mb-5 flex flex-wrap items-end justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">Query history</h1>
				<p class="mt-1 text-[13px] text-text-muted">
					Every execution in this workspace, searchable by SQL and filterable by status and time.
				</p>
			</div>
		</div>

		<div
			class="mb-4 flex flex-wrap items-end gap-3 rounded-lg border border-border bg-bg-panel p-3"
		>
			<label class="flex flex-col gap-1 text-[11px] font-medium text-text-muted">
				<span>Status</span>
				<select
					aria-label="Status"
					value={status}
					onchange={(e) => setParam('status', (e.currentTarget as HTMLSelectElement).value)}
					class="rounded border border-border bg-bg-panel px-2 py-1 text-xs text-text-main"
				>
					<option value="">All</option>
					<option value="running">Running</option>
					<option value="finished">Finished</option>
					<option value="failed">Failed</option>
					<option value="cancelled">Cancelled</option>
				</select>
			</label>
			<label class="flex flex-col gap-1 text-[11px] font-medium text-text-muted">
				<span>From</span>
				<input
					type="datetime-local"
					aria-label="From"
					value={from}
					onchange={(e) => setParam('from', (e.currentTarget as HTMLInputElement).value)}
					class="rounded border border-border bg-bg-panel px-2 py-1 text-xs text-text-main"
				/>
			</label>
			<label class="flex flex-col gap-1 text-[11px] font-medium text-text-muted">
				<span>To</span>
				<input
					type="datetime-local"
					aria-label="To"
					value={to}
					onchange={(e) => setParam('to', (e.currentTarget as HTMLInputElement).value)}
					class="rounded border border-border bg-bg-panel px-2 py-1 text-xs text-text-main"
				/>
			</label>
			<label
				class="flex min-w-[220px] flex-1 flex-col gap-1 text-[11px] font-medium text-text-muted"
			>
				<span>SQL contains</span>
				<input
					type="search"
					aria-label="SQL contains"
					placeholder="orders, revenue, …"
					value={q}
					oninput={(e) => onSearchInput((e.currentTarget as HTMLInputElement).value)}
					class="rounded border border-border bg-bg-panel px-2 py-1 text-xs text-text-main"
				/>
			</label>
		</div>

		<div class="rounded-lg border border-border bg-bg-panel">
			{#if loading}
				<div class="space-y-2 p-4" aria-label="Loading history">
					<Skeleton class="h-5 w-full" />
					<Skeleton class="h-5 w-5/6" />
					<Skeleton class="h-5 w-4/5" />
				</div>
			{:else if error}
				<ErrorState
					title="Could not load the history"
					message={error}
					onretry={() => {
						const seq = ++requestSeq;
						loading = true;
						void fetchPage(undefined, seq);
					}}
				/>
			{:else if items.length === 0}
				<EmptyState
					title="No executions"
					description="No execution matches these filters. Run a query or clear the filters."
				/>
			{:else}
				<div class="overflow-x-auto">
					<table class="w-full text-left text-xs">
						<thead>
							<tr
								class="border-b border-border bg-bg-toolbar text-[10px] uppercase tracking-wide text-text-muted"
							>
								<th class="px-3 py-2 font-semibold">Status</th>
								<th class="px-3 py-2 font-semibold">Started</th>
								<th class="px-3 py-2 font-semibold">Duration</th>
								<th class="px-3 py-2 text-right font-semibold">Rows</th>
								<th class="px-3 py-2 font-semibold">User</th>
								<th class="px-3 py-2 font-semibold">Engine</th>
								<th class="px-3 py-2 font-semibold">SQL</th>
							</tr>
						</thead>
						<tbody>
							{#each items as execution (execution.id)}
								<tr
									class="cursor-pointer border-b border-border last:border-b-0 hover:bg-bg-toolbar"
									tabindex="0"
									onclick={() => openDetails(execution)}
									onkeydown={(e) => {
										if (e.key === 'Enter' || e.key === ' ') {
											e.preventDefault();
											openDetails(execution);
										}
									}}
								>
									<td class="px-3 py-2">
										<StatusBadge kind="execution" status={execution.status} />
									</td>
									<td class="whitespace-nowrap px-3 py-2 text-text-muted">
										{#if execution.started_at}
											<RelativeTime at={execution.started_at} />
										{:else}
											—
										{/if}
									</td>
									<td class="px-3 py-2">
										{#if execution.duration_ms != null}
											<Duration ms={execution.duration_ms} />
										{:else}
											<span class="text-text-muted">—</span>
										{/if}
									</td>
									<td class="px-3 py-2 text-right font-mono text-text-muted">
										{execution.rows_returned?.toLocaleString() ?? '—'}
									</td>
									<td class="px-3 py-2 text-text-muted">{execution.user_name ?? '—'}</td>
									<td class="px-3 py-2">
										{#if execution.target?.engine_name && execution.target.engine_kind}
											<span class="flex flex-col gap-0.5">
												<EngineBadge
													engine={{
														name: execution.target.engine_name,
														kind: execution.target.engine_kind
													}}
												/>
												<span class="text-[10px] text-text-muted">
													{execution.target.catalog_name}{execution.target.schema
														? `.${execution.target.schema}`
														: ''}
												</span>
											</span>
										{:else}
											<span class="text-text-muted">—</span>
										{/if}
									</td>
									<td class="max-w-[360px] px-3 py-2">
										<span
											class="block overflow-hidden text-ellipsis whitespace-nowrap font-mono text-[11px]"
											>{firstLine(execution.sql)}</span
										>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
				{#if nextCursor}
					<div class="flex justify-center border-t border-border p-3">
						<Button variant="secondary" size="sm" disabled={loadingMore} onclick={loadMore}>
							{loadingMore ? 'Loading…' : 'Load more'}
						</Button>
					</div>
				{/if}
			{/if}
		</div>
	</div>
</div>

<Drawer bind:open={drawerOpen} title="Execution details">
	{#if selected}
		<div class="flex flex-col gap-4 p-5">
			<div class="flex flex-wrap items-center gap-2 text-xs text-text-muted">
				<StatusBadge kind="execution" status={selected.status} />
				{#if selected.user_name}<span>by {selected.user_name}</span>{/if}
				{#if selected.started_at}
					<span>started <RelativeTime at={selected.started_at} /></span>
				{/if}
				{#if selected.duration_ms != null}
					<Duration ms={selected.duration_ms} />
				{/if}
				{#if selected.rows_returned != null}
					<span>{selected.rows_returned.toLocaleString()} rows</span>
				{/if}
			</div>

			{#if selected.target?.engine_name && selected.target.engine_kind}
				<div
					class="rounded-md border border-border bg-bg-toolbar px-3 py-2 text-[11px] text-text-muted"
				>
					ran on
					<span class="font-medium text-text-main">{selected.target.engine_name}</span> ·
					<span class="font-mono"
						>{selected.target.catalog_name}{selected.target.schema
							? `.${selected.target.schema}`
							: ''}</span
					>
					{#if selected.target.read_only}<Badge tone="warning">read-only</Badge>{/if}
				</div>
			{/if}

			<CodeBlock code={selected.sql} language="sql" maxHeight="240px" />

			<div class="flex flex-wrap gap-2">
				<Button variant="secondary" size="sm" onclick={() => openInEditor(false)}
					>Open in editor</Button
				>
				<Button
					variant="primary"
					size="sm"
					disabled={!selected.target?.binding_id}
					onclick={() => openInEditor(true)}>Re-run</Button
				>
				<CopyButton value={selected.sql} label="SQL" />
			</div>

			<div>
				<h3 class="mb-2 text-[10px] font-bold uppercase tracking-wide text-text-muted">
					Statements
				</h3>
				{#if statementsLoading}
					<div class="space-y-2" aria-label="Loading statements">
						<Skeleton class="h-4 w-full" />
						<Skeleton class="h-4 w-2/3" />
					</div>
				{:else if statementsError}
					<ErrorState title="Could not load statements" message={statementsError} />
				{:else if statements.length === 0}
					<p class="text-[12px] text-text-muted">No statements recorded.</p>
				{:else}
					<div class="flex flex-col gap-2">
						{#each statements as statement (statement.id)}
							<div class="rounded-md border border-border px-3 py-2">
								<div class="flex flex-wrap items-center gap-2 text-[11px] text-text-muted">
									<span class="font-semibold text-text-main">#{statement.position}</span>
									<StatusBadge kind="statement" status={statement.status} />
									{#if statement.duration_ms != null}
										<Duration ms={statement.duration_ms} />
									{/if}
									{#if statement.rows_returned != null}
										<span>{statement.rows_returned.toLocaleString()} rows</span>
									{/if}
									{#if statement.truncated}
										<Badge tone="warning">truncated</Badge>
									{/if}
									{#if (statement.result_sets?.length ?? 0) > 0}
										<button
											type="button"
											class="ml-auto rounded px-1.5 py-0.5 text-primary hover:bg-bg-toolbar"
											onclick={() => showResults(statement.position)}
										>
											Results
										</button>
									{/if}
								</div>
								{#if statement.error_message}
									<p class="mt-1 text-[11px] text-red-600">{statement.error_message}</p>
								{/if}
								<p
									class="mt-1 overflow-hidden text-ellipsis whitespace-nowrap font-mono text-[11px] text-text-muted"
								>
									{firstLine(statement.sql)}
								</p>
							</div>
						{/each}
					</div>
				{/if}
			</div>
		</div>
	{/if}
</Drawer>

{#if resultsOpen && selected}
	<div
		class="fixed inset-0 z-[110] flex items-center justify-center bg-black/40 p-6"
		role="dialog"
		aria-modal="true"
		aria-label="Execution results"
		tabindex="-1"
	>
		<div
			class="flex h-[80vh] w-full max-w-[1100px] flex-col overflow-hidden rounded-xl border border-border bg-bg-panel"
		>
			<header class="flex shrink-0 items-center gap-2 border-b border-border px-4 py-3">
				<h2 class="flex-1 text-sm font-semibold">Results</h2>
				<button
					type="button"
					class="flex h-6 w-6 items-center justify-center rounded text-text-muted hover:bg-bg-toolbar hover:text-text-main"
					aria-label="Close results"
					onclick={() => (resultsOpen = false)}>×</button
				>
			</header>
			<ResultsPanel executionId={selected.id} enabled={true} initialPosition={resultsPosition} />
		</div>
	</div>
{/if}
