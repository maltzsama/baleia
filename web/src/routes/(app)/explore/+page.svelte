<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onDestroy, onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { apiErrorMessage, client } from '$lib/api/client';
	import { poll, type PollController } from '$lib/util/poll';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import { editor, type EditorTab } from '$lib/stores/editor.svelte';
	import ResultsPanel from '$lib/components/ResultsPanel.svelte';
	import SaveQueryDialog from '$lib/workbench/SaveQueryDialog.svelte';
	import ExploreFieldTree from '$lib/semantic/ExploreFieldTree.svelte';
	import {
		MAX_EXPLORE_LIMIT,
		SemanticRequestError,
		allFields,
		bindingOptions,
		canonicalQuery,
		compileExplore,
		decodeShare,
		encodeShare,
		fieldTree,
		filterGroups,
		listModels,
		offendingFields,
		operatorsFor,
		parseFilterValues,
		withExploreComment,
		type BindingOption,
		type ExploreCompileResult,
		type ExploreQuery,
		type FieldNode,
		type FilterOp,
		type SemanticModel
	} from '$lib/semantic/explore';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import Tabs from '$lib/ui/Tabs.svelte';
	import CodeBlock from '$lib/ui/patterns/CodeBlock.svelte';
	import CopyButton from '$lib/ui/patterns/CopyButton.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import { toasts } from '$lib/ui/toast.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');

	let models = $state<SemanticModel[]>([]);
	let loading = $state(true);
	let loadError = $state('');
	let query = $state<ExploreQuery>({ model: '' });
	// Raw text of each filter's value input; the query holds the typed values.
	let filterText = $state<string[]>([]);
	let search = $state('');
	let bindings = $state<BindingOption[]>([]);
	let bindingId = $state('');
	let tab = $state('results');

	let compiled = $state<ExploreCompileResult | null>(null);
	let compileError = $state<SemanticRequestError | null>(null);
	let compiling = $state(false);
	let compileSeq = 0;
	let compileTimer: ReturnType<typeof setTimeout> | undefined;

	let executionId = $state('');
	let runStatus = $state<'idle' | 'running' | 'finished' | 'failed' | 'cancelled' | 'timeout'>(
		'idle'
	);
	// The engine's failure as the error catalog reads it (FD7): a code for the
	// headline, the native message only as a detail.
	let runError = $state<{ code: string; error_native?: string } | null>(null);
	let poller: PollController<string> | undefined;
	let saveOpen = $state(false);

	const base = $derived(models.find((model) => model.slug === query.model));
	const groups = $derived(fieldTree(models, query.model));
	const visibleGroups = $derived(filterGroups(groups, search));
	const fields = $derived(allFields(groups));
	const selected = $derived([...(query.dimensions ?? []), ...(query.metrics ?? [])]);
	const hasSelection = $derived(selected.length > 0);
	const grainOptions = $derived(
		[...new Set((query.dimensions ?? []).flatMap((name) => fields.get(name)?.grains ?? []))].map(
			(grain) => ({ value: grain, label: grain })
		)
	);
	const badFields = $derived(
		compileError ? offendingFields(compileError.message, [...selected, ...fieldNames()]) : []
	);
	const shareUrl = $derived(
		query.model && typeof location !== 'undefined'
			? `${location.origin}/explore?q=${encodeShare(query)}`
			: ''
	);
	// SaveQueryDialog reads the name, SQL and target of an editor tab; Explore
	// has no tab of its own, so it hands the dialog these fields.
	const saveTab = $derived({
		name: base?.label ?? m.explore_title(),
		sql: compiled ? withExploreComment(compiled.sql, query) : '',
		savedRef: null,
		bindingId,
		schema: ''
	} as unknown as EditorTab);

	function fieldNames(): string[] {
		return [
			...(query.filters ?? []).map((f) => f.field),
			...(query.sorts ?? []).map((s) => s.field)
		];
	}

	// ── query state ─────────────────────────────────────────────────────────────

	function commit(next: ExploreQuery) {
		query = canonicalQuery(next);
		const url = query.model
			? `${resolve('/explore')}?q=${encodeShare(query)}`
			: resolve('/explore');
		// The query string is the explore query itself, not a route.
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(url, { replaceState: true, keepFocus: true, noScroll: true });
		scheduleCompile();
	}

	function selectModel(slug: string) {
		filterText = [];
		compiled = null;
		compileError = null;
		resetRun();
		// The previous model's binding belongs to another catalog: compile only
		// once this model's bindings are known.
		bindingId = '';
		bindings = [];
		commit({ model: slug });
		void loadBindings().then(scheduleCompile);
	}

	function pickGrain(field: FieldNode, grain: string) {
		const dimensions = query.dimensions ?? [];
		commit({
			...query,
			dimensions: dimensions.includes(field.name) ? dimensions : [...dimensions, field.name],
			time_grain: grain
		});
	}

	function toggleField(field: FieldNode) {
		const key = field.kind === 'dimension' ? 'dimensions' : 'metrics';
		const list = query[key] ?? [];
		const next = list.includes(field.name)
			? list.filter((name) => name !== field.name)
			: [...list, field.name];
		const draft = { ...query, [key]: next };
		if (!next.includes(field.name)) {
			// A sort on a field that is no longer selected is rejected by the compiler.
			draft.sorts = (draft.sorts ?? []).filter((sort) => sort.field !== field.name);
		}
		if (draft.time_grain && !(draft.dimensions ?? []).some((n) => fields.get(n)?.grains.length)) {
			draft.time_grain = undefined;
		}
		commit(draft);
	}

	function moveChip(key: 'dimensions' | 'metrics', index: number, delta: -1 | 1) {
		const list = [...(query[key] ?? [])];
		const target = index + delta;
		if (target < 0 || target >= list.length) return;
		[list[index], list[target]] = [list[target], list[index]];
		commit({ ...query, [key]: list });
	}

	function addFilter() {
		const first = [...fields.values()][0];
		if (!first) return;
		filterText = [...filterText, ''];
		commit({
			...query,
			filters: [
				...(query.filters ?? []),
				{ field: first.name, op: operatorsFor(first)[0], values: [] }
			]
		});
	}

	function updateFilter(index: number, patch: { field?: string; op?: FilterOp; text?: string }) {
		const filters = [...(query.filters ?? [])];
		const current = filters[index];
		const field = fields.get(patch.field ?? current.field);
		let op = patch.op ?? current.op;
		if (patch.field && !operatorsFor(field).includes(op)) op = operatorsFor(field)[0];
		const text = patch.text ?? filterText[index] ?? '';
		if (patch.field) filterText[index] = '';
		else if (patch.text !== undefined) filterText[index] = patch.text;
		filters[index] = {
			field: patch.field ?? current.field,
			op,
			values: parseFilterValues(field, op, patch.field ? '' : text)
		};
		commit({ ...query, filters });
	}

	function removeFilter(index: number) {
		filterText = filterText.filter((_, i) => i !== index);
		commit({ ...query, filters: (query.filters ?? []).filter((_, i) => i !== index) });
	}

	function addSort() {
		const candidate = selected.find((name) => !(query.sorts ?? []).some((s) => s.field === name));
		if (!candidate) return;
		commit({ ...query, sorts: [...(query.sorts ?? []), { field: candidate }] });
	}

	function updateSort(index: number, patch: { field?: string; desc?: boolean }) {
		const sorts = [...(query.sorts ?? [])];
		sorts[index] = { ...sorts[index], ...patch };
		commit({ ...query, sorts });
	}

	function removeSort(index: number) {
		commit({ ...query, sorts: (query.sorts ?? []).filter((_, i) => i !== index) });
	}

	function setLimit(text: string) {
		const value = Math.floor(Number(text));
		commit({
			...query,
			limit: Number.isFinite(value) && value > 0 ? Math.min(value, MAX_EXPLORE_LIMIT) : undefined
		});
	}

	function fieldOptions(names: string[]) {
		return names.map((name) => ({ value: name, label: fields.get(name)?.label ?? name }));
	}

	// ── compile and run ─────────────────────────────────────────────────────────

	function scheduleCompile() {
		clearTimeout(compileTimer);
		compileTimer = setTimeout(() => void doCompile(), 300);
	}

	async function doCompile() {
		const seq = ++compileSeq;
		if (!workspaceId || !query.model || !hasSelection) {
			compiled = null;
			compileError = null;
			compiling = false;
			return;
		}
		compiling = true;
		try {
			const out = await compileExplore(workspaceId, query, bindingId);
			if (seq !== compileSeq) return;
			compiled = out;
			compileError = null;
		} catch (e) {
			if (seq !== compileSeq) return;
			compiled = null;
			compileError =
				e instanceof SemanticRequestError
					? e
					: new SemanticRequestError(e instanceof Error ? e.message : String(e));
		} finally {
			if (seq === compileSeq) compiling = false;
		}
	}

	function resetRun() {
		poller?.stop();
		poller = undefined;
		executionId = '';
		runStatus = 'idle';
		runError = null;
	}

	async function run() {
		if (!compiled || !bindingId || !workspaceId) return;
		resetRun();
		runStatus = 'running';
		tab = 'results';
		const { data, error } = await client.POST('/workspaces/{id}/executions', {
			params: { path: { id: workspaceId } },
			body: {
				workspace_id: workspaceId,
				sql: compiled.sql,
				target: { binding_id: bindingId }
			}
		});
		if (error || !data) {
			runStatus = 'failed';
			const body = error as { code?: string } | undefined;
			runError = {
				code: body?.code ?? 'internal',
				error_native: apiErrorMessage(error, m.explore_run_failed())
			};
			return;
		}
		const id = data.id;
		poller = poll(
			async () => {
				const res = await client.GET('/workspaces/{id}/executions/{execution_id}', {
					params: { path: { id: workspaceId, execution_id: id } }
				});
				if (res.error || !res.data) throw new Error('poll');
				return res.data.status;
			},
			{ until: (status) => status !== 'running' }
		);
		const result = await poller.done;
		if (result === 'stopped') return;
		if (result === 'timeout') {
			runStatus = 'timeout';
			return;
		}
		executionId = id;
		runStatus = result as typeof runStatus;
		if (result === 'failed') runError = await statementError(id);
	}

	async function statementError(id: string) {
		const { data } = await client.GET('/workspaces/{id}/executions/{execution_id}/statements', {
			params: { path: { id: workspaceId, execution_id: id } }
		});
		const failed = (data ?? []).find((statement) => statement.status === 'failed');
		return {
			code: failed?.error_code ?? 'query_failed',
			error_native: failed?.error_message ?? undefined
		};
	}

	// ── actions ─────────────────────────────────────────────────────────────────

	function openInEditor() {
		if (!compiled) return;
		editor.addTab({
			name: base?.label ?? m.explore_title(),
			sql: withExploreComment(compiled.sql, query),
			bindingId
		});
		void goto(resolve('/sql'));
	}

	let bindingsSeq = 0;

	async function loadBindings() {
		// A slower answer for a model the user already left must not set the
		// engine of the model now selected.
		const seq = ++bindingsSeq;
		const model = base;
		bindings = [];
		if (!model) return;
		let options: BindingOption[] = [];
		try {
			options = await bindingOptions(model.catalog_id);
		} catch {
			if (seq === bindingsSeq) toasts.error(m.explore_bindings_failed());
		}
		if (seq !== bindingsSeq) return;
		bindings = options;
		bindingId = model.binding_id ?? options[0]?.id ?? '';
	}

	async function load() {
		if (!workspaceId) return;
		loading = true;
		loadError = '';
		try {
			await savedQueries.reload(workspaceId);
			models = await listModels(workspaceId);
			const shared = decodeShare(page.url.searchParams.get('q'));
			if (shared && models.some((model) => model.slug === shared.model)) {
				query = shared;
				filterText = (shared.filters ?? []).map((f) => (f.values ?? []).join(', '));
				await loadBindings();
				await doCompile();
			} else if (shared) {
				loadError = m.explore_unknown_model({ model: shared.model });
			}
		} catch (e) {
			loadError = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	onMount(async () => {
		await workspaceStore.load();
		await load();
	});

	onDestroy(() => {
		clearTimeout(compileTimer);
		poller?.stop();
	});
</script>

<div class="flex min-h-0 flex-1 overflow-hidden">
	<aside
		class="flex w-72 shrink-0 flex-col gap-3 overflow-y-auto border-r border-border bg-bg-panel p-4"
		aria-label={m.explore_fields()}
	>
		{#if loading}
			<Skeleton class="h-8 w-full" />
			<Skeleton class="h-40 w-full" />
		{:else}
			<Select
				label={m.explore_model()}
				value={query.model}
				placeholder={m.explore_pick_a_model()}
				options={models.map((model) => ({ value: model.slug, label: model.label }))}
				onchange={(e) => selectModel((e.target as HTMLSelectElement).value)}
			/>
			{#if query.model}
				<Input label={m.explore_search_fields()} bind:value={search} />
				{#if visibleGroups.length === 0}
					<p class="text-[12px] text-text-muted">{m.explore_no_fields()}</p>
				{:else}
					<ExploreFieldTree
						groups={visibleGroups}
						{selected}
						timeGrain={query.time_grain ?? ''}
						ontoggle={toggleField}
						onpickgrain={pickGrain}
					/>
				{/if}
			{/if}
			<a class="text-[12px] text-primary hover:underline" href={resolve('/models')}>
				{m.explore_manage_models()}
			</a>
		{/if}
	</aside>

	<div class="flex min-w-0 flex-1 flex-col overflow-y-auto p-6">
		<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.explore_title()}</h1>
				<p class="mt-1 text-[13px] text-text-muted">{m.explore_subtitle()}</p>
			</div>
			<div class="flex flex-wrap gap-2">
				<CopyButton value={shareUrl} label={m.explore_share_link()} />
				<Button onclick={openInEditor} disabled={!compiled}>{m.explore_open_in_editor()}</Button>
				<Button onclick={() => (saveOpen = true)} disabled={!compiled}>
					{m.explore_save_as_query()}
				</Button>
				<Button
					variant="primary"
					onclick={run}
					loading={runStatus === 'running'}
					disabled={!compiled || !bindingId || runStatus === 'running'}
				>
					{m.explore_run()}
				</Button>
			</div>
		</div>

		{#if loadError}
			<ErrorState title={m.explore_load_failed()} message={loadError} onretry={load} />
		{:else if loading}
			<Skeleton class="h-24 w-full" />
		{:else if models.length === 0}
			<EmptyState title={m.explore_no_models()} description={m.explore_no_models_hint()}>
				<Button variant="primary" onclick={() => goto(resolve('/models/new'))}>
					{m.models_new()}
				</Button>
			</EmptyState>
		{:else if !query.model}
			<EmptyState title={m.explore_pick_a_model()} description={m.explore_pick_hint()} />
		{:else}
			<section class="mb-3 rounded-lg border border-border bg-bg-panel p-3">
				<div class="flex flex-wrap gap-4">
					{#each [{ key: 'dimensions' as const, title: m.explore_dimensions() }, { key: 'metrics' as const, title: m.explore_metrics() }] as list (list.key)}
						<div class="min-w-48">
							<h2 class="mb-1 text-[11px] font-semibold text-text-muted">{list.title}</h2>
							<ul class="flex flex-wrap gap-1.5">
								{#each query[list.key] ?? [] as name, index (name)}
									<li
										class="inline-flex items-center gap-0.5 rounded-full border px-2 py-0.5 text-[11px] {badFields.includes(
											name
										)
											? 'border-red-400 bg-red-50 text-red-700 dark:bg-[#1f0f0f] dark:text-red-300'
											: 'border-border bg-bg-toolbar'}"
										data-invalid={badFields.includes(name) || undefined}
									>
										<span class="px-1">{fields.get(name)?.label ?? name}</span>
										<button
											type="button"
											class="px-1 disabled:opacity-30"
											aria-label={m.explore_move_earlier({ field: name })}
											disabled={index === 0}
											onclick={() => moveChip(list.key, index, -1)}>←</button
										>
										<button
											type="button"
											class="px-1 disabled:opacity-30"
											aria-label={m.explore_move_later({ field: name })}
											disabled={index === (query[list.key] ?? []).length - 1}
											onclick={() => moveChip(list.key, index, 1)}>→</button
										>
										<button
											type="button"
											class="px-1"
											aria-label={m.explore_remove_field({ field: name })}
											onclick={() => {
												const f = fields.get(name);
												if (f) toggleField(f);
											}}>×</button
										>
									</li>
								{:else}
									<li class="text-[11px] text-text-muted">{m.explore_none_selected()}</li>
								{/each}
							</ul>
						</div>
					{/each}
				</div>
			</section>

			<section
				class="mb-3 grid gap-3 rounded-lg border border-border bg-bg-panel p-3 lg:grid-cols-2"
			>
				<div>
					<div class="mb-1 flex items-center justify-between">
						<h2 class="text-[11px] font-semibold text-text-muted">{m.explore_filters()}</h2>
						<Button size="sm" onclick={addFilter}>{m.explore_add_filter()}</Button>
					</div>
					{#each query.filters ?? [] as filter, index (index)}
						{@const field = fields.get(filter.field)}
						<div
							class="mb-2 grid grid-cols-[1fr_auto_1fr_auto] items-end gap-1.5"
							data-invalid={badFields.includes(filter.field) || undefined}
						>
							<Select
								label={m.explore_filter_field()}
								value={filter.field}
								options={fieldOptions([...fields.keys()])}
								onchange={(e) =>
									updateFilter(index, { field: (e.target as HTMLSelectElement).value })}
							/>
							<Select
								label={m.explore_filter_operator()}
								value={filter.op}
								options={operatorsFor(field).map((op) => ({ value: op, label: op }))}
								onchange={(e) =>
									updateFilter(index, { op: (e.target as HTMLSelectElement).value as FilterOp })}
							/>
							{#if filter.op === 'is_null' || filter.op === 'is_not_null'}
								<div></div>
							{:else}
								<Input
									label={m.explore_filter_value()}
									value={filterText[index] ?? ''}
									oninput={(e) =>
										updateFilter(index, { text: (e.target as HTMLInputElement).value })}
								/>
							{/if}
							<Button size="sm" onclick={() => removeFilter(index)}>{m.common_remove()}</Button>
						</div>
					{:else}
						<p class="text-[11px] text-text-muted">{m.explore_no_filters()}</p>
					{/each}
					<p class="text-[10px] text-text-muted">{m.explore_filters_hint()}</p>
				</div>

				<div class="flex flex-col gap-2">
					<div>
						<div class="mb-1 flex items-center justify-between">
							<h2 class="text-[11px] font-semibold text-text-muted">{m.explore_sorts()}</h2>
							<Button size="sm" onclick={addSort} disabled={!hasSelection}>
								{m.explore_add_sort()}
							</Button>
						</div>
						{#each query.sorts ?? [] as sort, index (sort.field)}
							<div class="mb-2 grid grid-cols-[1fr_auto_auto] items-end gap-1.5">
								<Select
									label={m.explore_sort_field()}
									value={sort.field}
									options={fieldOptions(selected)}
									onchange={(e) =>
										updateSort(index, { field: (e.target as HTMLSelectElement).value })}
								/>
								<Select
									label={m.explore_sort_direction()}
									value={sort.desc ? 'desc' : 'asc'}
									options={[
										{ value: 'asc', label: m.explore_ascending() },
										{ value: 'desc', label: m.explore_descending() }
									]}
									onchange={(e) =>
										updateSort(index, { desc: (e.target as HTMLSelectElement).value === 'desc' })}
								/>
								<Button size="sm" onclick={() => removeSort(index)}>{m.common_remove()}</Button>
							</div>
						{/each}
					</div>
					<div class="grid grid-cols-3 gap-2">
						<Input
							label={m.explore_limit()}
							type="number"
							value={query.limit ? String(query.limit) : ''}
							placeholder={String(MAX_EXPLORE_LIMIT)}
							oninput={(e) => setLimit((e.target as HTMLInputElement).value)}
						/>
						<Select
							label={m.explore_time_grain()}
							value={query.time_grain ?? ''}
							disabled={grainOptions.length === 0}
							options={[{ value: '', label: m.explore_grain_none() }, ...grainOptions]}
							onchange={(e) =>
								commit({
									...query,
									time_grain: (e.target as HTMLSelectElement).value || undefined
								})}
						/>
						<Select
							label={m.explore_engine()}
							bind:value={bindingId}
							options={bindings.map((b) => ({ value: b.id, label: b.label }))}
							onchange={scheduleCompile}
						/>
					</div>
				</div>
			</section>

			<Tabs
				tabs={[
					{ id: 'results', label: m.explore_results() },
					{ id: 'sql', label: m.explore_sql() }
				]}
				bind:active={tab}
			/>
			<div class="min-h-64 flex-1 pt-3">
				{#if tab === 'sql'}
					{#if compileError}
						<ErrorPanel
							error={{
								code: compileError.code ?? 'invalid_definition',
								message: compileError.message
							}}
						/>
						<p class="mt-2 font-mono text-[11px] text-text-muted">{compileError.message}</p>
					{:else if compiling && !compiled}
						<Skeleton class="h-32 w-full" />
					{:else if compiled}
						<div class="mb-2 flex items-center gap-2 text-[11px]">
							<Badge tone="info">{compiled.dialect}</Badge>
							<CopyButton value={compiled.sql} label="SQL" />
						</div>
						<CodeBlock
							code={compiled.sql}
							language="sql"
							dialect={bindings.find((b) => b.id === bindingId)?.codemirrorBase}
							label={m.explore_compiled_sql()}
						/>
						{#if (compiled.notes ?? []).length > 0}
							<ul
								class="mt-2 list-disc pl-5 text-[12px] text-text-muted"
								aria-label={m.explore_notes()}
							>
								{#each compiled.notes ?? [] as note (note)}
									<li>{note}</li>
								{/each}
							</ul>
						{/if}
					{:else}
						<EmptyState title={m.explore_no_sql()} description={m.explore_no_sql_hint()} />
					{/if}
				{:else if compileError}
					<ErrorPanel
						error={{
							code: compileError.code ?? 'invalid_definition',
							message: compileError.message
						}}
					/>
					<p class="mt-2 font-mono text-[11px] text-text-muted">{compileError.message}</p>
				{:else if runStatus === 'running'}
					<Skeleton class="h-32 w-full" />
				{:else if runStatus === 'failed' || runStatus === 'cancelled' || runStatus === 'timeout'}
					<p class="mb-2 text-[13px] font-semibold">{m.explore_run_failed()}</p>
					{#if runError}
						<ErrorPanel error={runError} />
					{/if}
					<div class="mt-2">
						<Button onclick={run}>{m.explore_run_again()}</Button>
					</div>
				{:else if executionId}
					<div class="h-96">
						<ResultsPanel {executionId} enabled={runStatus === 'finished'} />
					</div>
				{:else}
					<EmptyState
						title={hasSelection ? m.explore_ready() : m.explore_select_fields()}
						description={hasSelection ? m.explore_ready_hint() : m.explore_select_hint()}
					/>
				{/if}
			</div>
		{/if}
	</div>
</div>

<SaveQueryDialog
	bind:open={saveOpen}
	tab={saveTab}
	{workspaceId}
	spaces={savedQueries.spaces}
	folders={savedQueries.folders}
	onconflict={() => toasts.error(m.explore_save_conflict())}
	onsaved={() => toasts.success(m.explore_saved())}
/>
