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

	let editorView: EditorView;
	let editorEl: HTMLDivElement;
	const themeCompartment = new Compartment();

	let executing = $state(false);
	let sqlCode = $state('SELECT 1;');
	let executionId = $state('');
	let results: any[] = $state([]);
	let columns: string[] = $state([]);
	let rowCount = $state(0);
	let durationMs = $state(0);
	let errorMsg = $state('');
	let statusText = $state('Ready');

	let page = $state(1);
	let perPage = $state(50);

	let dark = $state(browser && document.body.classList.contains('dark'));

	const apiBase = '/api/v1';
	let workspaceId = $state('');
	const token = localStorage.getItem('token') || '';

	onMount(async () => {
		if (!workspaceId) {
			try {
				const res = await fetch(`${apiBase}/workspaces`, { headers: { Authorization: `Bearer ${token}` } });
				if (res.ok) {
					const workspaces = await res.json();
					if (workspaces.length > 0) {
						workspaceId = workspaces[0].id;
					}
				}
			} catch {}
		}
	});

	onMount(() => {
		const observer = new MutationObserver(() => {
			dark = document.body.classList.contains('dark');
		});
		observer.observe(document.body, { attributes: true, attributeFilter: ['class'] });

		const trinoSQL = sql({
			dialect: PostgreSQL,
			schema: {},
			tables: [],
		});

		const extensions = [
			basicSetup,
			keymap.of([indentWithTab]),
			trinoSQL,
			autocompletion({ override: [catalogAutocomplete] }),
			themeCompartment.of(dark ? oneDark : []),
			EditorView.updateListener.of(update => {
				if (update.docChanged) sqlCode = update.state.doc.toString();
			}),
		];

		const state = EditorState.create({ doc: sqlCode, extensions });
		editorView = new EditorView({ state, parent: editorEl });

		return () => observer.disconnect();
	});

	$effect(() => {
		if (editorView) {
			editorView.dispatch({ effects: themeCompartment.reconfigure(dark ? oneDark : []) });
		}
	});

	async function runQuery() {
		if (!sqlCode.trim()) return;
		executing = true;
		errorMsg = '';
		results = [];
		statusText = 'Running...';

		try {
			const res = await fetch(`${apiBase}/workspaces/${workspaceId}/executions`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
				body: JSON.stringify({ workspace_id: workspaceId, sql: sqlCode })
			});
			if (!res.ok) throw new Error('Failed to start execution');
			const exec = await res.json();
			executionId = exec.id;
			connectWS(executionId);
		} catch (e: any) {
			errorMsg = e.message;
			executing = false;
			statusText = 'Error';
		}
	}

	function connectWS(execId: string) {
		const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
		const ws = new WebSocket(`${proto}//${location.host}/api/v1/ws/workspaces/${workspaceId}/executions/${execId}?token=${token}`);

		ws.onmessage = async (e) => {
			const event = JSON.parse(e.data);
			switch (event.type) {
				case 'running':
					statusText = 'Running...';
					break;
				case 'progress':
					statusText = `Running... ${event.rows_so_far || 0} rows`;
					break;
			case 'finished':
				statusText = 'Complete';
				rowCount = event.rows_returned || 0;
				durationMs = event.duration_ms || 0;
				page = 1;
				ws.close();
				await fetchResults(execId);
				executing = false;
				break;
				case 'failed':
					errorMsg = event.message || 'Query failed';
					executing = false;
					statusText = 'Failed';
					ws.close();
					break;
			}
		};

		ws.onerror = () => {
			executing = false;
			statusText = 'Connection error';
		};
	}

	async function fetchResults(execId: string) {
		try {
			const res = await fetch(`${apiBase}/workspaces/${workspaceId}/executions/${execId}/results`, {
				headers: { Authorization: `Bearer ${token}` }
			});
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			const buf = await res.arrayBuffer();
			console.log('Results buffer size:', buf.byteLength);
			if (buf.byteLength > 0) {
				const arr = new Uint8Array(buf);
				const table = tableFromIPC(arr);
				columns = table.schema.fields.map((f) => f.name);
				const decoded: any[][] = [];
				for (const row of table.toArray()) {
					decoded.push(columns.map((c) => (row as any)[c]));
				}
				results = decoded;
				rowCount = decoded.length;
				console.log('Rows loaded:', decoded.length, 'Columns:', columns.length);
			}
		} catch (e: any) {
			errorMsg = 'Fetch results: ' + (e.message || String(e));
			console.error('fetchResults error:', e);
		}
	}

	function formatSQL() {
		try {
			const formatted = formatSQLFormatter(sqlCode, {
				language: 'postgresql',
				keywordCase: 'upper',
				indentStyle: 'standard',
			});
			sqlCode = formatted;
			editorView.dispatch({
				changes: { from: 0, to: editorView.state.doc.length, insert: formatted }
			});
		} catch { /* keep original on error */ }
	}

	function explainQuery() {
		const current = sqlCode.trim();
		if (!current.toUpperCase().startsWith('EXPLAIN')) {
			sqlCode = 'EXPLAIN ' + current;
			editorView.dispatch({
				changes: { from: 0, to: editorView.state.doc.length, insert: sqlCode }
			});
		}
		runQuery();
	}

	async function catalogAutocomplete(context: CompletionContext) {
		const word = context.matchBefore(/\w+/);
		if (!word || word.from === word.to) return null;

		const token = localStorage.getItem('token') || '';
		const apiBase = '/api/v1';

		try {
			const connRes = await fetch(`${apiBase}/catalog-connections`, { headers: { Authorization: `Bearer ${token}` } });
			if (!connRes.ok) return null;
			const connections = await connRes.json();
			if (!connections.length) return null;

			const connId = connections[0].id;
			const catRes = await fetch(`${apiBase}/connections/${connId}/catalogs`, { headers: { Authorization: `Bearer ${token}` } });
			if (!catRes.ok) return null;
			const catalogs = await catRes.json();
			if (!catalogs.length) return null;

			const options: any[] = [];
			for (const cat of catalogs.slice(0, 3)) {
				try {
					const nsRes = await fetch(`${apiBase}/catalogs/${cat.id}/namespaces`, { headers: { Authorization: `Bearer ${token}` } });
					if (!nsRes.ok) continue;
					const namespaces: string[][] = await nsRes.json();
					for (const ns of namespaces.slice(0, 3)) {
						const nsName = ns.join('.');
						try {
							const tRes = await fetch(`${apiBase}/catalogs/${cat.id}/namespaces/${nsName}/tables`, { headers: { Authorization: `Bearer ${token}` } });
							if (!tRes.ok) continue;
							const tables = await tRes.json();
							for (const t of tables) {
								options.push({ label: `${cat.name}.${nsName}.${t.name}`, type: 'table', detail: 'table' });
							}
						} catch {}
					}
				} catch {}
			}

			if (options.length === 0) return null;
			return { from: word.from, options };
		} catch {
			return null;
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
			e.preventDefault();
			runQuery();
		}
	}

	const pagedRows = $derived(results.slice((page - 1) * perPage, page * perPage));
	const totalPages = $derived(Math.max(1, Math.ceil(results.length / perPage)));
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="flex-1 flex flex-col overflow-hidden min-w-0">

	<div class="flex items-stretch bg-bg-toolbar pt-1.5 px-2 gap-0.5 border-b border-border shrink-0">
		<div class="py-1.5 px-3.5 flex items-center gap-1.5 text-xs rounded-t-md cursor-pointer -mb-px border border-transparent border-b-0
			bg-bg-panel text-primary font-semibold" style="border-color: var(--color-border)">
			<span class="overflow-hidden text-ellipsis whitespace-nowrap flex-1 min-w-0">Untitled Query</span>
			<span class="opacity-0 text-sm text-text-muted py-px px-1 rounded-[3px] group-hover:opacity-100 hover:bg-border hover:text-text-main">×</span>
		</div>
		<button class="flex items-center justify-center w-8 text-lg text-text-muted cursor-pointer shrink-0 transition-colors duration-150 bg-none border-none hover:text-primary">+</button>
	</div>

	<div class="flex items-center py-1.5 px-3 gap-1.5 border-b border-border bg-bg-toolbar shrink-0">
		<button
			onclick={runQuery}
			disabled={executing}
			class="bg-success text-white border-none py-[5px] px-3.5 text-xs font-bold rounded-[5px] cursor-pointer flex items-center gap-1.5 transition-colors duration-150
				hover:enabled:bg-success-hover disabled:opacity-60 disabled:cursor-not-allowed">
			<svg viewBox="0 0 24 24" class="w-2.5 h-2.5 fill-white stroke-none"><polygon points="5 3 19 12 5 21 5 3"/></svg>
			{executing ? 'Running...' : 'Run'}
			<span class="text-[10px] opacity-70 font-normal">⌘↵</span>
		</button>
		<button onclick={explainQuery} class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent transition-all duration-150 whitespace-nowrap hover:bg-border hover:text-text-main">
			<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
			Explain
		</button>
		<div class="w-px h-[18px] bg-border mx-1"></div>
		<button onclick={formatSQL} class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent transition-all duration-150 whitespace-nowrap hover:bg-border hover:text-text-main">
			<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><polyline points="6 9 12 15 18 9"/></svg>
			Format
		</button>
		<button onclick={() => window.location.href = '/app'} class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-transparent bg-transparent transition-all duration-150 whitespace-nowrap hover:bg-border hover:text-text-main">
			<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
			Save
		</button>
		<div class="flex-1"></div>
	</div>

	<div bind:this={editorEl} class="flex-1 overflow-auto min-h-0"></div>

	<div class="flex items-center gap-4 px-3.5 h-6 bg-bg-toolbar border-t border-b border-border text-[11px] text-text-muted shrink-0">
		<span class="flex items-center gap-1"><span class="text-primary font-semibold">Trino SQL</span></span>
		<span class="flex items-center gap-1">Ln 1, Col 1</span>
		<span class="flex items-center gap-1">{statusText}</span>
		{#if errorMsg}
			<span class="flex items-center gap-1 text-red-600">{errorMsg}</span>
		{/if}
	</div>

	{#if results.length > 0 || executing}
		<div class="h-[42%] flex flex-col shrink-0">
			<div class="flex items-stretch bg-bg-toolbar border-b border-border px-3 shrink-0">
				<div class="py-[7px] px-3 text-xs font-semibold cursor-pointer -mb-px flex items-center gap-1.5 bg-none border-t-0 border-l-0 border-r-0 border-b-2
					text-primary border-b-primary">
					Result <span class="bg-blue-100 text-primary text-[10px] font-bold py-px px-[5px] rounded-full">{rowCount}</span>
				</div>
				<div class="ml-auto flex items-center">
					<button class="flex items-center gap-1 py-1 px-2 text-[11px] rounded cursor-pointer text-text-muted bg-none border-none hover:bg-border hover:text-text-main">
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
						Export
					</button>
				</div>
			</div>
			<div class="py-1.5 px-3.5 flex items-center gap-5 border-b border-border text-xs bg-bg-panel shrink-0">
				<span class="flex items-center gap-[5px] text-text-muted"><span class="w-[7px] h-[7px] rounded-full bg-success"></span> Finished</span>
				<span class="flex items-center gap-[5px] text-text-muted">{rowCount} rows</span>
				<span class="flex items-center gap-[5px] text-text-muted">{durationMs}ms</span>
			</div>
			<div class="flex-1 overflow-auto">
				<table class="w-full border-separate border-spacing-0 text-left text-xs">
					<thead>
						<tr>
							<th class="bg-bg-toolbar py-[7px] px-3.5 font-semibold text-text-muted border-b border-border sticky top-0 z-[1] text-[11px] tracking-[0.3px] uppercase text-right w-9">#</th>
							{#each columns as col}
								<th class="bg-bg-toolbar py-[7px] px-3.5 font-semibold text-text-muted border-b border-border sticky top-0 z-[1] text-[11px] tracking-[0.3px] uppercase">{col}</th>
							{/each}
						</tr>
					</thead>
					<tbody>
						{#each pagedRows as row, i}
							<tr class="hover:[&>td]:bg-bg-toolbar">
								<td class="py-[7px] px-3.5 border-b border-border font-mono text-xs text-text-muted w-9 text-right opacity-60">{(page - 1) * perPage + i + 1}</td>
								{#each row as cell}
									<td class="py-[7px] px-3.5 border-b border-border font-mono text-xs">
										{#if cell === null}
											<span class="text-slate-300 italic">NULL</span>
										{:else}
											{cell}
										{/if}
									</td>
								{/each}
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<div class="flex items-center gap-1.5 py-1.5 px-3 border-t border-border shrink-0 bg-bg-panel text-xs text-text-muted">
				<button
					disabled={page <= 1}
					onclick={() => page--}
					class="h-[26px] px-2.5 rounded-[5px] border border-border bg-transparent text-[11px] font-semibold cursor-pointer transition-all duration-150 text-text-muted
						hover:enabled:border-border-hover hover:enabled:text-text-main disabled:opacity-40 disabled:cursor-not-allowed">
					← Prev
				</button>
				<span class="text-[11px] text-text-muted">Page <b class="text-text-main">{page}</b> of <b class="text-text-main">{totalPages}</b></span>
				<button
					disabled={page >= totalPages}
					onclick={() => page++}
					class="h-[26px] px-2.5 rounded-[5px] border border-border bg-transparent text-[11px] font-semibold cursor-pointer transition-all duration-150 text-text-muted
						hover:enabled:border-border-hover hover:enabled:text-text-main disabled:opacity-40 disabled:cursor-not-allowed">
					Next →
				</button>
				<span class="flex-1"></span>
				<span class="flex items-center gap-[5px] text-[11px] text-text-muted">
					Rows per page:
					<select
						bind:value={perPage}
						onchange={() => page = 1}
						class="border border-border rounded py-0.5 px-1.5 text-[11px] text-text-main bg-bg-panel outline-none cursor-pointer">
						<option value={25}>25</option>
						<option value={50}>50</option>
						<option value={100}>100</option>
					</select>
				</span>
			</div>
		</div>
	{/if}
</div>