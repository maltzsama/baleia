<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { browser } from '$app/environment';
	import { basicSetup } from 'codemirror';
	import { EditorView, keymap } from '@codemirror/view';
	import { EditorState, Compartment, Prec } from '@codemirror/state';
	import { indentWithTab } from '@codemirror/commands';
	import { sql as sqlLanguage } from '@codemirror/lang-sql';
	import { oneDark } from '@codemirror/theme-one-dark';
	import { autocompletion, closeCompletion } from '@codemirror/autocomplete';

	import { apiErrorMessage, client } from '$lib/api/client';
	import { editor } from '$lib/stores/editor.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { savedQueries, type SavedQuery } from '$lib/stores/savedQueries.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { registerProvider, unregisterProvider } from '$lib/palette/registry';
	import { modelPaletteProvider } from '$lib/semantic/palette';
	import { exploreLinkOf, listModels, migratedModelOf } from '$lib/semantic/explore';
	import { MAX_DRAFT_SQL } from '$lib/workbench/drafts';
	import type { Conflict } from '$lib/workbench/conflict';
	import CommandPalette from '$lib/workbench/CommandPalette.svelte';
	import SaveConflictDialog from '$lib/workbench/SaveConflictDialog.svelte';
	import SaveQueryDialog from '$lib/workbench/SaveQueryDialog.svelte';
	import SplitHandle from '$lib/workbench/SplitHandle.svelte';
	import { SPLIT_DEFAULT, loadSplit } from '$lib/workbench/split';
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
	import { sqlCompletionSource, type CompletionScope } from '$lib/workbench/sqlCompletion';
	import { statementSpanAt } from '$lib/workbench/sqlContext';
	import { completionConfig, completionTheme } from '$lib/workbench/completionTheme';
	import Button from '$lib/ui/Button.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import ResultsPanel from './ResultsPanel.svelte';
	import RunPanel from '$lib/run/RunPanel.svelte';
	import { applyEvent, initialRunState, type RunState } from '$lib/run/reducer';
	import {
		terminalStatus,
		subscribeExecution,
		type ExecutionEvent,
		type ExecutionStream
	} from '$lib/results/stream';

	function errorMessage(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	let editorView: EditorView;
	let editorEl: HTMLDivElement;
	let editorReady = $state(false);
	const themeCompartment = new Compartment();
	const dialectCompartment = new Compartment();
	const readOnlyCompartment = new Compartment();

	const tabs = $derived(editor.tabs);
	const activeIdx = $derived(
		Math.max(
			0,
			editor.tabs.findIndex((tab) => tab.id === editor.activeId)
		)
	);
	const activeTab = $derived(editor.activeTab);

	let executing = $state(false);
	let runState = $state<RunState | null>(null);
	let baseTitle = '';
	let dark = $state(browser && document.body.classList.contains('dark'));
	let cursorLine = $state(1);
	let cursorCol = $state(1);
	// The results area's share of the workbench height, resizable (#395).
	let resultsPct = $state(browser ? loadSplit() : SPLIT_DEFAULT);
	let rootEl: HTMLDivElement | undefined = $state();

	const workspaceId = $derived(workspaceStore.current?.id || '');
	let targets = $state<WorkspaceTarget[]>([]);
	let engines = $state<Engine[]>([]);
	let targetError = $state('');
	let dialect = $state<Dialect | null>(null);
	let namespaces = $state<string[][]>([]);
	let namespacesError = $state('');

	let saveOpen = $state(false);
	let saveMode = $state<'save' | 'copy'>('save');
	let savePrefill = $state('');
	let conflict = $state<Conflict | null>(null);
	let conflictOpen = $state(false);
	let closingIdx = $state<number | null>(null);
	let closeOpen = $state(false);
	let paletteOpen = $state(false);
	let paletteMode = $state<'commands' | 'quick'>('commands');
	let shortcutsOpen = $state(false);
	let targetFallback = $state('');
	let hydratedFor = $state('');
	const userId = $derived(authStore.user?.id ?? '');

	// ── Target resolution ────────────────────────────────────────────────
	const selectedTarget = $derived(targets.find((t) => t.binding_id === activeTab.bindingId));
	const activeEngineId = $derived(selectedTarget?.engine_id ?? '');
	const selectedEngine = $derived(engines.find((e) => e.id === activeEngineId));
	// A query saved from Explore re-opens there (#121).
	const exploreLink = $derived(exploreLinkOf(activeTab?.sql ?? ''));

	function openInExplore() {
		if (!exploreLink) return;
		flushSql();
		// The link is the explore query the tab was saved from, not a route.
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(exploreLink);
	}

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

	// Drafts are per user × workspace (#112).
	$effect(() => {
		const uid = userId;
		const wid = workspaceId;
		if (!uid || !wid) return;
		const key = `${uid}.${wid}`;
		if (hydratedFor === key) return;
		hydratedFor = key;
		void editor.hydrate(uid, wid);
	});

	// Debounced persistence. Reading the fields makes edits reschedule it.
	$effect(() => {
		const uid = userId;
		const wid = workspaceId;
		const snapshot = editor.tabs
			.map((tab) => `${tab.id}:${tab.sql}:${tab.bindingId}:${tab.schema}:${tab.dirty}`)
			.join('|');
		void snapshot;
		if (!uid || !wid) return;
		const timer = setTimeout(() => {
			void editor.persist(uid, wid);
		}, 500);
		return () => clearTimeout(timer);
	});

	// A read-only tab still runs queries but cannot edit the SQL.
	$effect(() => {
		if (!editorView) return;
		editorView.dispatch({
			effects: readOnlyCompartment.reconfigure(EditorState.readOnly.of(activeTab.readOnly))
		});
	});

	// A tab without a target starts on the workspace default, else the first.
	$effect(() => {
		const tab = activeTab;
		if (!tab || targets.length === 0) return;
		if (tab.bindingId && targets.some((t) => t.binding_id === tab.bindingId)) return;
		if (tab.savedBindingId && !targets.some((t) => t.binding_id === tab.savedBindingId)) {
			targetFallback = m.components_queryeditor_saved_target_is_not_available_in_this_work();
		}
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
		if (pick) editor.setTarget(editor.activeId, pick.binding_id, '', true);
		targetFallback = '';
	}

	function selectCatalog(bindingId: string) {
		editor.setTarget(
			editor.activeId,
			bindingId,
			workspaceStore.current?.default_schema ?? '',
			true
		);
		targetFallback = '';
	}

	function selectSchema(schema: string) {
		editor.setTarget(editor.activeId, activeTab.bindingId, schema, true);
	}

	function switchTab(idx: number) {
		flushSql();
		editor.activate(tabs[idx].id);
	}

	function newTab() {
		flushSql();
		editor.addTab();
	}

	function closeTab(idx: number) {
		flushSql();
		const tab = tabs[idx];
		if (!tab) return;
		if (tab.dirty) {
			closingIdx = idx;
			closeOpen = true;
			return;
		}
		editor.closeTab(tab.id);
	}

	function discardAndClose() {
		if (closingIdx === null) return;
		flushSql();
		const id = tabs[closingIdx]?.id;
		if (id) editor.closeTab(id);
		closingIdx = null;
		closeOpen = false;
	}

	function saveAndClose() {
		discardAndClose();
		openSave('save');
	}

	function isTextTarget(target: EventTarget | null): boolean {
		const el = target as HTMLElement | null;
		if (!el) return false;
		return !!el.closest('input, textarea, select, [contenteditable="true"], .cm-editor');
	}

	function onGlobalKeydown(event: KeyboardEvent) {
		const mod = event.metaKey || event.ctrlKey;
		if (mod && event.key.toLowerCase() === 's') {
			event.preventDefault();
			openSave(event.shiftKey ? 'copy' : 'save');
		} else if (mod && event.key.toLowerCase() === 'p') {
			event.preventDefault();
			paletteMode = 'quick';
			paletteOpen = true;
		} else if (mod && event.key.toLowerCase() === 'k') {
			event.preventDefault();
			paletteMode = 'commands';
			paletteOpen = true;
		} else if (mod && event.key.toLowerCase() === 'e') {
			event.preventDefault();
			void explainQuery();
		} else if (event.shiftKey && event.altKey && event.key.toLowerCase() === 'f') {
			event.preventDefault();
			void formatSQL();
		} else if (event.altKey && event.key.toLowerCase() === 'n') {
			event.preventDefault();
			newTab();
		} else if (event.altKey && event.key.toLowerCase() === 'w') {
			event.preventDefault();
			closeTab(activeIdx);
		} else if (event.key === '?' && !isTextTarget(event.target)) {
			shortcutsOpen = true;
		}
	}

	function onBeforeUnload(event: BeforeUnloadEvent) {
		flushSql();
		if (editor.dirty) {
			event.preventDefault();
			event.returnValue = '';
		}
	}

	function registerPalette() {
		registerProvider({
			id: 'actions',
			label: 'Actions',
			search: (query) => {
				const q = query.toLowerCase();
				return [
					{ id: 'run-all', label: m.components_queryeditor_run_all(), run: () => void runAll() },
					{
						id: 'run-current',
						label: m.components_queryeditor_run_current_statement(),
						run: () => void runCurrent()
					},
					{ id: 'format', label: m.components_queryeditor_format_sql(), run: formatSQL },
					{
						id: 'explain',
						label: m.components_queryeditor_explain_current_query(),
						run: () => void explainQuery()
					},
					{ id: 'new-tab', label: m.components_queryeditor_new_tab(), run: newTab },
					{ id: 'save', label: 'Save', run: () => openSave('save') },
					{
						id: 'save-copy',
						label: m.components_queryeditor_save_as_a_copy(),
						run: () => openSave('copy')
					},
					{
						id: 'quick-open',
						label: 'Quick open a saved query',
						run: () => {
							paletteMode = 'quick';
							paletteOpen = true;
						}
					},
					{
						id: 'shortcuts',
						label: m.components_queryeditor_keyboard_shortcuts(),
						run: () => (shortcutsOpen = true)
					}
				]
					.filter((action) => action.label.toLowerCase().includes(q))
					.map((action) => ({ ...action, group: 'Actions' }));
			}
		});
		registerProvider({
			id: 'queries',
			label: 'Saved queries',
			search: async (query) => {
				if (workspaceId && savedQueries.workspaceId !== workspaceId)
					await savedQueries.reload(workspaceId);
				const q = query.trim().toLowerCase();
				return savedQueries.list
					.filter(
						(item) =>
							!q || item.name.toLowerCase().includes(q) || item.slug.toLowerCase().includes(q)
					)
					.slice(0, 20)
					.map((item) => ({
						id: `query-${item.id}`,
						label: item.name,
						detail: item.slug,
						group: 'Saved queries',
						run: () => {
							editor.openSavedQuery(item, !savedQueries.canEdit(item.space_id));
						}
					}));
			}
		});
		registerProvider({
			id: 'tables',
			label: 'Tables',
			search: async (query) => {
				const q = query.trim();
				if (q.length < 2) return [];
				const results = await searchMetadata(workspaceId, q, { kind: 'all', limit: 10 });
				return results
					.filter((result) => result.kind === 'table')
					.map((result) => ({
						id: `table-${result.table_id}`,
						label: result.table,
						detail: result.namespace.join('.'),
						group: 'Tables',
						run: () => editor.insert(`${result.namespace.join('.')}.${result.table}`)
					}));
			}
		});
		registerProvider({
			id: 'metrics',
			label: 'Metrics',
			search: async (query) => {
				const q = query.trim();
				if (!workspaceId) return [];
				const { data } = await client.GET('/workspaces/{id}/semantic/metrics', {
					params: { path: { id: workspaceId }, query: { ...(q ? { q } : {}), limit: 20 } }
				});
				const tabBinding = editor.activeTab?.bindingId ?? '';
				// A v1 metric migrated into a model is offered by "Insert from model".
				const models = await listModels(workspaceId).catch(() => []);
				return (data ?? [])
					.filter((metric) => !migratedModelOf(models, metric))
					.slice(0, 20)
					.map((metric) => ({
						id: `metric-${metric.id}`,
						label: metric.label,
						detail: metric.slug,
						group: 'Metrics',
						run: async () => {
							const { data: rendered } = await client.GET(
								'/workspaces/{id}/semantic/metrics/{metric_id}/sql',
								{
									params: {
										path: { id: workspaceId, metric_id: metric.id },
										query: tabBinding ? { binding_id: tabBinding } : {}
									}
								}
							);
							if (rendered?.sql) editor.insert(rendered.sql);
						}
					}));
			}
		});
		registerProvider(
			modelPaletteProvider({
				workspaceId: () => workspaceId,
				bindingId: () => editor.activeTab?.bindingId ?? '',
				insert: (sql) => editor.insert(sql)
			})
		);
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

	// ── Autocomplete (#383) ──────────────────────────────────────────────
	// The source reads the active tab's target on every request, so switching
	// catalog, schema or engine needs no reconfiguration.
	const completionScope = $derived<CompletionScope | null>(
		selectedTarget
			? {
					workspaceId,
					catalogId: selectedTarget.catalog_id,
					bindingId: selectedTarget.binding_id,
					engineCatalog: selectedTarget.engine_catalog_name,
					namespaceDepth: selectedEngine?.capabilities?.namespace_depth ?? 0,
					schema: activeTab.schema || workspaceStore.current?.default_schema || '',
					keywords: dialect?.keywords ?? [],
					functions: dialect?.functions ?? [],
					cmDialect: codemirrorDialect(selectedEngine?.dialect?.codemirror_base),
					upperCase: true
				}
			: null
	);
	const complete = sqlCompletionSource(() => completionScope);

	// The live document lives in CodeMirror; `tab.sql` is a debounced mirror so a
	// burst of keystrokes does not serialize the whole doc, rebuild the draft
	// snapshot and compare `dirty` on every keypress (#233). It is flushed on blur
	// and before any action that reads or switches the tab (save, close, switch).
	let sqlSyncTimer: ReturnType<typeof setTimeout> | undefined;

	function flushSql() {
		if (!editorView) return;
		if (sqlSyncTimer) {
			clearTimeout(sqlSyncTimer);
			sqlSyncTimer = undefined;
		}
		editor.updateSql(editor.activeId, editorView.state.doc.toString());
	}

	function scheduleSqlSync() {
		if (sqlSyncTimer) return;
		sqlSyncTimer = setTimeout(() => {
			sqlSyncTimer = undefined;
			if (editorView) editor.updateSql(editor.activeId, editorView.state.doc.toString());
		}, 150);
	}

	onMount(() => {
		registerPalette();
		window.addEventListener('keydown', onGlobalKeydown, true);
		window.addEventListener('beforeunload', onBeforeUnload);
		document.addEventListener('visibilitychange', onVisibility);
		const observer = new MutationObserver(() => {
			dark = document.body.classList.contains('dark');
		});
		observer.observe(document.body, { attributes: true, attributeFilter: ['class'] });

		const extensions = [
			basicSetup,
			// The run keys outrank basicSetup's defaultKeymap, which binds
			// Mod-Enter to insertBlankLine: it won, and ⌘/Ctrl+Enter only added
			// a line (#395).
			Prec.highest(
				keymap.of([
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
				])
			),
			keymap.of([indentWithTab]),
			dialectCompartment.of(
				sqlLanguage({ dialect: codemirrorDialect(undefined), upperCaseKeywords: true })
			),
			readOnlyCompartment.of(EditorState.readOnly.of(false)),
			autocompletion({ ...completionConfig, override: [complete] }),
			completionTheme,
			EditorView.contentAttributes.of({ 'aria-label': m.components_queryeditor_sql_editor() }),
			themeCompartment.of(dark ? oneDark : []),
			EditorView.updateListener.of((update) => {
				if (update.docChanged) {
					scheduleSqlSync();
				}
				if (update.focusChanged && !editorView.hasFocus) {
					flushSql();
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
		editorReady = true;

		return () => {
			observer.disconnect();
			window.removeEventListener('keydown', onGlobalKeydown, true);
			window.removeEventListener('beforeunload', onBeforeUnload);
			document.removeEventListener('visibilitychange', onVisibility);
			unregisterProvider('actions');
			unregisterProvider('metrics');
			unregisterProvider('models');
			unregisterProvider('queries');
			unregisterProvider('tables');
			stream?.close();
		};
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

	// "Re-run" from history (#36): run the tab the store opened.
	let appliedRunSeq = 0;
	$effect(() => {
		const run = editor.pendingRun;
		if (!editorReady || !run || run.seq === appliedRunSeq) return;
		appliedRunSeq = run.seq;
		if (run.tabId !== editor.activeId) return;
		void runAll();
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
		// A run is not a completion choice: drop an open suggestion list.
		if (editorView) closeCompletion(editorView);
		const idx = activeIdx;
		const tab = tabs[idx];
		if (!tab.bindingId) {
			tab.errorMsg = m.components_queryeditor_choose_an_engine_and_catalog_to_run_on();
			tab.statusText = m.components_queryeditor_no_target();
			return;
		}
		executing = true;
		tab.errorMsg = '';
		tab.results = [];
		tab.statusText = m.components_queryeditor_running();
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
				throw new Error(
					apiErrorMessage(error, m.components_queryeditor_failed_to_start_execution())
				);
			}
			tab.runTarget = data.target ?? null;
			editor.setExecution(tab.id, data.id);
			runState = initialRunState(data.id);
			connectWS(data.id, idx);
		} catch (e) {
			tab.errorMsg = errorMessage(e);
			executing = false;
			tab.statusText = m.components_queryeditor_error();
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
		const head = selection.head;
		const name = selectedEngine?.dialect?.name;
		// Run never falls back to the whole script, which is Run all's job: the
		// engine's splitter knows its dialect, and the local one stands in when
		// it is unavailable (#395).
		let span: { start: number; end: number } | null | undefined;
		if (name) {
			try {
				const spans = await splitStatements(name, full);
				span =
					spans.find((s) => s.start <= head && head <= s.end) ??
					[...spans].reverse().find((s) => s.end <= head) ??
					spans[0];
			} catch {
				span = undefined;
			}
		}
		span ??= statementSpanAt(full, head);
		if (span) await submit(full.slice(span.start, span.end));
	}

	async function cancelExecution() {
		const tab = activeTab;
		if (!tab?.executionId) return;
		try {
			tab.statusText = m.components_queryeditor_cancelling();
			const { error } = await client.POST('/workspaces/{id}/executions/{execution_id}/cancel', {
				params: { path: { id: workspaceId, execution_id: tab.executionId } }
			});
			if (error) throw new Error(apiErrorMessage(error, 'Could not cancel'));
		} catch (e) {
			tab.errorMsg = errorMessage(e);
		}
	}

	let stream: ExecutionStream | null = null;

	// The results load once the run ended, from its state, never from the
	// translated status line (#387).
	const runEnded = $derived(
		!!runState && ['finished', 'failed', 'cancelled'].includes(runState.status)
	);
	const runHasResults = $derived(
		!!runState && Object.values(runState.statements).some((statement) => statement.resultCount > 0)
	);

	function executionStatusText(status: string): string {
		switch (status) {
			case 'finished':
				return m.components_queryeditor_complete();
			case 'failed':
				return 'Failed';
			case 'cancelled':
				return 'Cancelled';
			default:
				return m.components_queryeditor_running();
		}
	}

	function notifyBackground(status: string) {
		if (typeof document === 'undefined' || !document.hidden) return;
		if (!baseTitle) baseTitle = document.title;
		document.title = `${status === 'finished' ? '✓' : '✗'} ${baseTitle}`;
	}

	function onVisibility() {
		if (typeof document !== 'undefined' && !document.hidden && baseTitle) {
			document.title = baseTitle;
			baseTitle = '';
		}
	}

	function connectWS(execId: string, idx: number) {
		stream?.close();
		const tab = tabs[idx];
		stream = subscribeExecution({
			workspaceId,
			executionId: execId,
			onEvent: (event: ExecutionEvent) => {
				runState = applyEvent(runState ?? initialRunState(execId), event);
				if (event.type === 'statement_status' && event.status === 'running') {
					tab.statusText = `Running statement ${event.position}…`;
					return;
				}
				// A snapshot can already be final (the run ended before the socket).
				const done = terminalStatus(event);
				if (done) {
					tab.statusText = executionStatusText(done);
					executing = false;
					notifyBackground(done);
					return;
				}
				if (event.type === 'execution_status') tab.statusText = executionStatusText(event.status);
			},
			onGaveUp: () => {
				executing = false;
				tab.statusText = m.components_queryeditor_connection_lost();
			}
		});
	}

	async function rerun(fromPosition: number) {
		const tab = activeTab;
		if (!tab?.executionId || !workspaceId) return;
		try {
			const { data, error } = await client.POST('/workspaces/{id}/executions', {
				params: { path: { id: workspaceId } },
				body: {
					workspace_id: workspaceId,
					sql: tab.sql,
					rerun: { execution_id: tab.executionId, from_position: fromPosition }
				}
			});
			if (error || !data) throw new Error(apiErrorMessage(error, 'Could not re-run'));
			tab.results = [];
			tab.errorMsg = '';
			tab.statusText = m.components_queryeditor_running();
			executing = true;
			editor.setExecution(tab.id, data.id);
			runState = initialRunState(data.id);
			connectWS(data.id, activeIdx);
		} catch (e) {
			tab.errorMsg = errorMessage(e);
		}
	}

	async function formatSQL() {
		if (!editorView) return;
		try {
			// sql-formatter is heavy and only used on demand (#233): load it lazily.
			const { format: formatSQLFormatter } = await import('sql-formatter');
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

	async function explainQuery() {
		if (!editorView) return;
		const name = selectedEngine?.dialect?.name;
		if (!name) return;
		const sqlText = editorView.state.doc.toString().trim();
		if (!sqlText) return;
		try {
			const { data, error } = await client.POST('/dialects/{name}/explain', {
				params: { path: { name } },
				body: { sql: sqlText }
			});
			if (error || !data) {
				activeTab.errorMsg = apiErrorMessage(
					error,
					m.components_queryeditor_could_not_explain_the_query()
				);
				return;
			}
			if (!data.supported || !data.sql) {
				activeTab.errorMsg = m.components_queryeditor_this_engine_does_not_support_explain();
				return;
			}
			await submit(data.sql);
		} catch (e) {
			activeTab.errorMsg = errorMessage(e);
		}
	}

	function openSave(mode: 'save' | 'copy', prefill = '') {
		flushSql();
		if (workspaceId && savedQueries.spaces.length === 0) void savedQueries.reload(workspaceId);
		saveMode = mode;
		savePrefill = prefill;
		saveOpen = true;
	}

	function onSaved(query: SavedQuery) {
		editor.markSaved(editor.activeId, query);
		savedQueries.onSaved(query);
	}

	function onConflict(details: Conflict) {
		conflict = details;
		conflictOpen = true;
	}
</script>

<div
	bind:this={rootEl}
	class="flex-1 flex flex-col overflow-hidden min-w-0"
	style:--font-editor="'Sometype Mono', var(--font-mono)"
>
	<div
		class="flex items-stretch bg-bg-toolbar pt-1.5 px-2 gap-0.5 border-b border-border shrink-0 overflow-x-auto"
	>
		{#each tabs as tab, i (tab.id)}
			<div
				class="flex items-center gap-0.5 rounded-t-md -mb-px border border-transparent border-b-0 transition-colors duration-150
				{i === activeIdx ? 'bg-bg-panel border-border' : 'hover:bg-border/50'}"
			>
				<button
					type="button"
					class="flex items-center py-1.5 pl-3.5 pr-1.5 text-xs cursor-pointer {i === activeIdx
						? 'text-primary font-semibold'
						: 'text-text-muted'}"
					onclick={() => switchTab(i)}
				>
					<span class="block max-w-[120px] overflow-hidden text-ellipsis whitespace-nowrap"
						>{tab.name}</span
					>
				</button>
				{#if tabs.length > 1}
					<button
						type="button"
						aria-label={m.components_queryeditor_close_tab()}
						class="mr-1.5 rounded-[3px] px-1 py-px text-sm text-text-muted hover:bg-border hover:text-text-main"
						onclick={() => closeTab(i)}>×</button
					>
				{/if}
			</div>
		{/each}
		<button
			class="flex items-center justify-center w-8 text-lg text-text-muted cursor-pointer shrink-0 bg-none border-none hover:text-primary"
			onclick={newTab}>+</button
		>
	</div>

	<div class="flex items-center py-1.5 px-3 gap-1.5 border-b border-border bg-bg-toolbar shrink-0">
		<!-- Run is the selection, or the statement at the cursor, like ⌘↵;
		     Run all is the whole script (#395). -->
		<button
			onclick={() => void runCurrent()}
			disabled={executing}
			title={m.components_queryeditor_run_hint()}
			data-testid="run-current"
			class="bg-success text-white border-none py-[5px] px-3.5 text-xs font-bold rounded-[5px] cursor-pointer flex items-center gap-1.5 hover:enabled:bg-success-hover disabled:opacity-60 disabled:cursor-not-allowed"
		>
			<svg viewBox="0 0 24 24" class="w-2.5 h-2.5 fill-white stroke-none"
				><polygon points="5 3 19 12 5 21 5 3" /></svg
			>
			{executing ? m.components_queryeditor_running() : m.components_queryeditor_run()}
			<span class="text-[10px] opacity-70 font-normal">⌘↵</span>
		</button>
		<button
			onclick={() => void runAll()}
			disabled={executing}
			title={m.components_queryeditor_run_all_hint()}
			data-testid="run-all"
			class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent hover:bg-border hover:text-text-main whitespace-nowrap disabled:opacity-50 disabled:cursor-not-allowed"
		>
			{m.components_queryeditor_run_all()}
			<span class="text-[10px] opacity-70 font-normal">⇧⌘↵</span>
		</button>
		{#if executing && canCancel}
			<button
				onclick={cancelExecution}
				class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-red-600 border border-transparent bg-transparent hover:bg-border whitespace-nowrap"
				>{m.common_cancel()}</button
			>
		{/if}
		<button
			onclick={explainQuery}
			disabled={!canExplain}
			title={canExplain
				? m.components_queryeditor_run_an_explain_plan()
				: m.components_queryeditor_this_engine_does_not_support_explain_2()}
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
			{m.components_queryeditor_explain()}
		</button>
		<div class="w-px h-[18px] bg-border mx-1"></div>
		<button
			onclick={formatSQL}
			class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent hover:bg-border hover:text-text-main whitespace-nowrap"
			>{m.components_queryeditor_format()}</button
		>
		<button
			onclick={() => openSave(activeTab.readOnly ? 'copy' : 'save')}
			class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent hover:bg-border hover:text-text-main whitespace-nowrap"
			>{activeTab.readOnly ? m.components_queryeditor_save_a_copy() : 'Save'}</button
		>
		<button
			onclick={() => {
				paletteMode = 'commands';
				paletteOpen = true;
			}}
			class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent hover:bg-border hover:text-text-main whitespace-nowrap"
			>{m.components_queryeditor_commands()}</button
		>
		{#if exploreLink}
			<button
				onclick={openInExplore}
				class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-primary border border-transparent bg-transparent hover:bg-border whitespace-nowrap"
				>{m.explore_open_in_explore()}</button
			>
		{/if}
		<div class="flex-1"></div>

		<div class="flex items-center gap-1.5" aria-label={m.components_queryeditor_query_target()}>
			<select
				data-testid="engine-select"
				aria-label={m.common_engine()}
				value={activeEngineId}
				onchange={(e) => selectEngine((e.currentTarget as HTMLSelectElement).value)}
				class="border border-border rounded py-0.5 px-1.5 text-[11px] bg-bg-panel cursor-pointer max-w-[150px]"
			>
				{#if engineChoices.length === 0}
					<option value="">{m.components_queryeditor_no_engine_available()}</option>
				{/if}
				{#each engineChoices as choice (choice.id)}
					<option value={choice.id}>{choice.name}</option>
				{/each}
			</select>

			<select
				data-testid="catalog-select"
				aria-label={m.common_catalog()}
				value={selectedTarget?.binding_id ?? ''}
				onchange={(e) => selectCatalog((e.currentTarget as HTMLSelectElement).value)}
				class="border border-border rounded py-0.5 px-1.5 text-[11px] bg-bg-panel cursor-pointer max-w-[170px]"
			>
				{#if catalogChoices.length === 0}
					<option value="">{m.components_queryeditor_no_catalog()}</option>
				{/if}
				{#each catalogChoices as target (target.binding_id)}
					<option value={target.binding_id}>
						{target.catalog_name}{target.permission === 'read'
							? m.components_queryeditor_read_only()
							: ''}
					</option>
				{/each}
			</select>

			{#if canChooseSchema}
				<select
					data-testid="schema-select"
					aria-label={m.components_queryeditor_schema()}
					value={activeTab.schema}
					onchange={(e) => selectSchema((e.currentTarget as HTMLSelectElement).value)}
					class="border border-border rounded py-0.5 px-1.5 text-[11px] bg-bg-panel cursor-pointer max-w-[150px]"
				>
					<option value="">{m.components_queryeditor_default_schema()}</option>
					{#each namespaces as path (path.join('.'))}
						<option value={path.join('.')}>{path.join('.')}</option>
					{/each}
				</select>
				{#if !sessionSchema}
					<span
						class="text-[10px] text-text-muted"
						title={m.components_queryeditor_this_engine_does_not_keep_a_session_schema()}
						>{m.components_queryeditor_no_session_schema()}</span
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

	{#if activeTab.readOnly}
		<div
			class="flex items-center gap-2 border-b border-border bg-amber-50 px-3 py-1.5 text-[11px] text-amber-800 dark:bg-[#241a06] dark:text-amber-300"
		>
			<span>{m.components_queryeditor_view_only_you_can_run_this_query_or_save_a()}</span>
			<button class="font-semibold underline" onclick={() => openSave('copy')}
				>{m.components_queryeditor_save_a_copy()}</button
			>
		</div>
	{/if}
	{#if targetFallback}
		<p
			class="border-b border-border bg-bg-toolbar px-3 py-1 text-[11px] text-slate-600 dark:text-slate-300"
		>
			{targetFallback}
		</p>
	{/if}
	{#if activeTab.sql.length > MAX_DRAFT_SQL}
		<p class="border-b border-border bg-bg-toolbar px-3 py-1 text-[11px] text-amber-700">
			{m.components_queryeditor_too_large_to_keep_as_a_draft()}
		</p>
	{/if}

	<div bind:this={editorEl} class="flex-1 overflow-auto min-h-0"></div>

	<div
		class="flex items-center gap-4 px-3.5 h-6 bg-bg-toolbar border-t border-b border-border text-[11px] text-text-muted shrink-0"
	>
		<span class="text-primary font-semibold">
			{selectedEngine ? `${selectedEngine.name} · ${selectedEngine.kind}` : 'No engine'}
		</span>
		<span>{selectedEngine?.version ?? m.components_queryeditor_version_unknown()}</span>
		<span
			>{m.components_queryeditor_ln()}
			{cursorLine}{m.components_queryeditor_col()}
			{cursorCol}</span
		>
		<!-- Once the run ended, its panel below says how (#393). -->
		{#if !runEnded}<span>{activeTab.statusText}</span>{/if}
		{#if activeTab.errorMsg}<span class="text-red-600">{activeTab.errorMsg}</span>{/if}
	</div>

	{#if activeTab.executionId && runState}
		<SplitHandle bind:value={resultsPct} container={() => rootEl} />
		<div class="flex shrink-0 flex-col" style:height="{resultsPct}%" data-testid="results-area">
			<RunPanel
				state={runState}
				{executing}
				{canCancel}
				oncancel={cancelExecution}
				onrerun={rerun}
				onpick={() => editorView?.focus()}
			/>
			<!-- A run that ended without any result set (a failed statement, a DDL
			     or DML) has nothing to show below the run panel (#387). A SELECT
			     with no rows still has a result set: its columns. -->
			{#if !runEnded || runHasResults}
				<!-- A flex column, so the grid gets a bounded height and scrolls (#393). -->
				<div class="flex min-h-0 flex-1 flex-col">
					<ResultsPanel executionId={activeTab.executionId} enabled={runEnded} />
				</div>
			{/if}
		</div>
	{/if}

	<SaveQueryDialog
		bind:open={saveOpen}
		tab={activeTab}
		{workspaceId}
		spaces={savedQueries.spaces}
		folders={savedQueries.folders}
		mode={saveMode}
		prefillName={savePrefill}
		onconflict={onConflict}
		onsaved={onSaved}
	/>

	{#if conflict}
		<SaveConflictDialog
			bind:open={conflictOpen}
			tab={activeTab}
			{workspaceId}
			{conflict}
			onsaved={onSaved}
			oncopy={() => openSave('copy', `${activeTab.name} (copy)`)}
		/>
	{/if}

	<CommandPalette bind:open={paletteOpen} mode={paletteMode} />

	<Modal bind:open={closeOpen} title={m.components_queryeditor_close_this_tab()}>
		<p class="text-[13px] text-slate-600 dark:text-slate-300">
			{m.components_queryeditor_this_tab_has_unsaved_changes_save_them_bef()}
		</p>
		{#snippet footer()}
			<div class="flex justify-end gap-2">
				<Button onclick={() => (closeOpen = false)}>{m.common_cancel()}</Button>
				<Button onclick={discardAndClose}>{m.components_queryeditor_discard()}</Button>
				<Button variant="primary" onclick={saveAndClose}>{m.common_save()}</Button>
			</div>
		{/snippet}
	</Modal>

	<Modal bind:open={shortcutsOpen} title={m.components_queryeditor_keyboard_shortcuts()}>
		<ul class="grid gap-1 text-[12px] text-slate-600 dark:text-slate-300">
			{#each [['Run current statement', 'Mod-Enter'], ['Run all', 'Shift-Mod-Enter'], ['Save', 'Mod-S'], ['Save as a copy', 'Shift-Mod-S'], ['Quick open', 'Mod-P'], ['Command palette', 'Mod-K'], ['Format', 'Shift-Alt-F'], ['Explain', 'Mod-E'], ['New tab', 'Alt-N'], ['Close tab', 'Alt-W']] as [label, keys] (label)}
				<li class="flex justify-between gap-4">
					<span>{label}</span>
					<kbd class="font-mono text-[11px]">{keys}</kbd>
				</li>
			{/each}
		</ul>
	</Modal>
</div>
