<script lang="ts">
	import { onMount } from 'svelte';

	const token = localStorage.getItem('token') || '';
	let connections = $state<any[]>([]);
	let loading = $state(true);
	let error = $state('');
	let search = $state('');
	let modalOpen = $state(false);
	let editingId = $state('');
	let deleteTarget = $state<any>(null);
	let testing = $state(false);
	let testResult = $state<'ok' | 'err' | ''>('');
	let testMsg = $state('');
	let form = $state({ name: '', type: 'polaris', url: '', client_id: '', client_secret: '' });

	const engineLabels: Record<string, string> = { polaris: 'Polaris', nessie: 'Nessie', iceberg: 'Iceberg', glue: 'Glue', hive: 'Hive' };
	const engineClasses: Record<string, string> = {
		polaris: 'bg-blue-50 text-blue-700',
		nessie: 'bg-green-50 text-green-700',
		iceberg: 'bg-fuchsia-50 text-purple-700',
		glue: 'bg-orange-50 text-orange-700',
		hive: 'bg-red-50 text-red-700',
	};

	onMount(() => { loadConnections(); });

	async function loadConnections() {
		loading = true; error = '';
		try {
			const res = await fetch('/api/v1/catalog-connections', { headers: { 'Authorization': `Bearer ${token}` } });
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			connections = await res.json();
		} catch (e: any) { error = e.message; }
		loading = false;
	}

	const filtered = $derived(connections.filter(c => !search || c.name.toLowerCase().includes(search.toLowerCase())));

	function openCreate() { editingId = ''; form = { name: '', type: 'polaris', url: '', client_id: '', client_secret: '' }; testResult = ''; modalOpen = true; }
	function openEdit(c: any) { editingId = c.id; form = { name: c.name, type: c.type, url: c.url, client_id: '', client_secret: '' }; testResult = ''; modalOpen = true; }
	function closeModal() { modalOpen = false; }

	async function saveConnection() {
		const body: any = { name: form.name, type: form.type, url: form.url, default_readonly: false };
		if (form.type === 'polaris') body.polaris_credentials = { client_id: form.client_id, client_secret: form.client_secret };
		const method = editingId ? 'PATCH' : 'POST';
		const url = editingId ? `/api/v1/catalog-connections/${editingId}` : '/api/v1/catalog-connections';
		await fetch(url, { method, headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` }, body: JSON.stringify(body) });
		closeModal(); loadConnections();
	}

	async function testConnection() {
		testing = true; testResult = '';
		try {
			const tr = await fetch(`/api/v1/catalog-connections/${editingId}/test`, { method: 'POST', headers: { 'Authorization': `Bearer ${token}` } });
			const r = await tr.json();
			testResult = r.success ? 'ok' : 'err';
			testMsg = r.success ? `Connected — ${r.latency_ms}ms` : (r.error || 'Failed');
		} catch { testResult = 'err'; testMsg = 'Test failed'; }
		testing = false;
	}

	async function deleteConnection() {
		if (!deleteTarget) return;
		await fetch(`/api/v1/catalog-connections/${deleteTarget.id}`, { method: 'DELETE', headers: { 'Authorization': `Bearer ${token}` } });
		deleteTarget = null; loadConnections();
	}

	const minputBase = "w-full h-9 bg-slate-50 border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-all duration-150 focus:bg-white focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)]";
</script>

<div class="flex-1 overflow-y-auto py-7 px-8">
	<div class="flex items-end justify-between mb-5">
		<div>
			<div class="text-xl font-semibold tracking-[-0.3px]">Datasources</div>
			<div class="text-[13px] text-text-muted mt-[3px]">Manage catalog connections for your workspaces.</div>
		</div>
		<button onclick={openCreate} class="flex items-center gap-1.5 bg-primary text-white border-none rounded-[7px] px-4 h-9 text-[13px] font-semibold cursor-pointer transition-colors duration-150 hover:bg-primary-hover">
			<svg viewBox="0 0 24 24" class="w-[13px] h-[13px] stroke-white"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
			New connection
		</button>
	</div>

	<div class="flex items-center gap-2.5 mb-5">
		<div class="flex items-center gap-[7px] bg-bg-panel border border-border rounded-[7px] px-2.5 h-[34px] w-60 transition-all duration-150 focus-within:border-primary focus-within:shadow-[0_0_0_3px_rgba(59,130,246,.08)]">
			<svg viewBox="0 0 24 24" class="w-[13px] h-[13px] text-text-muted"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
			<input type="text" placeholder="Search connections..." bind:value={search} class="border-none bg-transparent outline-none text-xs text-text-main w-full">
		</div>
		<span class="ml-auto text-xs text-text-muted font-mono">{filtered.length} connection{filtered.length !== 1 ? 's' : ''}</span>
	</div>

	<div class="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-3">
		{#if loading}
			<div class="col-span-full text-center py-12 px-6 text-text-muted text-sm">Loading connections...</div>
		{:else if error}
			<div class="col-span-full text-center py-12 px-6 text-danger text-sm">{error}</div>
		{:else if filtered.length === 0}
			<div class="col-span-full text-center py-12 px-6 text-text-muted text-sm">
				No connections. <button onclick={openCreate} class="text-primary cursor-pointer font-semibold bg-none border-none text-sm">Create one</button>
			</div>
		{:else}
			{#each filtered as conn (conn.id)}
				<div class="bg-bg-panel border border-border rounded-[var(--radius)] p-4 transition-colors duration-150 relative hover:border-border-hover hover:shadow-[0_2px_8px_rgba(0,0,0,.06)]">
					<div class="flex items-start gap-3 mb-3 pr-[72px]">
						<div class="w-9 h-9 rounded-lg flex items-center justify-center font-mono text-[11px] font-bold shrink-0 {engineClasses[conn.type] || engineClasses.polaris}">
							{conn.type.slice(0,2).toUpperCase()}
						</div>
						<div class="flex-1 min-w-0">
							<div class="text-[13px] font-semibold overflow-hidden text-ellipsis whitespace-nowrap mb-0.5">{conn.name}</div>
							<div class="text-[11px] text-text-muted font-mono overflow-hidden text-ellipsis whitespace-nowrap">{conn.url} · {engineLabels[conn.type] || conn.type}</div>
						</div>
					</div>
					<div class="absolute top-3.5 right-3.5 flex items-center gap-1 text-[10px] font-semibold font-mono py-[3px] px-2 rounded-full bg-green-50 text-green-600">
						<div class="w-[5px] h-[5px] rounded-full bg-green-500"></div>Active
					</div>
					<div class="flex gap-1.5">
						<button onclick={() => openEdit(conn)}
							class="flex-1 h-[30px] rounded-[5px] border border-border bg-transparent text-[11px] font-semibold text-text-muted cursor-pointer transition-all duration-150 flex items-center justify-center gap-1
								hover:border-border-hover hover:text-text-main hover:bg-slate-50 dark:hover:bg-[#1c2128]">
							<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg> Edit
						</button>
						<button onclick={() => deleteTarget = conn}
							class="flex-1 h-[30px] rounded-[5px] border border-border bg-transparent text-[11px] font-semibold text-text-muted cursor-pointer transition-all duration-150 flex items-center justify-center gap-1
								hover:border-red-200 hover:text-danger hover:bg-red-50">
							<svg viewBox="0 0 24 24" class="w-3 h-3"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>
						</button>
					</div>
				</div>
			{/each}
		{/if}
	</div>
</div>

{#if modalOpen}
	<div onclick={(e) => e.target === e.currentTarget && closeModal()}
		class="flex fixed inset-0 bg-slate-900/50 backdrop-blur-[3px] z-[100] items-center justify-center p-6 animate-[fadeIn_.18s_ease]">
		<div class="bg-bg-panel border border-border rounded-xl w-full max-w-[500px] max-h-[90vh] overflow-y-auto shadow-[0_24px_64px_rgba(0,0,0,.2)] animate-[popIn_.22s_cubic-bezier(.34,1.4,.64,1)]">
			<div class="flex items-center py-[18px] px-5 border-b border-border sticky top-0 bg-bg-panel rounded-t-xl z-[1]">
				<div class="text-sm font-semibold flex-1">{editingId ? 'Edit — ' + form.name : 'New connection'}</div>
				<button onclick={closeModal} class="w-[26px] h-[26px] rounded-md border-none bg-transparent cursor-pointer flex items-center justify-center text-text-muted hover:bg-slate-100 hover:text-text-main">
					<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
				</button>
			</div>
			<div class="p-5">
				<div class="text-[10px] font-bold uppercase tracking-[0.8px] text-text-muted my-3 flex items-center gap-2.5
					after:content-[''] after:flex-1 after:h-px after:bg-border">
					Engine
				</div>
				<div class="grid grid-cols-3 gap-2 mb-4">
					{#each Object.entries(engineLabels) as [key, label]}
						<div onclick={() => form.type = key}
							class="flex flex-col items-center gap-[5px] py-2.5 px-1.5 rounded-[7px] border cursor-pointer transition-all duration-150
								{form.type === key ? 'border-primary bg-blue-50 dark:bg-[#152236]' : 'border-border hover:border-border-hover'}">
							<div class="w-7 h-7 rounded-md flex items-center justify-center font-mono text-[10px] font-bold {engineClasses[key]}">{key.slice(0,2).toUpperCase()}</div>
							<span class="text-[10px] font-semibold {form.type === key ? 'text-primary' : 'text-text-muted'}">{label}</span>
						</div>
					{/each}
				</div>
				<div class="mb-3.5">
					<label class="block text-[11px] font-semibold mb-[5px]">Connection name</label>
					<input class={minputBase} type="text" placeholder="e.g. analytics-prod" bind:value={form.name}>
				</div>
				<div class="mb-3.5">
					<label class="block text-[11px] font-semibold mb-[5px]">URL</label>
					<input class="{minputBase} font-mono text-[11px]" type="text" placeholder="http://host:port" bind:value={form.url}>
				</div>
				<div class="text-[10px] font-bold uppercase tracking-[0.8px] text-text-muted my-3 flex items-center gap-2.5
					after:content-[''] after:flex-1 after:h-px after:bg-border">
					Credentials
				</div>
				<div class="grid grid-cols-2 gap-2 mb-3.5">
					<div>
						<label class="block text-[11px] font-semibold mb-[5px]">Client ID</label>
						<input class={minputBase} type="text" bind:value={form.client_id}>
					</div>
					<div>
						<label class="block text-[11px] font-semibold mb-[5px]">Client Secret</label>
						<input class={minputBase} type="password" bind:value={form.client_secret}>
					</div>
				</div>
				{#if testResult}
					<div class="flex items-center gap-[7px] text-xs font-medium py-2 px-3 rounded-md mt-3.5
						{testResult === 'ok' ? 'bg-green-50 text-green-600 border border-green-200' : 'bg-red-50 text-red-600 border border-red-200'}">
						<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]">
							{#if testResult === 'ok'}<polyline points="20 6 9 17 4 12"/>{:else}<circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/>{/if}
						</svg>
						{testMsg}
					</div>
				{/if}
			</div>
			<div class="flex items-center justify-between py-3.5 px-5 border-t border-border">
				<button onclick={testConnection} disabled={testing}
					class="flex items-center gap-1.5 h-[34px] px-3.5 rounded-md border text-xs font-semibold cursor-pointer
						{testing ? 'text-primary border-blue-200 bg-blue-50 pointer-events-none' : 'border-border text-text-muted hover:border-border-hover hover:text-text-main'} bg-transparent">
					<svg viewBox="0 0 24 24" class="w-3 h-3"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
					{testing ? 'Testing...' : 'Test connection'}
				</button>
				<div>
					<button onclick={closeModal} class="h-[34px] px-3.5 rounded-md border border-border bg-transparent text-xs font-medium text-text-muted cursor-pointer mr-1.5 hover:border-border-hover hover:text-text-main">Cancel</button>
					<button onclick={saveConnection} class="flex items-center gap-1.5 h-[34px] px-4 rounded-md border-none bg-primary text-white text-xs font-semibold cursor-pointer hover:bg-primary-hover">Save</button>
				</div>
			</div>
		</div>
	</div>
{/if}

{#if deleteTarget}
	<div onclick={(e) => e.target === e.currentTarget && (deleteTarget = null)}
		class="flex fixed inset-0 bg-slate-900/50 backdrop-blur-[3px] z-[100] items-center justify-center p-6 animate-[fadeIn_.18s_ease]">
		<div class="bg-bg-panel border border-border rounded-xl w-full max-w-[360px] max-h-[90vh] overflow-y-auto shadow-[0_24px_64px_rgba(0,0,0,.2)] animate-[popIn_.22s_cubic-bezier(.34,1.4,.64,1)]">
			<div class="flex items-center py-[18px] px-5 border-b border-border">
				<div class="text-sm font-semibold flex-1">Remove connection</div>
				<button onclick={() => deleteTarget = null} class="w-[26px] h-[26px] rounded-md border-none bg-transparent cursor-pointer flex items-center justify-center text-text-muted hover:bg-slate-100 hover:text-text-main">
					<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
				</button>
			</div>
			<div class="py-6 px-5 pb-5 text-center">
				<div class="w-11 h-11 rounded-full bg-red-50 flex items-center justify-center mx-auto mb-3.5">
					<svg viewBox="0 0 24 24" class="w-5 h-5 stroke-danger"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>
				</div>
				<div class="text-[15px] font-semibold mb-2">Remove this connection?</div>
				<div class="text-[13px] text-text-muted leading-[1.55]">
					<span class="font-mono font-semibold text-text-main">{deleteTarget.name}</span> will be permanently removed.
				</div>
			</div>
			<div class="flex items-center justify-between py-3.5 px-5 border-t border-border">
				<span></span>
				<div>
					<button onclick={() => deleteTarget = null} class="h-[34px] px-3.5 rounded-md border border-border bg-transparent text-xs font-medium text-text-muted cursor-pointer mr-1.5 hover:border-border-hover hover:text-text-main">Cancel</button>
					<button onclick={deleteConnection} class="h-[34px] px-4 rounded-md border-none bg-danger text-white text-xs font-semibold cursor-pointer hover:bg-red-600">Remove</button>
				</div>
			</div>
		</div>
	</div>
{/if}

<style>
	@keyframes fadeIn { from { opacity: 0; } to { opacity: 1; } }
	@keyframes popIn { from { opacity: 0; transform: scale(.95) translateY(8px); } to { opacity: 1; transform: scale(1) translateY(0); } }
</style>