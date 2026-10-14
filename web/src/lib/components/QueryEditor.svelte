<script lang="ts">
	import { onMount } from 'svelte';
	import { browser } from '$app/environment';
	import { basicSetup } from 'codemirror';
	import { EditorView, keymap } from '@codemirror/view';
	import { EditorState, Compartment } from '@codemirror/state';
	import { indentWithTab } from '@codemirror/commands';
	import { sql, PostgreSQL } from '@codemirror/lang-sql';
	import { oneDark } from '@codemirror/theme-one-dark';
	import { autocompletion, type CompletionContext } from '@codemirror/autocomplete';
	import { format as formatSQLFormatter } from 'sql-formatter';
	import { tableFromIPC } from 'apache-arrow';
	import { workspaceStore } from '$lib/stores/workspace.svelte';

	type Tab = { id: string; name: string; sql: string; savedId: string | null;
		results: any[]; columns: string[]; rowCount: number; durationMs: number; errorMsg: string; statusText: string; page: number; };
	// A place the workspace can run queries: a catalog bound to an engine
	// (GET /workspaces/{id}/targets).
	type Target = { binding_id: string; catalog_id: string; catalog_name: string; engine_id: string;
		engine_name: string; engine_kind: string; engine_catalog_name: string; mode: string;
		permission: 'read' | 'write'; is_default: boolean; };

	let editorView: EditorView;
	let editorEl: HTMLDivElement;
	const themeCompartment = new Compartment();

	let activeIdx = $state(0);
	let executing = $state(false);
	let executionId = $state('');
	let runningTabIdx = $state(0);
	let tabs = $state<Tab[]>([{ id: '1', name: 'Untitled', sql: 'SELECT 1;', savedId: null,
		results: [], columns: [], rowCount: 0, durationMs: 0, errorMsg: '', statusText: 'Ready', page: 1 }]);
	let dark = $state(browser && document.body.classList.contains('dark'));

	const activeTab = $derived(tabs[activeIdx]);
	let tabCounter = $state(2);
	const apiBase = '/api/v1';
	const workspaceId = $derived(workspaceStore.current?.id || '');
	const token = localStorage.getItem('token') || '';
	let targets = $state<Target[]>([]);
	let targetId = $state('');
	const selectedTarget = $derived(targets.find((t) => t.binding_id === targetId));
	let saveModalOpen = $state(false);
	let saveName = $state('');
	let perPage = $state(50);

	function switchTab(idx: number) {
		if (activeIdx !== idx) {
			tabs[activeIdx].sql = editorView.state.doc.toString();
		}
		activeIdx = idx;
		editorView.dispatch({ changes: { from: 0, to: editorView.state.doc.length, insert: tabs[idx].sql } });
	}

	function newTab() {
		tabs[activeIdx].sql = editorView.state.doc.toString();
		tabs.push({ id: String(tabCounter++), name: 'Untitled', sql: 'SELECT 1;', savedId: null,
			results: [], columns: [], rowCount: 0, durationMs: 0, errorMsg: '', statusText: 'Ready', page: 1 });
		activeIdx = tabs.length - 1;
		editorView.dispatch({ changes: { from: 0, to: editorView.state.doc.length, insert: 'SELECT 1;' } });
	}

	function closeTab(idx: number) {
		if (tabs.length <= 1) return;
		tabs.splice(idx, 1);
		if (activeIdx >= tabs.length) activeIdx = tabs.length - 1;
		const t = tabs[activeIdx];
		editorView.dispatch({ changes: { from: 0, to: editorView.state.doc.length, insert: t.sql } });
	}

	function loadQuery(q: any) {
		tabs.push({ id: String(tabCounter++), name: q.name, sql: q.sql, savedId: q.id,
			results: [], columns: [], rowCount: 0, durationMs: 0, errorMsg: '', statusText: 'Ready', page: 1 });
		activeIdx = tabs.length - 1;
		editorView.dispatch({ changes: { from: 0, to: editorView.state.doc.length, insert: q.sql } });
	}

	// Until the engine contract lands (#20) only Trino runs queries.
	const runnable = (t: Target) => t.engine_kind === 'trino';
	const targetKey = (wid: string) => `target:${wid}`;

	async function loadTargets(wid: string) {
		targets = []; targetId = '';
		if (!wid) return;
		try {
			const res = await fetch(`${apiBase}/workspaces/${wid}/targets`, { headers: { Authorization: `Bearer ${token}` } });
			if (!res.ok) return;
			targets = await res.json();
		} catch { return; }
		// The last choice in this workspace, else its default, else its only target.
		const saved = localStorage.getItem(targetKey(wid));
		const pick = targets.find((t) => t.binding_id === saved && runnable(t))
			|| targets.find((t) => t.is_default && runnable(t))
			|| (targets.length === 1 && runnable(targets[0]) ? targets[0] : undefined);
		targetId = pick?.binding_id || '';
	}

	function selectTarget(id: string) {
		targetId = id;
		if (workspaceId) localStorage.setItem(targetKey(workspaceId), id);
	}

	$effect(() => { loadTargets(workspaceId); });

	onMount(async () => {
		await workspaceStore.load();
		const observer = new MutationObserver(() => {
			dark = document.body.classList.contains('dark');
		});
		observer.observe(document.body, { attributes: true, attributeFilter: ['class'] });

		const trinoSQL = sql({ dialect: PostgreSQL });
		const extensions = [
			basicSetup, keymap.of([indentWithTab]), trinoSQL,
			autocompletion({ override: [catalogAutocomplete] }),
			themeCompartment.of(dark ? oneDark : []),
			EditorView.updateListener.of(update => {
				if (update.docChanged) {
					tabs[activeIdx].sql = update.state.doc.toString();
				}
			}),
		];

		const state = EditorState.create({ doc: tabs[activeIdx].sql, extensions });
		editorView = new EditorView({ state, parent: editorEl });

		if (typeof window !== 'undefined') {
			window.addEventListener('query-load', ((e: CustomEvent) => loadQuery(e.detail)) as EventListener);
		}

		return () => observer.disconnect();
	});

	$effect(() => {
		if (editorView) {
			editorView.dispatch({ effects: themeCompartment.reconfigure(dark ? oneDark : []) });
		}
	});

	async function runQuery() {
		const sql = editorView.state.doc.toString().trim();
		if (!sql) return;
		const idx = activeIdx;
		executing = true; tabs[idx].errorMsg = ''; tabs[idx].results = []; tabs[idx].statusText = 'Running...';
		try {
			const body: any = { workspace_id: workspaceId, sql };
			if (targetId) body.target = { binding_id: targetId };
			const res = await fetch(`${apiBase}/workspaces/${workspaceId}/executions`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
				body: JSON.stringify(body)
			});
			if (!res.ok) {
				// The API explains target problems (target_required, engine_required, …).
				const err = await res.json().catch(() => ({}));
				throw new Error(err.errors?.[0]?.message || err.message || 'Failed to start execution');
			}
			const exec = await res.json();
			executionId = exec.id;
			connectWS(executionId);
		} catch (e: any) { tabs[activeIdx].errorMsg = e.message; executing = false; tabs[activeIdx].statusText = 'Error'; }
	}

	function connectWS(execId: string) {
		const idx = activeIdx;
		const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
		const ws = new WebSocket(`${proto}//${location.host}/api/v1/ws/workspaces/${workspaceId}/executions/${execId}?token=${token}`);
		ws.onmessage = async (e) => {
			const event = JSON.parse(e.data);
			switch (event.type) {
				case 'running': tabs[idx].statusText = 'Running...'; break;
				case 'progress': tabs[idx].statusText = `Running... ${event.rows_so_far || 0} rows`; break;
				case 'finished':
					tabs[idx].statusText = 'Complete'; tabs[idx].rowCount = event.rows_returned || 0;
					tabs[idx].durationMs = event.duration_ms || 0; tabs[idx].page = 1; ws.close();
					await fetchResults(execId, idx); executing = false;
					break;
				case 'failed': tabs[idx].errorMsg = event.message || 'Query failed'; executing = false; tabs[idx].statusText = 'Failed'; ws.close(); break;
			}
		};
		ws.onerror = () => { executing = false; tabs[activeIdx].statusText = 'Connection error'; };
	}

	async function fetchResults(execId: string, idx: number) {
		try {
			const res = await fetch(`${apiBase}/workspaces/${workspaceId}/executions/${execId}/results`, { headers: { Authorization: `Bearer ${token}` } });
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			const buf = await res.arrayBuffer();
			if (buf.byteLength > 0) {
				const arr = new Uint8Array(buf);
				const table = tableFromIPC(arr);
				tabs[idx].columns = table.schema.fields.map(f => f.name);
				const decoded: any[][] = [];
				for (const row of table.toArray()) decoded.push(tabs[idx].columns.map(c => (row as any)[c]));
				tabs[idx].results = decoded; tabs[idx].rowCount = decoded.length;
			}
		} catch (e: any) { /* results unavailable */ }
	}

	function formatSQL() {
		try {
			const sql = editorView.state.doc.toString();
			const f = formatSQLFormatter(sql, { language: 'postgresql', keywordCase: 'upper', indentStyle: 'standard' });
			editorView.dispatch({ changes: { from: 0, to: editorView.state.doc.length, insert: f } });
		} catch {}
	}

	function explainQuery() {
		const sql = editorView.state.doc.toString().trim();
		if (!sql.toUpperCase().startsWith('EXPLAIN')) {
			editorView.dispatch({ changes: { from: 0, to: editorView.state.doc.length, insert: 'EXPLAIN ' + sql } });
		}
		runQuery();
	}

	async function saveQuery() {
		if (!saveName.trim() || !workspaceId) return;
		try {
			const sql = editorView.state.doc.toString();
			const savedId = tabs[activeIdx].savedId;
			const method = savedId ? 'PATCH' : 'POST';
			const url = savedId
				? `${apiBase}/workspaces/${workspaceId}/queries/${savedId}`
				: `${apiBase}/workspaces/${workspaceId}/queries`;
			const res = await fetch(url, {
				method,
				headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
				body: JSON.stringify({ name: saveName.trim(), sql })
			});
			if (!res.ok) {
				const body = await res.json().catch(() => ({}));
				tabs[activeIdx].errorMsg = body.message || body.error || 'Save failed';
				return;
			}
			const saved = await res.json();
			tabs[activeIdx].savedId = saved.id;
			tabs[activeIdx].name = saveName.trim();
			saveModalOpen = false; saveName = '';
			window.dispatchEvent(new CustomEvent('query-saved'));
		} catch (e: any) { tabs[activeIdx].errorMsg = 'Save failed: ' + e.message; }
	}

	async function catalogAutocomplete(context: CompletionContext) {
		const word = context.matchBefore(/\w+/);
		if (!word || word.from === word.to) return null;
		const wid = workspaceStore.current?.id;
		if (!wid) return null;
		try {
			const accRes = await fetch(`${apiBase}/workspaces/${wid}/catalogs`, { headers: { Authorization: `Bearer ${token}` } });
			if (!accRes.ok) return null;
			const access: any[] = await accRes.json();
			if (!access.length) return null;
			const options: any[] = [];
			for (const item of access.slice(0, 3)) {
				const connId = item.connection_id;
				const catName = item.catalog_name;
				try {
					const nsRes = await fetch(`${apiBase}/connections/${connId}/catalogs/${catName}/namespaces`, { headers: { Authorization: `Bearer ${token}` } });
					if (!nsRes.ok) continue;
					const nss: string[][] = await nsRes.json();
					for (const ns of nss.slice(0, 3)) {
						const nsName = ns.join('.');
						try {
							const tRes = await fetch(`${apiBase}/connections/${connId}/catalogs/${catName}/namespaces/${nsName}/tables`, { headers: { Authorization: `Bearer ${token}` } });
							if (!tRes.ok) continue;
							const tables = await tRes.json();
							for (const t of tables) options.push({ label: `${catName}.${nsName}.${t.name}`, type: 'table' });
						} catch {}
					}
				} catch {}
			}
			if (options.length === 0) return null;
			return { from: word.from, options };
		} catch { return null; }
	}

	function handleKeydown(e: KeyboardEvent) {
		if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); runQuery(); }
	}

	const pagedRows = $derived(activeTab.results.slice((activeTab.page - 1) * perPage, activeTab.page * perPage));
	const totalPages = $derived(Math.max(1, Math.ceil(activeTab.results.length / perPage)));
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="flex-1 flex flex-col overflow-hidden min-w-0">
	<div class="flex items-stretch bg-bg-toolbar pt-1.5 px-2 gap-0.5 border-b border-border shrink-0 overflow-x-auto">
		{#each tabs as tab, i (tab.id)}
			<button class="py-1.5 px-3.5 flex items-center gap-1.5 text-xs rounded-t-md cursor-pointer -mb-px border border-transparent border-b-0 transition-colors duration-150
				{i === activeIdx ? 'bg-bg-panel text-primary font-semibold border-border' : 'text-text-muted hover:bg-border/50'}"
				onclick={() => switchTab(i)}>
				<span class="overflow-hidden text-ellipsis whitespace-nowrap max-w-[120px]">{tab.name}</span>
				{#if tabs.length > 1}
					<span class="text-sm text-text-muted py-px px-1 rounded-[3px] hover:bg-border hover:text-text-main" onclick={(e) => { e.stopPropagation(); closeTab(i); }}>×</span>
				{/if}
			</button>
		{/each}
		<button class="flex items-center justify-center w-8 text-lg text-text-muted cursor-pointer shrink-0 bg-none border-none hover:text-primary" onclick={newTab}>+</button>
	</div>

	<div class="flex items-center py-1.5 px-3 gap-1.5 border-b border-border bg-bg-toolbar shrink-0">
		<button onclick={runQuery} disabled={executing} class="bg-success text-white border-none py-[5px] px-3.5 text-xs font-bold rounded-[5px] cursor-pointer flex items-center gap-1.5 hover:enabled:bg-success-hover disabled:opacity-60 disabled:cursor-not-allowed">
			<svg viewBox="0 0 24 24" class="w-2.5 h-2.5 fill-white stroke-none"><polygon points="5 3 19 12 5 21 5 3"/></svg>
			{executing ? 'Running...' : 'Run'}
			<span class="text-[10px] opacity-70 font-normal">⌘↵</span>
		</button>
		<button onclick={explainQuery} class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent hover:bg-border hover:text-text-main whitespace-nowrap">
			<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
			Explain
		</button>
		<div class="w-px h-[18px] bg-border mx-1"></div>
		<button onclick={formatSQL} class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent hover:bg-border hover:text-text-main whitespace-nowrap">Format</button>
		<button onclick={() => saveModalOpen = true} class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent hover:bg-border hover:text-text-main whitespace-nowrap">Save</button>
		<div class="flex-1"></div>
		<label class="flex items-center gap-1.5 text-[11px] text-text-muted">
			<span>Target</span>
			<select value={targetId} onchange={(e) => selectTarget((e.currentTarget as HTMLSelectElement).value)}
				title="Where the query runs: a catalog on an engine"
				class="border border-border rounded py-0.5 px-1.5 text-[11px] bg-bg-panel cursor-pointer max-w-[260px] {targetId ? 'text-text-main' : 'text-text-muted'}">
				<option value="">{targets.length ? 'Choose a target…' : 'No target available'}</option>
				{#each targets as t (t.binding_id)}
					<option value={t.binding_id} disabled={!runnable(t)}>
						{t.catalog_name} · {t.engine_name}{t.permission === 'read' ? ' (read-only)' : ''}{runnable(t) ? '' : ' — not runnable yet'}
					</option>
				{/each}
			</select>
		</label>
	</div>

	<div bind:this={editorEl} class="flex-1 overflow-auto min-h-0"></div>

	<div class="flex items-center gap-4 px-3.5 h-6 bg-bg-toolbar border-t border-b border-border text-[11px] text-text-muted shrink-0">
		<span class="text-primary font-semibold">{selectedTarget ? `${selectedTarget.engine_kind === 'trino' ? 'Trino' : selectedTarget.engine_kind} SQL` : 'Trino SQL'}</span>
		<span>Ln 1, Col 1</span>
		<span>{tabs[activeIdx].statusText}</span>
		{#if tabs[activeIdx].errorMsg}<span class="text-red-600">{tabs[activeIdx].errorMsg}</span>{/if}
	</div>

	{#if tabs[activeIdx].results.length > 0 || executing}
		<div class="h-[42%] flex flex-col shrink-0">
			<div class="flex items-stretch bg-bg-toolbar border-b border-border px-3 shrink-0">
				<div class="py-[7px] px-3 text-xs font-semibold cursor-pointer -mb-px text-primary border-b-2 border-b-primary">Result <span class="bg-blue-100 text-primary text-[10px] font-bold py-px px-[5px] rounded-full">{tabs[activeIdx].rowCount}</span></div>
			</div>
			<div class="py-1.5 px-3.5 flex items-center gap-5 border-b border-border text-xs bg-bg-panel shrink-0">
				<span class="flex items-center gap-[5px] text-text-muted"><span class="w-[7px] h-[7px] rounded-full bg-success"></span> Finished</span>
				<span class="text-text-muted">{tabs[activeIdx].rowCount} rows</span>
				<span class="text-text-muted">{tabs[activeIdx].durationMs}ms</span>
			</div>
			<div class="flex-1 overflow-auto">
				<table class="w-full border-separate border-spacing-0 text-left text-xs">
					<thead><tr><th class="bg-bg-toolbar py-[7px] px-3.5 font-semibold text-text-muted border-b border-border sticky top-0 z-[1] text-[11px] uppercase text-right w-9">#</th>
					{#each activeTab.columns as col}<th class="bg-bg-toolbar py-[7px] px-3.5 font-semibold text-text-muted border-b border-border sticky top-0 z-[1] text-[11px] uppercase">{col}</th>{/each}</tr></thead>
					<tbody>
						{#each pagedRows as row, i}
							<tr class="hover:[&>td]:bg-bg-toolbar">
								<td class="py-[7px] px-3.5 border-b border-border font-mono text-xs text-text-muted w-9 text-right opacity-60">{(activeTab.page - 1) * perPage + i + 1}</td>
								{#each row as cell}<td class="py-[7px] px-3.5 border-b border-border font-mono text-xs">{cell === null ? '<null>' : cell}</td>{/each}
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<div class="flex items-center gap-1.5 py-1.5 px-3 border-t border-border shrink-0 bg-bg-panel text-xs text-text-muted">
				<button disabled={activeTab.page <= 1} onclick={() => activeTab.page--} class="h-[26px] px-2.5 rounded-[5px] border border-border bg-transparent text-[11px] font-semibold cursor-pointer text-text-muted hover:enabled:border-border-hover hover:enabled:text-text-main disabled:opacity-40 disabled:cursor-not-allowed">← Prev</button>
				<span class="text-[11px]">Page <b class="text-text-main">{activeTab.page}</b> of <b class="text-text-main">{totalPages}</b></span>
				<button disabled={activeTab.page >= totalPages} onclick={() => activeTab.page++} class="h-[26px] px-2.5 rounded-[5px] border border-border bg-transparent text-[11px] font-semibold cursor-pointer text-text-muted hover:enabled:border-border-hover hover:enabled:text-text-main disabled:opacity-40 disabled:cursor-not-allowed">Next →</button>
				<span class="flex-1"></span>
				<select onchange={(e) => tabs[activeIdx].page = 1} class="border border-border rounded py-0.5 px-1.5 text-[11px] bg-bg-panel cursor-pointer">
					<option value={25}>25</option><option value={50}>50</option><option value={100}>100</option>
				</select>
			</div>
		</div>
	{/if}

	{#if saveModalOpen}
		<div class="modal-overlay fixed inset-0 bg-black/30 backdrop-blur-[2px] z-[100] flex items-center justify-center p-6" onclick={(e) => e.target === e.currentTarget && (saveModalOpen = false)}>
			<div class="modal bg-bg-panel border border-border rounded-xl w-full max-w-[360px] shadow-[0_24px_64px_rgba(0,0,0,.2)]">
				<div class="modal-header flex items-center px-5 py-[18px] border-b border-border"><h2 class="text-sm font-semibold flex-1">Save Query</h2><button class="w-[26px] h-[26px] rounded-md border-none bg-transparent cursor-pointer flex items-center justify-center text-text-muted hover:bg-bg-toolbar" onclick={() => saveModalOpen = false}>×</button></div>
				<div class="p-5">
					<input class="w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs outline-none focus:border-primary mb-3.5" type="text" placeholder="Query name" bind:value={saveName} onkeydown={(e) => e.key === 'Enter' && saveQuery()}>
					<div class="flex justify-end gap-1.5">
						<button class="h-[34px] px-3.5 rounded-md border border-border bg-transparent text-xs font-medium text-text-muted cursor-pointer hover:border-border-hover" onclick={() => saveModalOpen = false}>Cancel</button>
						<button class="h-[34px] px-4 rounded-md border-none bg-primary text-white text-xs font-semibold cursor-pointer hover:bg-primary-hover" onclick={saveQuery}>Save</button>
					</div>
				</div>
			</div>
		</div>
	{/if}
</div>
