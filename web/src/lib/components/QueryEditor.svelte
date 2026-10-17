<script lang="ts">
	import { onMount } from 'svelte';
	import { browser } from '$app/environment';
	import { basicSetup } from 'codemirror';
	import { EditorView, keymap } from '@codemirror/view';
	import { EditorState, Compartment } from '@codemirror/state';
	import { indentWithTab } from '@codemirror/commands';
	import { sql as sqlLanguage } from '@codemirror/lang-sql';
	import { oneDark } from '@codemirror/theme-one-dark';
	import {
		autocompletion,
		type Completion,
		type CompletionContext,
		type CompletionResult
	} from '@codemirror/autocomplete';
	import { format as formatSQLFormatter } from 'sql-formatter';
	import { tableFromIPC } from 'apache-arrow';
	import { API_ROOT, apiFetch, apiErrorMessage, client, getAccessToken } from '$lib/api/client';
	import { editor } from '$lib/stores/editor.svelte';
	import { savedQueries } from '$lib/stores/savedQueries.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import {
		codemirrorDialect,
		formatterLanguage,
		loadDialect,
		loadEngines,
		type Dialect,
		type Engine,
		type WorkspaceTarget
	} from '$lib/engines/client';
	import { loadNamespaces, searchMetadata } from '$lib/metadata/client';
	import { resolveAliasColumns } from '$lib/engines/completion';
	import Badge from '$lib/ui/Badge.svelte';
	import Modal from '$lib/ui/Modal.svelte';

	type ExecutionEvent = {
		type: string;
		rows_so_far?: number;
		rows_returned?: number;
		duration_ms?: number;
		message?: string;
	};

	function errorMessage(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	let editorView: EditorView;
	let editorEl: HTMLDivElement;
	const themeCompartment = new Compartment();
	const dialectCompartment = new Compartment();

	const tabs = $derived(editor.tabs);
	const activeIdx = $derived(
		Math.max(
			0,
			editor.tabs.findIndex((tab) => tab.id === editor.activeId)
		)
	);
	const activeTab = $derived(editor.activeTab);

	let executing = $state(false);
	let dark = $state(browser && document.body.classList.contains('dark'));
	let cursorLine = $state(1);
	let cursorCol = $state(1);

	const workspaceId = $derived(workspaceStore.current?.id || '');
	let targets = $state<WorkspaceTarget[]>([]);
	let engines = $state<Engine[]>([]);
	let targetError = $state('');
	let dialect = $state<Dialect | null>(null);
	let namespaces = $state<string[][]>([]);
	let namespacesError = $state('');

	let saveModalOpen = $state(false);
	let saveName = $state('');
	let perPage = $state(50);

	// ── Target resolution ────────────────────────────────────────────────
	const selectedTarget = $derived(targets.find((t) => t.binding_id === activeTab.bindingId));
	const activeEngineId = $derived(selectedTarget?.engine_id ?? '');
	const selectedEngine = $derived(engines.find((e) => e.id === activeEngineId));

	const engineChoices = $derived.by(() => {
		const out: { id: string; name: string; kind: string }[] = [];
		for (const target of targets) {
			if (out.some((choice) => choice.id === target.engine_id)) continue;
			out.push({ id: target.engine_id, name: target.engine_name, kind: target.engine_kind });
		}
		return out;
	});

	const catalogChoices = $derived(targets.filter((target) => target.engine_id === activeEngineId));
	const canChooseSchema = $derived((selectedEngine?.capabilities?.namespace_depth ?? 0) > 0);
	const sessionSchema = $derived(selectedEngine?.capabilities?.session_schema ?? true);
	const canExplain = $derived(selectedEngine?.capabilities?.supports_explain ?? true);
	const canCancel = $derived(selectedEngine?.capabilities?.supports_cancel ?? true);

	async function loadTargets(wid: string) {
		targets = [];
		targetError = '';
		if (!wid) return;
		try {
			const [{ data, error }, loadedEngines] = await Promise.all([
				client.GET('/workspaces/{id}/targets', { params: { path: { id: wid } } }),
				loadEngines().catch(() => [] as Engine[])
			]);
			if (error) throw new Error(apiErrorMessage(error, 'Failed to load targets'));
			targets = data ?? [];
			engines = loadedEngines;
		} catch (e) {
			targetError = errorMessage(e);
		}
	}

	$effect(() => {
		void workspaceStore.load();
	});

	$effect(() => {
		void loadTargets(workspaceId);
	});

	// A tab without a target starts on the workspace default, else the first.
	$effect(() => {
		const tab = activeTab;
		if (!tab || targets.length === 0) return;
		if (tab.bindingId && targets.some((t) => t.binding_id === tab.bindingId)) return;
		const ws = workspaceStore.current;
		const pick = targets.find((t) => t.binding_id === ws?.default_binding_id) ?? targets[0];
		editor.setTarget(tab.id, pick.binding_id, tab.schema || ws?.default_schema || '');
	});

	// The dialect (keywords and functions) follows the selected engine.
	$effect(() => {
		const name = selectedEngine?.dialect?.name;
		const wid = workspaceId;
		void wid;
		if (!name) {
			dialect = null;
			return;
		}
		let cancelled = false;
		loadDialect(name)
			.then((loaded) => {
				if (!cancelled) dialect = loaded;
			})
			.catch(() => {
				if (!cancelled) dialect = null;
			});
		return () => {
			cancelled = true;
		};
	});

	// Namespaces of the selected catalog feed the schema picker.
	$effect(() => {
		const catalogId = selectedTarget?.catalog_id;
		const depth = selectedEngine?.capabilities?.namespace_depth ?? 0;
		namespaces = [];
		namespacesError = '';
		if (!catalogId || depth <= 0 || !workspaceId) return;
		let cancelled = false;
		loadNamespaces(workspaceId, catalogId, [])
			.then((list) => {
				if (!cancelled) namespaces = list.items.map((item) => item.path);
			})
			.catch((e) => {
				if (!cancelled) namespacesError = errorMessage(e);
			});
		return () => {
			cancelled = true;
		};
	});

	function selectEngine(engineId: string) {
		const candidates = targets.filter((t) => t.engine_id === engineId);
		const pick =
			candidates.find((t) => t.catalog_name === selectedTarget?.catalog_name) ?? candidates[0];
		if (pick) editor.setTarget(editor.activeId, pick.binding_id, '');
	}

	function selectCatalog(bindingId: string) {
		editor.setTarget(editor.activeId, bindingId, workspaceStore.current?.default_schema ?? '');
	}

	function selectSchema(schema: string) {
		editor.setTarget(editor.activeId, activeTab.bindingId, schema);
	}

	function switchTab(idx: number) {
		editor.activate(tabs[idx].id);
	}

	function newTab() {
		editor.addTab();
	}

	function closeTab(idx: number) {
		editor.closeTab(tabs[idx].id);
	}

	// Mirrors the store's active tab into CodeMirror when another component
	// (a saved query, the route) changes the selection.
	let syncedTabId = $state('');
	$effect(() => {
		const id = editor.activeId;
		if (!editorView || id === syncedTabId) return;
		syncedTabId = id;
		const sql = editor.activeTab?.sql ?? '';
		if (editorView.state.doc.toString() !== sql) {
			editorView.dispatch({ changes: { from: 0, to: editorView.state.doc.length, insert: sql } });
		}
	});

	// Switching engine re-configures the grammar without remounting the editor.
	$effect(() => {
		const base = selectedEngine?.dialect?.codemirror_base;
		void base;
		if (!editorView) return;
		editorView.dispatch({
			effects: dialectCompartment.reconfigure(
				sqlLanguage({ dialect: codemirrorDialect(base), upperCaseKeywords: true })
			)
		});
	});

	// ── Autocomplete ─────────────────────────────────────────────────────
	const keywordOptions = $derived<Completion[]>([
		...(dialect?.keywords ?? []).map((keyword) => ({ label: keyword, type: 'keyword' })),
		...(dialect?.functions ?? []).map((fn) => ({
			label: fn,
			type: 'function',
			apply: `${fn}()`
		}))
	]);

	let completionController: AbortController | undefined;
	let completionSeq = 0;

	function delay(ms: number): Promise<void> {
		return new Promise((resolve) => setTimeout(resolve, ms));
	}

	async function complete(context: CompletionContext): Promise<CompletionResult | null> {
		if (!workspaceId) return null;
		const { state, pos } = context;
		const before = state.sliceDoc(Math.max(0, pos - 80), pos);
		const aliasMatch = /([A-Za-z_][\w$]*)\.$/.exec(before);
		const word = context.matchBefore(/[\w$]*/);
		const token = word?.text ?? '';
		const from = word?.from ?? pos;

		if (!aliasMatch && token.length < 1 && !context.explicit) return null;

		completionController?.abort();
		const controller = new AbortController();
		completionController = controller;
		const seq = ++completionSeq;

		const options: Completion[] = keywordOptions
			.filter((option) => option.label.toLowerCase().startsWith(token.toLowerCase()))
			.map((option) => ({ ...option, boost: 0 }));

		if (aliasMatch) {
			try {
				const columns = await resolveAliasColumns(
					workspaceId,
					state.doc.toString(),
					aliasMatch[1],
					controller.signal
				);
				if (seq !== completionSeq || controller.signal.aborted) return null;
				for (const column of columns) {
					options.unshift({
						label: column.name,
						type: 'property',
						detail: column.display_type || column.native_type,
						boost: 3
					});
				}
			} catch {
				// Aborted or unresolvable: fall back to keywords.
			}
			return { from, options };
		}

		if (token.length < 2) return { from, options };

		// Debounce the search so a burst of keystrokes issues one request.
		await delay(250);
		if (controller.signal.aborted || seq !== completionSeq) return null;
		try {
			const results = await searchMetadata(workspaceId, token, {
				kind: 'all',
				limit: 20,
				signal: controller.signal
			});
			if (seq !== completionSeq) return null;
			for (const result of results) {
				if (result.kind === 'column' && result.column) {
					options.unshift({
						label: result.column,
						type: 'property',
						detail: `${result.table} · ${result.native_type ?? ''}`.trim(),
						boost: 2
					});
				} else {
					options.unshift({
						label: result.table,
						type: 'class',
						detail: result.namespace.join('.'),
						boost: 1
					});
				}
			}
		} catch {
			// Aborted: CodeMirror keeps the previous result.
		}
		return { from, options };
	}

	onMount(() => {
		const observer = new MutationObserver(() => {
			dark = document.body.classList.contains('dark');
		});
		observer.observe(document.body, { attributes: true, attributeFilter: ['class'] });

		const extensions = [
			basicSetup,
			keymap.of([
				indentWithTab,
				{
					key: 'Mod-Enter',
					run: () => {
						void runCurrent();
						return true;
					}
				},
				{
					key: 'Shift-Mod-Enter',
					run: () => {
						void runAll();
						return true;
					}
				}
			]),
			dialectCompartment.of(
				sqlLanguage({ dialect: codemirrorDialect(undefined), upperCaseKeywords: true })
			),
			autocompletion({ override: [complete] }),
			EditorView.contentAttributes.of({ 'aria-label': 'SQL editor' }),
			themeCompartment.of(dark ? oneDark : []),
			EditorView.updateListener.of((update) => {
				if (update.docChanged) {
					editor.updateSql(editor.activeId, update.state.doc.toString());
				}
				if (update.selectionSet || update.docChanged) {
					const head = update.state.selection.main.head;
					const line = update.state.doc.lineAt(head);
					cursorLine = line.number;
					cursorCol = head - line.from + 1;
				}
			})
		];

		const state = EditorState.create({ doc: editor.activeTab?.sql ?? '', extensions });
		editorView = new EditorView({ state, parent: editorEl });

		return () => observer.disconnect();
	});

	$effect(() => {
		if (editorView) {
			editorView.dispatch({ effects: themeCompartment.reconfigure(dark ? oneDark : []) });
		}
	});

	// "Insert name in editor" (#23) writes through the typed store, never a
	// window event: apply the pending insert at the cursor of the active tab.
	let appliedInsertSeq = 0;
	$effect(() => {
		const pending = editor.pendingInsert;
		if (!editorView || !pending || pending.seq === appliedInsertSeq) return;
		appliedInsertSeq = pending.seq;
		if (pending.tabId !== editor.activeId) return;
		const { from, to } = editorView.state.selection.main;
		editorView.dispatch({
			changes: { from, to, insert: pending.text },
			selection: { anchor: from + pending.text.length }
		});
		editorView.focus();
	});

	// ── Running ──────────────────────────────────────────────────────────
	async function splitStatements(name: string, sqlText: string) {
		const { data, error } = await client.POST('/dialects/{name}/split', {
			params: { path: { name } },
			body: { sql: sqlText }
		});
		if (error || !data) throw new Error(apiErrorMessage(error, 'Could not split the script'));
		return data;
	}

	async function submit(sqlText: string) {
		const text = sqlText.trim();
		if (!text) return;
		const idx = activeIdx;
		const tab = tabs[idx];
		if (!tab.bindingId) {
			tab.errorMsg = 'Choose an engine and catalog to run on';
			tab.statusText = 'No target';
			return;
		}
		executing = true;
		tab.errorMsg = '';
		tab.results = [];
		tab.statusText = 'Running...';
		try {
			const { data, error } = await client.POST('/workspaces/{id}/executions', {
				params: { path: { id: workspaceId } },
				body: {
					workspace_id: workspaceId,
					sql: text,
					target: {
						binding_id: tab.bindingId,
						...(tab.schema ? { schema: tab.schema } : {})
					}
				}
			});
			if (error || !data) {
				throw new Error(apiErrorMessage(error, 'Failed to start execution'));
			}
			tab.runTarget = data.target ?? null;
			editor.setExecution(tab.id, data.id);
			connectWS(data.id, idx);
		} catch (e) {
			tab.errorMsg = errorMessage(e);
			executing = false;
			tab.statusText = 'Error';
		}
	}

	async function runAll() {
		if (!editorView) return;
		await submit(editorView.state.doc.toString());
	}

	async function runCurrent() {
		if (!editorView) return;
		const selection = editorView.state.selection.main;
		const doc = editorView.state.doc;
		if (!selection.empty) {
			await submit(doc.sliceString(selection.from, selection.to));
			return;
		}
		const full = doc.toString();
		const name = selectedEngine?.dialect?.name;
		if (!name) {
			await submit(full);
			return;
		}
		try {
			const spans = await splitStatements(name, full);
			const head = selection.head;
			const span =
				spans.find((s) => s.start <= head && head <= s.end) ??
				[...spans].reverse().find((s) => s.end <= head) ??
				spans[0];
			await submit(span ? full.slice(span.start, span.end) : full);
		} catch {
			// The splitter refused (unterminated string, …): run the script.
			await submit(full);
		}
	}

	async function cancelExecution() {
		const tab = activeTab;
		if (!tab?.executionId) return;
		try {
			tab.statusText = 'Cancelling...';
			const { error } = await client.POST('/workspaces/{id}/executions/{execution_id}/cancel', {
				params: { path: { id: workspaceId, execution_id: tab.executionId } }
			});
			if (error) throw new Error(apiErrorMessage(error, 'Could not cancel'));
		} catch (e) {
			tab.errorMsg = errorMessage(e);
		}
	}

	function connectWS(execId: string, idx: number) {
		const tab = tabs[idx];
		const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
		const ws = new WebSocket(
			`${proto}//${location.host}${API_ROOT}/ws/workspaces/${workspaceId}/executions/${execId}?token=${getAccessToken() ?? ''}`
		);
		ws.onmessage = async (e) => {
			const event = JSON.parse(e.data) as ExecutionEvent;
			switch (event.type) {
				case 'running':
					tab.statusText = 'Running...';
					break;
				case 'progress':
					tab.statusText = `Running... ${event.rows_so_far || 0} rows`;
					break;
				case 'finished':
					tab.statusText = 'Complete';
					tab.rowCount = event.rows_returned || 0;
					tab.durationMs = event.duration_ms || 0;
					tab.page = 1;
					ws.close();
					await fetchResults(execId, idx);
					executing = false;
					break;
				case 'failed':
					tab.errorMsg = event.message || 'Query failed';
					executing = false;
					tab.statusText = 'Failed';
					ws.close();
					break;
				case 'cancelled':
					tab.statusText = 'Cancelled';
					executing = false;
					ws.close();
					break;
			}
		};
		ws.onerror = () => {
			executing = false;
			tab.statusText = 'Connection error';
		};
	}

	async function fetchResults(execId: string, idx: number) {
		const tab = tabs[idx];
		try {
			const res = await apiFetch(`/workspaces/${workspaceId}/executions/${execId}/results`);
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			const buf = await res.arrayBuffer();
			if (buf.byteLength > 0) {
				const arr = new Uint8Array(buf);
				const table = tableFromIPC(arr);
				tab.columns = table.schema.fields.map((f) => f.name);
				const decoded: unknown[][] = [];
				for (const row of table.toArray()) {
					decoded.push(tab.columns.map((c) => (row as Record<string, unknown>)[c]));
				}
				tab.results = decoded;
				tab.rowCount = decoded.length;
			}
		} catch (e) {
			tab.errorMsg = 'Results unavailable: ' + errorMessage(e);
		}
	}

	function formatSQL() {
		if (!editorView) return;
		try {
			const sqlText = editorView.state.doc.toString();
			const formatted = formatSQLFormatter(sqlText, {
				language: formatterLanguage(selectedEngine?.dialect?.formatter_language),
				keywordCase: 'upper',
				indentStyle: 'standard'
			});
			editorView.dispatch({
				changes: { from: 0, to: editorView.state.doc.length, insert: formatted }
			});
		} catch {
			/* invalid SQL: leave the editor untouched */
		}
	}

	function explainQuery() {
		if (!editorView) return;
		const sqlText = editorView.state.doc.toString().trim();
		if (!sqlText.toUpperCase().startsWith('EXPLAIN')) {
			editorView.dispatch({
				changes: { from: 0, to: editorView.state.doc.length, insert: 'EXPLAIN ' + sqlText }
			});
		}
		void runAll();
	}

	async function saveQuery() {
		if (!saveName.trim() || !workspaceId) return;
		try {
			const sql = editorView.state.doc.toString();
			const savedId = tabs[activeIdx].savedId;
			const { data: saved, error } = savedId
				? await client.PATCH('/workspaces/{id}/queries/{query_id}', {
						params: { path: { id: workspaceId, query_id: savedId } },
						body: { name: saveName.trim(), sql }
					})
				: await client.POST('/workspaces/{id}/queries', {
						params: { path: { id: workspaceId } },
						body: { name: saveName.trim(), sql }
					});
			if (error || !saved) {
				tabs[activeIdx].errorMsg = apiErrorMessage(error, 'Save failed');
				return;
			}
			editor.markSaved(editor.activeId, saved);
			savedQueries.onSaved(saved);
			saveModalOpen = false;
			saveName = '';
		} catch (e) {
			tabs[activeIdx].errorMsg = 'Save failed: ' + errorMessage(e);
		}
	}

	const pagedRows = $derived(
		activeTab.results.slice((activeTab.page - 1) * perPage, activeTab.page * perPage)
	);
	const totalPages = $derived(Math.max(1, Math.ceil(activeTab.results.length / perPage)));
</script>

<div class="flex-1 flex flex-col overflow-hidden min-w-0">
	<div
		class="flex items-stretch bg-bg-toolbar pt-1.5 px-2 gap-0.5 border-b border-border shrink-0 overflow-x-auto"
	>
		{#each tabs as tab, i (tab.id)}
			<button
				class="py-1.5 px-3.5 flex items-center gap-1.5 text-xs rounded-t-md cursor-pointer -mb-px border border-transparent border-b-0 transition-colors duration-150
				{i === activeIdx
					? 'bg-bg-panel text-primary font-semibold border-border'
					: 'text-text-muted hover:bg-border/50'}"
				onclick={() => switchTab(i)}
			>
				<span class="overflow-hidden text-ellipsis whitespace-nowrap max-w-[120px]">{tab.name}</span
				>
				{#if tabs.length > 1}
					<button
						type="button"
						aria-label="Close tab"
						class="text-sm text-text-muted py-px px-1 rounded-[3px] hover:bg-border hover:text-text-main"
						onclick={(e) => {
							e.stopPropagation();
							closeTab(i);
						}}>×</button
					>
				{/if}
			</button>
		{/each}
		<button
			class="flex items-center justify-center w-8 text-lg text-text-muted cursor-pointer shrink-0 bg-none border-none hover:text-primary"
			onclick={newTab}>+</button
		>
	</div>

	<div class="flex items-center py-1.5 px-3 gap-1.5 border-b border-border bg-bg-toolbar shrink-0">
		<button
			onclick={runAll}
			disabled={executing}
			class="bg-success text-white border-none py-[5px] px-3.5 text-xs font-bold rounded-[5px] cursor-pointer flex items-center gap-1.5 hover:enabled:bg-success-hover disabled:opacity-60 disabled:cursor-not-allowed"
		>
			<svg viewBox="0 0 24 24" class="w-2.5 h-2.5 fill-white stroke-none"
				><polygon points="5 3 19 12 5 21 5 3" /></svg
			>
			{executing ? 'Running...' : 'Run'}
			<span class="text-[10px] opacity-70 font-normal">⌘↵</span>
		</button>
		{#if executing && canCancel}
			<button
				onclick={cancelExecution}
				class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-red-600 border border-transparent bg-transparent hover:bg-border whitespace-nowrap"
				>Cancel</button
			>
		{/if}
		<button
			onclick={explainQuery}
			disabled={!canExplain}
			title={canExplain ? 'Run an explain plan' : 'This engine does not support EXPLAIN'}
			class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent hover:bg-border hover:text-text-main whitespace-nowrap disabled:opacity-50 disabled:cursor-not-allowed"
		>
			<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"
				><circle cx="12" cy="12" r="10" /><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" /><line
					x1="12"
					y1="17"
					x2="12.01"
					y2="17"
				/></svg
			>
			Explain
		</button>
		<div class="w-px h-[18px] bg-border mx-1"></div>
		<button
			onclick={formatSQL}
			class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent hover:bg-border hover:text-text-main whitespace-nowrap"
			>Format</button
		>
		<button
			onclick={() => (saveModalOpen = true)}
			class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent hover:bg-border hover:text-text-main whitespace-nowrap"
			>Save</button
		>
		<div class="flex-1"></div>

		<div class="flex items-center gap-1.5" aria-label="Query target">
			<select
				data-testid="engine-select"
				aria-label="Engine"
				value={activeEngineId}
				onchange={(e) => selectEngine((e.currentTarget as HTMLSelectElement).value)}
				class="border border-border rounded py-0.5 px-1.5 text-[11px] bg-bg-panel cursor-pointer max-w-[150px]"
			>
				{#if engineChoices.length === 0}
					<option value="">No engine available</option>
				{/if}
				{#each engineChoices as choice (choice.id)}
					<option value={choice.id}>{choice.name}</option>
				{/each}
			</select>

			<select
				data-testid="catalog-select"
				aria-label="Catalog"
				value={selectedTarget?.binding_id ?? ''}
				onchange={(e) => selectCatalog((e.currentTarget as HTMLSelectElement).value)}
				class="border border-border rounded py-0.5 px-1.5 text-[11px] bg-bg-panel cursor-pointer max-w-[170px]"
			>
				{#if catalogChoices.length === 0}
					<option value="">No catalog</option>
				{/if}
				{#each catalogChoices as target (target.binding_id)}
					<option value={target.binding_id}>
						{target.catalog_name}{target.permission === 'read' ? ' (read-only)' : ''}
					</option>
				{/each}
			</select>

			{#if canChooseSchema}
				<select
					data-testid="schema-select"
					aria-label="Schema"
					value={activeTab.schema}
					onchange={(e) => selectSchema((e.currentTarget as HTMLSelectElement).value)}
					class="border border-border rounded py-0.5 px-1.5 text-[11px] bg-bg-panel cursor-pointer max-w-[150px]"
				>
					<option value="">Default schema</option>
					{#each namespaces as path (path.join('.'))}
						<option value={path.join('.')}>{path.join('.')}</option>
					{/each}
				</select>
				{#if !sessionSchema}
					<span
						class="text-[10px] text-text-muted"
						title="This engine does not keep a session schema; the schema is part of the SQL"
						>no session schema</span
					>
				{/if}
			{/if}
		</div>
	</div>

	{#if targetError}
		<p class="px-3 py-1 text-[11px] text-red-600" role="alert">{targetError}</p>
	{:else if namespacesError}
		<p class="px-3 py-1 text-[11px] text-red-600" role="alert">{namespacesError}</p>
	{/if}

	<div bind:this={editorEl} class="flex-1 overflow-auto min-h-0"></div>

	<div
		class="flex items-center gap-4 px-3.5 h-6 bg-bg-toolbar border-t border-b border-border text-[11px] text-text-muted shrink-0"
	>
		<span class="text-primary font-semibold">
			{selectedEngine ? `${selectedEngine.name} · ${selectedEngine.kind}` : 'No engine'}
		</span>
		<span>{selectedEngine?.version ?? 'version unknown'}</span>
		<span>Ln {cursorLine}, Col {cursorCol}</span>
		<span>{activeTab.statusText}</span>
		{#if activeTab.errorMsg}<span class="text-red-600">{activeTab.errorMsg}</span>{/if}
	</div>

	{#if activeTab.results.length > 0 || executing}
		<div class="h-[42%] flex flex-col shrink-0">
			<div class="flex items-stretch bg-bg-toolbar border-b border-border px-3 shrink-0">
				<div
					class="py-[7px] px-3 text-xs font-semibold cursor-pointer -mb-px text-primary border-b-2 border-b-primary"
				>
					Result <span
						class="bg-blue-100 text-primary text-[10px] font-bold py-px px-[5px] rounded-full"
						>{activeTab.rowCount}</span
					>
				</div>
			</div>
			<div
				class="py-1.5 px-3.5 flex items-center gap-5 border-b border-border text-xs bg-bg-panel shrink-0"
			>
				<span class="flex items-center gap-[5px] text-text-muted"
					><span class="w-[7px] h-[7px] rounded-full bg-success"></span> Finished</span
				>
				{#if activeTab.runTarget}
					<span class="flex items-center gap-1.5 text-text-muted">
						ran on {activeTab.runTarget.engine_name} ·
						{activeTab.runTarget.catalog_name}{activeTab.runTarget.schema
							? `.${activeTab.runTarget.schema}`
							: ''}
						{#if activeTab.runTarget.read_only}<Badge tone="warning">read-only</Badge>{/if}
					</span>
				{/if}
				<span class="text-text-muted">{activeTab.rowCount} rows</span>
				<span class="text-text-muted">{activeTab.durationMs}ms</span>
			</div>
			<div class="flex-1 overflow-auto">
				<table class="w-full border-separate border-spacing-0 text-left text-xs">
					<thead
						><tr
							><th
								class="bg-bg-toolbar py-[7px] px-3.5 font-semibold text-text-muted border-b border-border sticky top-0 z-[1] text-[11px] uppercase text-right w-9"
								>#</th
							>
							{#each activeTab.columns as col (col)}<th
									class="bg-bg-toolbar py-[7px] px-3.5 font-semibold text-text-muted border-b border-border sticky top-0 z-[1] text-[11px] uppercase"
									>{col}</th
								>{/each}</tr
						></thead
					>
					<tbody>
						{#each pagedRows as row, i (i)}
							<tr class="hover:[&>td]:bg-bg-toolbar">
								<td
									class="py-[7px] px-3.5 border-b border-border font-mono text-xs text-text-muted w-9 text-right opacity-60"
									>{(activeTab.page - 1) * perPage + i + 1}</td
								>
								{#each row as cell, ci (ci)}<td
										class="py-[7px] px-3.5 border-b border-border font-mono text-xs"
										>{cell === null ? '<null>' : cell}</td
									>{/each}
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<div
				class="flex items-center gap-1.5 py-1.5 px-3 border-t border-border shrink-0 bg-bg-panel text-xs text-text-muted"
			>
				<button
					disabled={activeTab.page <= 1}
					onclick={() => activeTab.page--}
					class="h-[26px] px-2.5 rounded-[5px] border border-border bg-transparent text-[11px] font-semibold cursor-pointer text-text-muted hover:enabled:border-border-hover hover:enabled:text-text-main disabled:opacity-40 disabled:cursor-not-allowed"
					>← Prev</button
				>
				<span class="text-[11px]"
					>Page <b class="text-text-main">{activeTab.page}</b> of
					<b class="text-text-main">{totalPages}</b></span
				>
				<button
					disabled={activeTab.page >= totalPages}
					onclick={() => activeTab.page++}
					class="h-[26px] px-2.5 rounded-[5px] border border-border bg-transparent text-[11px] font-semibold cursor-pointer text-text-muted hover:enabled:border-border-hover hover:enabled:text-text-main disabled:opacity-40 disabled:cursor-not-allowed"
					>Next →</button
				>
				<span class="flex-1"></span>
				<select
					onchange={() => (tabs[activeIdx].page = 1)}
					class="border border-border rounded py-0.5 px-1.5 text-[11px] bg-bg-panel cursor-pointer"
				>
					<option value={25}>25</option><option value={50}>50</option><option value={100}
						>100</option
					>
				</select>
			</div>
		</div>
	{/if}

	<Modal bind:open={saveModalOpen} title="Save Query">
		{#snippet footer()}
			<div class="flex justify-end gap-1.5">
				<button
					class="h-[34px] px-3.5 rounded-md border border-border bg-transparent text-xs font-medium text-text-muted cursor-pointer hover:border-border-hover"
					onclick={() => (saveModalOpen = false)}>Cancel</button
				>
				<button
					class="h-[34px] px-4 rounded-md border-none bg-primary text-white text-xs font-semibold cursor-pointer hover:bg-primary-hover"
					onclick={saveQuery}>Save</button
				>
			</div>
		{/snippet}
		<input
			class="w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs outline-none focus:border-primary"
			type="text"
			placeholder="Query name"
			bind:value={saveName}
			onkeydown={(e) => e.key === 'Enter' && saveQuery()}
		/>
	</Modal>
</div>
