<script lang="ts">
	let { collapsed = $bindable(false) } = $props();
	let inspectorTab = $state<'columns' | 'details'>('columns');
	let colSearch = $state('');
	let colFilter = $state<'all' | 'pk' | 'partition' | 'nullable'>('all');
</script>

<aside class="panel sidebar-right" class:collapsed>
	{#if collapsed}
		<div class="inspector-collapsed">
			<button class="inspector-reopen-btn" onclick={() => collapsed = false} title="Expand">
				<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
			</button>
			<span class="inspector-reopen-label">Inspector</span>
		</div>
	{:else}
		<div class="inspector-full">
			<div class="inspector-header">
				<div class="inspector-topbar">
					<span class="inspector-source-badge">
						<span class="dot"></span>ICEBERG
					</span>
					<span class="inspector-breadcrumb">default / <b>users</b></span>
					<button class="inspector-collapse-btn" onclick={() => collapsed = true} title="Collapse">
						<svg viewBox="0 0 24 24"><path d="M9 18l15-6-6-6"/></svg>
					</button>
				</div>

				<div class="inspector-header-main">
					<div class="inspector-entity-name-row">
						<svg class="inspector-table-icon" viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="9" y1="21" x2="9" y2="9"/></svg>
						<span class="inspector-entity-name">users</span>
					</div>
					<div class="table-comment-box">User accounts with profile, preferences and activity tracking.</div>
					<div class="inspector-stats-strip">
						<div class="stat-cell"><span class="stat-cell-val">24</span><span class="stat-cell-label">Columns</span></div>
						<div class="stat-cell"><span class="stat-cell-val">1.2M</span><span class="stat-cell-label">Rows</span></div>
						<div class="stat-cell"><span class="stat-cell-val">48 MB</span><span class="stat-cell-label">Size</span></div>
					</div>
				</div>
			</div>

			<div class="inspector-tabs">
				<button class="itab" class:active={inspectorTab === 'columns'} onclick={() => inspectorTab = 'columns'} style="flex:1.2">Columns</button>
				<button class="itab" class:active={inspectorTab === 'details'} onclick={() => inspectorTab = 'details'}>Details</button>
			</div>

			<div class="col-search-bar">
				<div class="col-search-input">
					<svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
					<input type="text" placeholder="Search columns..." bind:value={colSearch}>
				</div>
			</div>

			<div class="col-filters">
				<button class="filter-chip" class:active={colFilter === 'all'} onclick={() => colFilter = 'all'}>All</button>
				<button class="filter-chip" class:active={colFilter === 'pk'} onclick={() => colFilter = 'pk'}>PK</button>
				<button class="filter-chip" class:active={colFilter === 'partition'} onclick={() => colFilter = 'partition'}>Partition</button>
				<button class="filter-chip" class:active={colFilter === 'nullable'} onclick={() => colFilter = 'nullable'}>Nullable</button>
			</div>

			<div class="inspector-content">
				<div class="col-row">
					<div class="col-body">
						<div class="col-name-row">
							<span class="badge-pk"><svg viewBox="0 0 24 24"><polygon points="12 2 22 8.5 22 15.5 12 22 2 15.5 2 8.5 12 2"/><line x1="12" y1="22" x2="12" y2="15.5"/><polyline points="22 8.5 12 15.5 2 8.5"/></svg></span>
							<span class="col-name">id</span>
							<span class="type-chip type-numeric">UUID</span>
						</div>
					</div>
				</div>
				<div class="col-row">
					<div class="col-body">
						<div class="col-name-row">
							<span class="col-name">email</span>
							<span class="type-chip type-string">varchar</span>
						</div>
					</div>
				</div>
				<div class="col-row highlighted">
					<div class="col-body">
						<div class="col-name-row">
							<span class="badge-partition">P</span>
							<span class="col-name">created_at</span>
							<span class="type-chip type-temporal">timestamp</span>
						</div>
					</div>
				</div>
				<div class="col-row">
					<div class="col-body">
						<div class="col-name-row">
							<span class="col-name">name</span>
							<span class="type-chip type-string">varchar</span>
						</div>
						<div class="col-comment">User display name</div>
					</div>
				</div>
				<div class="col-row">
					<div class="col-body">
						<div class="col-name-row">
							<span class="col-name">is_active</span>
							<span class="type-chip type-bool">boolean</span>
						</div>
					</div>
				</div>
			</div>
		</div>
	{/if}
</aside>

<style>
	.sidebar-right { width: 292px; flex-shrink: 0; display: flex; flex-direction: column; overflow: hidden; transition: width 0.2s; }
	.sidebar-right.collapsed { width: 36px; }

	.inspector-collapsed {
		display: flex; flex-direction: column;
		align-items: center; padding: 10px 0; gap: 10px; flex: 1;
	}
	.inspector-reopen-btn {
		width: 22px; height: 22px; border-radius: 5px; cursor: pointer;
		display: flex; align-items: center; justify-content: center;
		color: var(--text-muted); transition: 0.15s;
		background: none; border: none;
	}
	.inspector-reopen-btn:hover { background: #eff6ff; color: var(--primary); }
	:global(.dark) .inspector-reopen-btn:hover { background: #152236; }
	.inspector-reopen-label {
		writing-mode: vertical-rl; font-size: 10px; font-weight: 600;
		letter-spacing: 0.5px; text-transform: uppercase;
		color: var(--text-muted); transform: rotate(180deg); margin-top: 4px;
	}
	.inspector-full { display: flex; flex-direction: column; flex: 1; overflow: hidden; }
	.inspector-header { flex-shrink: 0; border-bottom: 1px solid var(--border-light); }
	.inspector-topbar {
		display: flex; align-items: center; gap: 8px;
		padding: 7px 10px; border-bottom: 1px solid var(--border-light);
		background: var(--bg-toolbar);
	}
	.inspector-source-badge {
		display: flex; align-items: center; gap: 4px;
		background: #fef2f2; border: 1px solid #fecaca;
		color: #dc2626; font-size: 9px; font-weight: 700;
		letter-spacing: 0.5px; padding: 2px 6px;
		border-radius: 3px; font-family: 'JetBrains Mono', monospace;
		text-transform: uppercase; flex-shrink: 0;
	}
	:global(.dark) .inspector-source-badge { background: #1f0f0f; border-color: #4d1111; color: #f87171; }
	.inspector-source-badge .dot { width: 5px; height: 5px; border-radius: 50%; background: #ef4444; }
	.inspector-breadcrumb {
		flex: 1; font-family: 'JetBrains Mono', monospace;
		font-size: 11px; color: var(--text-muted);
		overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
	}
	.inspector-breadcrumb b { color: var(--text-main); font-weight: 600; }
	.inspector-collapse-btn {
		width: 20px; height: 20px; border-radius: 4px; cursor: pointer;
		display: flex; align-items: center; justify-content: center;
		color: var(--text-muted); transition: 0.15s; flex-shrink: 0;
		background: none; border: none;
	}
	.inspector-collapse-btn:hover { background: #e2e8f0; color: var(--text-main); }
	.inspector-collapse-btn svg { width: 12px; height: 12px; }

	.inspector-header-main { padding: 12px 12px 10px; background: var(--bg-panel); }
	.inspector-entity-name-row { display: flex; align-items: center; gap: 7px; margin-bottom: 8px; }
	.inspector-table-icon { width: 16px; height: 16px; flex-shrink: 0; color: var(--primary); }
	.inspector-table-icon svg { width: 16px; height: 16px; }
	.inspector-entity-name { font-size: 15px; font-weight: 700; font-family: 'JetBrains Mono', monospace; }
	.table-comment-box {
		font-size: 12px; color: var(--text-muted); line-height: 1.55;
		background: #f8fafc; border: 1px solid var(--border-light);
		border-radius: 4px; padding: 7px 9px; margin-bottom: 10px;
		max-height: 80px; overflow-y: auto; word-break: break-word;
	}
	:global(.dark) .table-comment-box { background: #1c2128; }
	.inspector-stats-strip {
		display: flex; gap: 0; border: 1px solid var(--border-light);
		border-radius: 5px; overflow: hidden; margin-top: 2px;
	}
	.stat-cell {
		flex: 1; padding: 5px 0; text-align: center;
		border-right: 1px solid var(--border-light);
	}
	.stat-cell:last-child { border-right: none; }
	.stat-cell-val {
		display: block; font-size: 12px; font-weight: 700;
		font-family: 'JetBrains Mono', monospace; line-height: 1.2;
	}
	.stat-cell-label {
		display: block; font-size: 9px; font-weight: 600;
		text-transform: uppercase; letter-spacing: 0.4px;
		color: var(--text-muted); margin-top: 1px;
	}

	.inspector-tabs {
		display: flex; border-bottom: 1px solid var(--border-light);
		background: var(--bg-toolbar); flex-shrink: 0;
	}
	.itab {
		flex: 1; text-align: center; padding: 7px 4px;
		font-size: 11px; font-weight: 600; cursor: pointer;
		color: var(--text-muted); border-bottom: 2px solid transparent;
		transition: 0.15s; background: none; border-top: none;
		border-left: none; border-right: none;
	}
	.itab:hover { color: var(--text-main); }
	.itab.active { color: var(--primary); border-bottom-color: var(--primary); background: var(--bg-panel); }

	.col-search-bar {
		display: flex; align-items: center; gap: 0;
		padding: 7px 10px; border-bottom: 1px solid var(--border-light);
		background: var(--bg-panel); flex-shrink: 0;
	}
	.col-search-input {
		flex: 1; display: flex; align-items: center; gap: 5px;
		background: #f1f5f9; border: 1px solid var(--border-light);
		border-radius: 4px; padding: 4px 7px; transition: 0.15s;
	}
	:global(.dark) .col-search-input { background: #1c2128; }
	.col-search-input:focus-within { background: var(--bg-panel); border-color: var(--primary); }
	.col-search-input svg { width: 12px; height: 12px; color: var(--text-muted); flex-shrink: 0; }
	.col-search-input input {
		border: none; background: transparent; outline: none;
		font-size: 12px; color: var(--text-main); width: 100%;
	}

	.col-filters {
		display: flex; gap: 4px; padding: 8px 10px;
		background: var(--bg-panel); flex-shrink: 0;
	}
	.filter-chip {
		font-size: 10px; font-weight: 600; padding: 2px 7px;
		border-radius: 3px; cursor: pointer; border: 1px solid var(--border-light);
		background: transparent; color: var(--text-muted); transition: 0.15s;
	}
	.filter-chip:hover { border-color: var(--primary); color: var(--primary); }
	.filter-chip.active { background: #eff6ff; border-color: #bfdbfe; color: var(--primary); }
	:global(.dark) .filter-chip.active { background: #152236; }

	.inspector-content { flex: 1; overflow-y: auto; padding: 0; }
	.col-row {
		display: flex; align-items: flex-start; padding: 8px 12px;
		border-bottom: 1px solid var(--border-light); cursor: default; gap: 8px;
	}
	.col-row:hover { background: #f8fafc; }
	:global(.dark) .col-row:hover { background: #1c2128; }
	.col-row.highlighted { border-left: 2px solid #f59e0b; padding-left: 10px; }
	:global(.dark) .col-row.highlighted { background: #1f1a00; border-left-color: #b45309; }
	.col-body { flex: 1; min-width: 0; }
	.col-name-row { display: flex; align-items: center; gap: 5px; margin-bottom: 3px; }
	.col-name {
		font-size: 12px; font-weight: 600; font-family: 'JetBrains Mono', monospace;
		flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
	}
	.badge-pk { color: #f59e0b; flex-shrink: 0; }
	.badge-pk svg { width: 11px; height: 11px; }
	.badge-partition {
		font-size: 9px; font-weight: 700; font-family: 'JetBrains Mono', monospace;
		padding: 1px 4px; border-radius: 2px; background: #fef3c7;
		border: 1px solid #fde68a; color: #b45309; flex-shrink: 0;
	}
	.type-chip {
		display: inline-flex; align-items: center;
		font-size: 10px; font-weight: 600; font-family: 'JetBrains Mono', monospace;
		padding: 1px 5px; border-radius: 3px; border: 1px solid transparent;
		white-space: nowrap; flex-shrink: 0;
	}
	.type-string  { color: #0f766e; background: #f0fdf9; border-color: #99f6e4; }
	.type-numeric { color: #7c3aed; background: #f5f3ff; border-color: #ddd6fe; }
	.type-temporal{ color: #b45309; background: #fffbeb; border-color: #fde68a; }
	.type-bool    { color: #dc2626; background: #fef2f2; border-color: #fecaca; }
	.col-comment { font-size: 11px; color: var(--text-muted); line-height: 1.45; }
</style>
