<script lang="ts">
	let { collapsed = $bindable(false), activeTab = $bindable<'database' | 'queries'>('database') } = $props();

	let search = $state('');
</script>

<aside class="panel sidebar-left" class:collapsed>
	{#if collapsed}
		<div class="sidebar-collapsed-strip">
			<button class="sidebar-reopen-btn" onclick={() => collapsed = false} title="Expand">
				<svg viewBox="0 0 24 24"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/></svg>
			</button>
			<span class="sidebar-reopen-label">Database</span>
		</div>
	{:else}
		<div class="sidebar-full">
			<div class="sidebar-tabs">
				<button class="sidebar-tab" class:active={activeTab === 'database'} onclick={() => activeTab = 'database'}>
					<svg viewBox="0 0 24 24"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/></svg>
					Database
				</button>
				<button class="sidebar-tab" class:active={activeTab === 'queries'} onclick={() => activeTab = 'queries'}>
					<svg viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
					Queries
				</button>
				<button class="sidebar-collapse-btn" onclick={() => collapsed = true} title="Collapse">
					<svg viewBox="0 0 24 24"><path d="M15 18l-6-6 6-6"/></svg>
				</button>
			</div>

			<div class="sidebar-search">
				<div class="search-input">
					<svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
					<input type="text" placeholder="Search tables, schemas..." bind:value={search}>
				</div>
			</div>

			{#if activeTab === 'database'}
				<div class="tree-view">
					<div class="tree-node">
						<svg class="chevron open" viewBox="0 0 24 24"><path d="M9 18l6-6-6-6"/></svg>
						<svg class="db-icon" viewBox="0 0 24 24"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/></svg>
						<span class="node-label"><b>Polaris Catalog</b></span>
					</div>
					<div class="tree-node indent-1">
						<svg class="chevron open" viewBox="0 0 24 24"><path d="M9 18l6-6-6-6"/></svg>
						<svg class="schema-icon" viewBox="0 0 24 24"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
						<span class="node-label">default</span>
					</div>
					<div class="tree-node indent-2 active">
						<svg class="chevron" viewBox="0 0 24 24"><path d="M9 18l6-6-6-6"/></svg>
						<svg class="table-icon" viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="9" y1="21" x2="9" y2="9"/></svg>
						<span class="node-label">users</span>
					</div>
					<div class="tree-node indent-2">
						<svg class="chevron" viewBox="0 0 24 24"><path d="M9 18l6-6-6-6"/></svg>
						<svg class="table-icon muted" viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="9" y1="21" x2="9" y2="9"/></svg>
						<span class="node-label">projects</span>
					</div>
				</div>
			{:else}
				<div class="sq-list">
					<div class="sq-group-label">Mine</div>
					<div class="sq-file">
						<svg class="sq-file-icon" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
						<div class="sq-file-info">
							<div class="sq-file-name">Example Query</div>
							<div class="sq-file-meta">Edited just now</div>
						</div>
						<span class="sq-file-badge mine">SQL</span>
					</div>
					<div class="sq-empty">No saved queries yet.</div>
				</div>
			{/if}
		</div>
	{/if}
</aside>

<style>
	.sidebar-left { width: 260px; flex-shrink: 0; transition: width 0.2s; display: flex; flex-direction: column; overflow: hidden; }
	.sidebar-left.collapsed { width: 36px; }
	.sidebar-collapsed-strip {
		display: flex; flex-direction: column;
		align-items: center; padding: 10px 0; gap: 10px; flex: 1;
	}
	.sidebar-reopen-btn {
		width: 22px; height: 22px; border-radius: 5px; cursor: pointer;
		display: flex; align-items: center; justify-content: center;
		color: var(--text-muted); transition: 0.15s;
		background: none; border: none;
	}
	.sidebar-reopen-btn:hover { background: #eff6ff; color: var(--primary); }
	.dark .sidebar-reopen-btn:hover { background: #152236; }
	.sidebar-reopen-label {
		writing-mode: vertical-rl; font-size: 10px; font-weight: 600;
		letter-spacing: 0.5px; text-transform: uppercase;
		color: var(--text-muted); transform: rotate(180deg); margin-top: 4px;
	}
	.sidebar-full { display: flex; flex-direction: column; flex: 1; overflow: hidden; }

	.sidebar-tabs {
		display: flex; background: #f8fafc;
		border-bottom: 1px solid var(--border-light); flex-shrink: 0;
	}
	.dark .sidebar-tabs { background: #1c2128; }
	.sidebar-tab {
		flex: 1; text-align: center; padding: 9px 6px;
		font-size: 11px; font-weight: 600; cursor: pointer;
		color: var(--text-muted); border-bottom: 2px solid transparent;
		transition: 0.15s; display: flex; align-items: center;
		justify-content: center; gap: 5px;
		background: none; border-top: none; border-left: none; border-right: none;
	}
	.sidebar-tab:hover { color: var(--text-main); }
	.sidebar-tab.active { color: var(--primary); border-bottom-color: var(--primary); background: var(--bg-panel); }
	.sidebar-tab svg { width: 12px; height: 12px; }
	.sidebar-collapse-btn {
		display: flex; align-items: center; padding: 0 8px;
		border-left: 1px solid var(--border-light);
		color: var(--text-muted); cursor: pointer;
		background: none; border-top: none; border-bottom: none; border-right: none;
		transition: 0.15s;
	}
	.sidebar-collapse-btn:hover { color: var(--text-main); }

	.sidebar-search { padding: 8px 10px; border-bottom: 1px solid var(--border-light); flex-shrink: 0; }
	.search-input {
		width: 100%; display: flex; align-items: center; gap: 6px;
		background: #f1f5f9; border: 1px solid var(--border-light);
		border-radius: 6px; padding: 5px 8px; transition: 0.15s;
	}
	.dark .search-input { background: #1c2128; }
	.search-input:focus-within { background: var(--bg-panel); border-color: var(--primary); }
	.search-input input {
		border: none; background: transparent; outline: none;
		font-size: 12px; color: var(--text-main); width: 100%;
	}
	.search-input input::placeholder { color: var(--text-muted); }
	.search-input svg { width: 12px; height: 12px; color: var(--text-muted); flex-shrink: 0; }

	.tree-view { overflow-y: auto; flex: 1; padding: 0 6px 8px; font-size: 13px; }
	.sq-list { overflow-y: auto; flex: 1; padding: 6px 6px 8px; }

	.tree-node { display: flex; align-items: center; padding: 5px 6px; border-radius: 5px; cursor: pointer; gap: 4px; }
	.tree-node:hover { background: #f1f5f9; }
	.dark .tree-node:hover { background: #1c2128; }
	.tree-node.active { background: #eff6ff; color: var(--primary); font-weight: 500; }
	.dark .tree-node.active { background: #152236; }
	.node-label { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.chevron { transition: transform 0.15s; color: var(--text-muted); flex-shrink: 0; }
	.chevron.open { transform: rotate(90deg); }
	.db-icon { color: #ef4444; }
	.schema-icon { color: #f59e0b; }
	.table-icon { color: var(--primary); }
	.muted { color: var(--text-muted); }
	.indent-1 { padding-left: 20px; }
	.indent-2 { padding-left: 36px; }
	.sq-group-label {
		font-size: 10px; font-weight: 700; letter-spacing: 0.5px; text-transform: uppercase;
		color: var(--text-muted); padding: 8px 6px 4px;
		display: flex; align-items: center; gap: 6px;
	}
	.sq-group-label::after { content: ''; flex: 1; height: 1px; background: var(--border-light); }
	.sq-file { display: flex; align-items: center; padding: 6px 8px; border-radius: 6px; gap: 8px; cursor: pointer; margin-bottom: 1px; }
	.sq-file:hover { background: #f1f5f9; }
	.dark .sq-file:hover { background: #1c2128; }
	.sq-file-icon { flex-shrink: 0; color: var(--text-muted); }
	.sq-file-icon svg { width: 14px; height: 14px; }
	.sq-file-info { flex: 1; min-width: 0; }
	.sq-file-name { font-size: 12px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.sq-file-meta { font-size: 10px; color: var(--text-muted); margin-top: 1px; }
	.sq-file-badge { font-size: 9px; font-weight: 700; padding: 2px 5px; border-radius: 3px; flex-shrink: 0; }
	.sq-file-badge.mine { background: #fef3c7; color: #d97706; }
	.sq-file-badge.shared { background: #f0fdf4; color: #16a34a; }
	.sq-empty { display: none; padding: 24px 12px; text-align: center; font-size: 12px; color: var(--text-muted); }
</style>
