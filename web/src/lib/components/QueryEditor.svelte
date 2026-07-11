<script lang="ts">
	import { onMount } from 'svelte';
	import { basicSetup } from 'codemirror';
	import { EditorView, keymap } from '@codemirror/view';
	import { EditorState, Compartment } from '@codemirror/state';
	import { indentWithTab } from '@codemirror/commands';
	import { sql, PostgreSQL } from '@codemirror/lang-sql';
	import { oneDark } from '@codemirror/theme-one-dark';
	import { autocompletion } from '@codemirror/autocomplete';
	import { tableFromIPC } from 'apache-arrow';

	let { dark = false }: { dark?: boolean } = $props();

	let editorView: EditorView;
	let editorEl: HTMLDivElement;
	const themeCompartment = new Compartment();

	let executing = $state(false);
	let sqlCode = $state('SELECT * FROM users LIMIT 10;');
	let executionId = $state('');
	let results: any[] = $state([]);
	let columns: string[] = $state([]);
	let rowCount = $state(0);
	let durationMs = $state(0);
	let errorMsg = $state('');
	let statusText = $state('Ready');

	let page = $state(1);
	let perPage = $state(50);

	// Same-origin: the Go server serves this SPA and the API under /api/v1.
	const apiBase = '/api/v1';
	const workspaceId = localStorage.getItem('workspace_id') || '';
	const token = localStorage.getItem('token') || '';

	onMount(() => {
		const extensions = [
			basicSetup,
			keymap.of([indentWithTab]),
			sql({ dialect: PostgreSQL }),
			autocompletion(),
			themeCompartment.of(dark ? oneDark : []),
			EditorView.updateListener.of(update => {
				if (update.docChanged) sqlCode = update.state.doc.toString();
			}),
		];

		const state = EditorState.create({ doc: sqlCode, extensions });
		editorView = new EditorView({ state, parent: editorEl });
	});

	// React to theme changes without recreating the editor.
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

		ws.onmessage = (e) => {
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
					fetchResults(execId);
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

	// Results are an Arrow IPC stream proxied by the API (same origin, auth).
	async function fetchResults(execId: string) {
		try {
			const res = await fetch(`${apiBase}/workspaces/${workspaceId}/executions/${execId}/results`, {
				headers: { Authorization: `Bearer ${token}` }
			});
			if (!res.ok) throw new Error(`results ${res.status}`);
			const buf = await res.arrayBuffer();
			const table = tableFromIPC(new Uint8Array(buf));
			columns = table.schema.fields.map((f) => f.name);
			const decoded: any[][] = [];
			for (const row of table.toArray()) {
				decoded.push(columns.map((c) => (row as any)[c]));
			}
			results = decoded;
			rowCount = decoded.length;
		} catch (e: any) {
			errorMsg = 'Failed to fetch results';
			statusText = 'Error';
		}
		executing = false;
	}

	function formatSQL() {
		// simple uppercase for keywords
		const keywords = ['SELECT', 'FROM', 'WHERE', 'AND', 'OR', 'INSERT', 'UPDATE', 'DELETE',
			'CREATE', 'TABLE', 'ALTER', 'DROP', 'JOIN', 'LEFT', 'RIGHT', 'INNER', 'ON',
			'GROUP', 'BY', 'ORDER', 'LIMIT', 'OFFSET', 'AS', 'IN', 'NOT', 'NULL', 'IS'];
		let formatted = sqlCode;
		keywords.forEach(kw => {
			const re = new RegExp(`\\b${kw}\\b`, 'gi');
			formatted = formatted.replace(re, kw);
		});
		sqlCode = formatted;
		editorView.dispatch({
			changes: { from: 0, to: editorView.state.doc.length, insert: formatted }
		});
	}

	function explainQuery() {
		runQuery();
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

<div class="editor-panel">
	<div class="editor-tabs">
		<div class="tab active">
			<span class="tab-name">Untitled Query</span>
			<span class="tab-close">×</span>
		</div>
		<button class="tab-add">+</button>
	</div>

	<div class="editor-toolbar">
		<button class="btn-run" onclick={runQuery} disabled={executing}>
			<svg viewBox="0 0 24 24"><polygon points="5 3 19 12 5 21 5 3"/></svg>
			{executing ? 'Running...' : 'Run'}
			<span class="shortcut">⌘↵</span>
		</button>
		<button class="toolbar-btn" onclick={explainQuery}>
			<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
			Explain
		</button>
		<div class="toolbar-divider"></div>
		<button class="toolbar-btn" onclick={formatSQL}>
			<svg viewBox="0 0 24 24"><polyline points="6 9 12 15 18 9"/></svg>
			Format
		</button>
		<button class="toolbar-btn" onclick={() => window.location.href = '/app'}>
			<svg viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
			Save
		</button>
		<div class="toolbar-spacer"></div>
	</div>

	<div class="editor-body" bind:this={editorEl}></div>

	<div class="status-bar">
		<span class="status-item"><span class="status-dialect">Trino SQL</span></span>
		<span class="status-item">Ln 1, Col 1</span>
		<span class="status-item">{statusText}</span>
		{#if errorMsg}
			<span class="status-item" style="color:#dc2626">{errorMsg}</span>
		{/if}
	</div>

	{#if results.length > 0 || executing}
		<div class="results-container">
			<div class="results-tabs">
				<div class="rtab active">
					Result <span class="rtab-count">{rowCount}</span>
				</div>
				<div class="results-actions">
					<button class="results-action-btn">
						<svg viewBox="0 0 24 24"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
						Export
					</button>
				</div>
			</div>
			<div class="results-meta">
				<span class="metric"><span class="status-dot"></span> Finished</span>
				<span class="metric">{rowCount} rows</span>
				<span class="metric">{durationMs}ms</span>
			</div>
			<div class="table-wrapper">
				<table>
					<thead>
						<tr>
							<th class="row-num">#</th>
							{#each columns as col}
								<th>{col}</th>
							{/each}
						</tr>
					</thead>
					<tbody>
						{#each pagedRows as row, i}
							<tr>
							<td class="row-num">{(page - 1) * perPage + i + 1}</td>
							{#each row as cell}
								<td>
									{#if cell === null}
										<span class="null-val">NULL</span>
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
			<div class="pagination">
				<button class="pg-btn" disabled={page <= 1} onclick={() => page--}>← Prev</button>
				<span class="pg-info">Page <b>{page}</b> of <b>{totalPages}</b></span>
				<button class="pg-btn" disabled={page >= totalPages} onclick={() => page++}>Next →</button>
				<span class="pg-spacer"></span>
				<span class="pg-size">
					Rows per page:
					<select bind:value={perPage} onchange={() => page = 1}>
						<option value={25}>25</option>
						<option value={50}>50</option>
						<option value={100}>100</option>
					</select>
				</span>
			</div>
		</div>
	{/if}
</div>

<style>
	.editor-panel { flex: 1; display: flex; flex-direction: column; overflow: hidden; min-width: 0; }

	.editor-tabs {
		display: flex; align-items: stretch; background: var(--bg-toolbar);
		padding: 6px 8px 0; gap: 2px;
		border-bottom: 1px solid var(--border-light); flex-shrink: 0;
	}
	.tab {
		padding: 6px 14px; display: flex; align-items: center; gap: 6px;
		font-size: 12px; border-radius: 6px 6px 0 0; cursor: pointer;
		color: var(--text-muted); border: 1px solid transparent;
		border-bottom: none; margin-bottom: -1px;
	}
	.tab.active { background: var(--bg-panel); color: var(--primary); font-weight: 600; border-color: var(--border-light); }
	.tab-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; min-width: 0; }
	.tab-close { opacity: 0; font-size: 14px; color: var(--text-muted); padding: 1px 3px; border-radius: 3px; }
	.tab:hover .tab-close { opacity: 1; }
	.tab-close:hover { background: var(--border-light); color: var(--text-main); }
	.tab-add {
		display: flex; align-items: center; justify-content: center;
		width: 32px; font-size: 18px; color: var(--text-muted);
		cursor: pointer; flex-shrink: 0; transition: .15s;
		background: none; border: none;
	}
	.tab-add:hover { color: var(--primary); }

	.editor-toolbar {
		display: flex; align-items: center; padding: 6px 12px; gap: 6px;
		border-bottom: 1px solid var(--border-light);
		background: var(--bg-toolbar); flex-shrink: 0;
	}
	.toolbar-divider { width: 1px; height: 18px; background: var(--border-light); margin: 0 4px; }
	.toolbar-btn {
		display: flex; align-items: center; gap: 5px; padding: 4px 8px;
		font-size: 12px; font-weight: 500; border-radius: 5px; cursor: pointer;
		color: var(--text-muted); border: 1px solid transparent;
		background: transparent; transition: .15s; white-space: nowrap;
	}
	.toolbar-btn:hover { background: var(--border-light); color: var(--text-main); border-color: var(--border-light); }
	.toolbar-spacer { flex: 1; }

	.btn-run {
		background: var(--success); color: white; border: none;
		padding: 5px 14px; font-size: 12px; font-weight: 700;
		border-radius: 5px; cursor: pointer; display: flex;
		align-items: center; gap: 6px; transition: .15s;
	}
	.btn-run:hover:not(:disabled) { background: var(--success-hover); }
	.btn-run:disabled { opacity: 0.6; cursor: not-allowed; }
	.btn-run svg { width: 10px; height: 10px; fill: white; stroke: none; }
	.shortcut { font-size: 10px; opacity: 0.7; font-weight: 400; }

	.editor-body { flex: 1; overflow: auto; min-height: 0; }
	:global(.cm-editor) { height: 100%; font-size: 13px; }
	:global(.cm-editor.cm-focused) { outline: none; }
	:global(.cm-scroller) { font-family: var(--font-mono); }

	.status-bar {
		display: flex; align-items: center; gap: 16px;
		padding: 0 14px; height: 24px; background: var(--bg-toolbar);
		border-top: 1px solid var(--border-light);
		border-bottom: 1px solid var(--border-light);
		font-size: 11px; color: var(--text-muted); flex-shrink: 0;
	}
	.status-item { display: flex; align-items: center; gap: 4px; }
	.status-dialect { color: var(--primary); font-weight: 600; }

	.results-container { height: 42%; display: flex; flex-direction: column; flex-shrink: 0; }
	.results-tabs {
		display: flex; align-items: stretch; background: var(--bg-toolbar);
		border-bottom: 1px solid var(--border-light); padding: 0 12px; flex-shrink: 0;
	}
	.rtab {
		padding: 7px 12px; font-size: 12px; font-weight: 500; cursor: pointer;
		color: var(--text-muted); border-bottom: 2px solid transparent;
		margin-bottom: -1px; display: flex; align-items: center; gap: 6px;
		background: none; border-top: none; border-left: none; border-right: none;
	}
	.rtab.active { color: var(--primary); border-bottom-color: var(--primary); font-weight: 600; }
	.rtab-count { background: #e2e8f0; color: var(--text-muted); font-size: 10px; font-weight: 700; padding: 1px 5px; border-radius: 10px; }
	.rtab.active .rtab-count { background: #dbeafe; color: var(--primary); }
	.results-actions { margin-left: auto; display: flex; align-items: center; }
	.results-action-btn {
		display: flex; align-items: center; gap: 4px; padding: 4px 8px;
		font-size: 11px; border-radius: 4px; cursor: pointer;
		color: var(--text-muted); background: none; border: none;
	}
	.results-action-btn:hover { background: var(--border-light); color: var(--text-main); }

	.results-meta {
		padding: 6px 14px; display: flex; align-items: center; gap: 20px;
		border-bottom: 1px solid var(--border-light); font-size: 12px;
		background: var(--bg-panel); flex-shrink: 0;
	}
	.metric { display: flex; align-items: center; gap: 5px; color: var(--text-muted); }
	.status-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--success); }

	.table-wrapper { flex: 1; overflow: auto; }
	table { width: 100%; border-collapse: separate; border-spacing: 0; text-align: left; font-size: 12px; }
	th {
		background: var(--bg-toolbar); padding: 7px 14px; font-weight: 600;
		color: var(--text-muted); border-bottom: 1px solid var(--border-light);
		position: sticky; top: 0; z-index: 1; font-size: 11px;
		letter-spacing: 0.3px; text-transform: uppercase;
	}
	td { padding: 7px 14px; border-bottom: 1px solid var(--border-light); font-family: var(--font-mono); font-size: 12px; }
	tr:hover td { background: var(--bg-toolbar); }
	.row-num { color: var(--text-muted); width: 36px; text-align: right; font-family: var(--font-mono); font-size: 11px; opacity: 0.6; }
	.null-val { color: #cbd5e1; font-style: italic; }

	.pagination {
		display: flex; align-items: center; gap: 6px; padding: 6px 12px;
		border-top: 1px solid var(--border-light); flex-shrink: 0;
		background: var(--bg-panel); font-size: 12px; color: var(--text-muted);
	}
	.pg-btn {
		height: 26px; padding: 0 10px; border-radius: 5px;
		border: 1px solid var(--border-light); background: transparent;
		font-size: 11px; font-weight: 600; cursor: pointer; transition: .15s;
		color: var(--text-muted);
	}
	.pg-btn:hover:not(:disabled) { border-color: var(--border-hover); color: var(--text-main); }
	.pg-btn:disabled { opacity: .4; cursor: not-allowed; }
	.pg-info { font-size: 11px; color: var(--text-muted); }
	.pg-info b { color: var(--text-main); }
	.pg-spacer { flex: 1; }
	.pg-size { display: flex; align-items: center; gap: 5px; font-size: 11px; color: var(--text-muted); }
	.pg-size select {
		border: 1px solid var(--border-light); border-radius: 4px;
		padding: 2px 6px; font-size: 11px; color: var(--text-main);
		background: var(--bg-panel); outline: none; cursor: pointer;
	}
</style>
