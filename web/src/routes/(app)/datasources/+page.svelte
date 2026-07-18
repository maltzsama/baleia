<script lang="ts">
	import { onMount } from 'svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';

	const token = localStorage.getItem('token') || '';
	let connections = $state<any[]>([]);
	let loading = $state(true);
	let error = $state('');
	let search = $state('');
	let modalOpen = $state(false);
	let editingId = $state('');
	let deleteTarget = $state<any>(null);
	let form = $state({
		name: '', type: 'polaris', url: '',
		host: '', port: '', database: '',
		client_id: '', client_secret: '',
		username: '', password: '',
	});
	let testStatus = $state<'idle' | 'testing' | 'success' | 'fail'>('idle');
	let testMessage = $state('');
	let verifiedId = $state('');

	const engineLabels: Record<string, string> = {
		polaris: 'Polaris', nessie: 'Nessie', iceberg: 'Iceberg',
		glue: 'Glue', hive: 'Hive', postgresql: 'PostgreSQL', mysql: 'MySQL',
	};
	const engineClasses: Record<string, string> = {
		polaris: 'bg-blue-50 text-blue-700', nessie: 'bg-green-50 text-green-700',
		iceberg: 'bg-fuchsia-50 text-purple-700', glue: 'bg-orange-50 text-orange-700',
		hive: 'bg-red-50 text-red-700', postgresql: 'bg-blue-50 text-blue-700', mysql: 'bg-amber-50 text-amber-700',
	};

	onMount(() => { loadConnections(); });

	async function loadConnections() {
		loading = true; error = '';
		try {
			const res = await fetch('/api/v1/catalog-connections', { headers: { Authorization: `Bearer ${token}` } });
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			connections = await res.json();
		} catch (e: any) { error = e.message; }
		loading = false;
	}

	const filtered = $derived(connections.filter(c => !search || c.name.toLowerCase().includes(search.toLowerCase())));

	function openCreate() {
		editingId = '';
		form = { name: '', type: 'polaris', url: '', client_id: '', client_secret: '', username: '', password: '' };
		modalOpen = true;
	}
	async function openEdit(c: any) {
		editingId = c.id;
		form = { name: c.name, type: c.type, url: c.url || '', host: c.host || '', port: c.port || '', database: '', client_id: '', client_secret: '', username: '', password: '' };
		if (!c.host && c.url) {
			const m = c.url.match(/jdbc:[a-z]+:\/\/([^:]+):(\d+)\/(.+)/);
			if (m) { form.host = m[1]; form.port = m[2]; form.database = m[3]; }
		}
		modalOpen = true;
		try {
			const res = await fetch(`/api/v1/catalog-connections/${c.id}`, { headers: { Authorization: `Bearer ${token}` } });
			if (res.ok) {
				const detail = await res.json();
				if (detail.credentials) {
					const creds = detail.credentials as any;
					if (creds.client_id) form.client_id = creds.client_id;
					if (creds.client_secret) form.client_secret = creds.client_secret;
					if (creds.username) form.username = creds.username;
					if (creds.password) form.password = creds.password;
				}
			}
		} catch {}
	}
	function closeModal() { modalOpen = false; testStatus = 'idle'; testMessage = ''; verifiedId = ''; }

	function isFormValid() {
		if (!form.name) return false;
		if (isSqlType()) {
			if (!form.host || !form.database || !form.username || !form.password) return false;
		} else if (isIcebergType()) {
			if (!form.url || !form.client_id || !form.client_secret) return false;
		} else {
			if (!form.url) return false;
		}
		return true;
	}

	function isSqlType() { return form.type === 'postgresql' || form.type === 'mysql'; }
	function isIcebergType() { return ['polaris', 'nessie', 'iceberg'].includes(form.type); }
	const showIcebergCreds = $derived(isIcebergType());
	const showSqlCreds = $derived(isSqlType());

	function buildConnUrl() {
		if (isSqlType()) {
			const db = form.database || 'postgres';
			return `jdbc:${form.type}://${form.host || 'localhost'}:${form.port || (form.type === 'postgresql' ? '5432' : '3306')}/${db}`;
		}
		return form.url;
	}

	function buildConnBody(connUrl: string) {
		const body: any = { name: form.name, type: form.type, url: connUrl, default_readonly: false };
		if (editingId) {
			if (isIcebergType() || isSqlType()) {
				const creds: any = {};
				if (isIcebergType()) { creds.client_id = form.client_id; creds.client_secret = form.client_secret; }
				if (isSqlType()) { creds.username = form.username; creds.password = form.password; }
				body.credentials = creds;
			}
		} else {
			if (isIcebergType()) {
				body.polaris_credentials = { client_id: form.client_id, client_secret: form.client_secret };
			}
			if (isSqlType()) {
				const creds = { username: form.username, password: form.password };
				if (form.type === 'postgresql') body.postgresql_credentials = creds;
				else body.mysql_credentials = creds;
			}
		}
		return body;
	}

	async function testConnection() {
		testStatus = 'testing';
		testMessage = '';
		verifiedId = '';

		const connUrl = buildConnUrl();
		const body = buildConnBody(connUrl);
		const method = editingId ? 'PATCH' : 'POST';
		const apiUrl = editingId ? `/api/v1/catalog-connections/${editingId}` : '/api/v1/catalog-connections';

		try {
			const saveRes = await fetch(apiUrl, {
				method, headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
				body: JSON.stringify(body)
			});
			if (!saveRes.ok) {
				testStatus = 'fail';
				testMessage = `Save failed (HTTP ${saveRes.status})`;
				return;
			}

			const saved = await saveRes.json();
			const testId = saved.id || editingId;

			const testRes = await fetch(`/api/v1/catalog-connections/${testId}/test`, {
				method: 'POST', headers: { Authorization: `Bearer ${token}` }
			});
			const testResult = await testRes.json();

			if (testResult.success) {
				testStatus = 'success';
				testMessage = '';
				verifiedId = testId;
			} else {
				testStatus = 'fail';
				testMessage = testResult.error || 'Connection test failed';
				if (!editingId) {
					await fetch(`/api/v1/catalog-connections/${testId}`, {
						method: 'DELETE', headers: { Authorization: `Bearer ${token}` }
					});
				}
			}
		} catch (e: any) {
			testStatus = 'fail';
			testMessage = e.message || 'Connection error';
		}
	}

	async function saveConnection() {
		if (verifiedId) {
			closeModal();
			loadConnections();
			workspaceStore.load();
			return;
		}
		const connUrl = buildConnUrl();
		const body = buildConnBody(connUrl);
		const method = editingId ? 'PATCH' : 'POST';
		const apiUrl = editingId ? `/api/v1/catalog-connections/${editingId}` : '/api/v1/catalog-connections';
		await fetch(apiUrl, { method, headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` }, body: JSON.stringify(body) });
		closeModal(); loadConnections(); workspaceStore.load();
	}

	async function deleteConnection() {
		if (!deleteTarget) return;
		await fetch(`/api/v1/catalog-connections/${deleteTarget.id}`, { method: 'DELETE', headers: { Authorization: `Bearer ${token}` } });
		deleteTarget = null; loadConnections();
	}

	function displayUrl(conn: any) {
		if (conn.host) return `${conn.host}:${conn.port || ''}`;
		const sqlTypes = ['postgresql', 'mysql'];
		if (sqlTypes.includes(conn.type) && conn.url) {
			const m = conn.url.match(/jdbc:[a-z]+:\/\/([^\/]+)/);
			if (m) return m[1];
		}
		return conn.url;
	}

	function engineShort(type: string) { return type.slice(0, 2).toUpperCase(); }
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="page-header flex items-end justify-between mb-5">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">Datasources</h1>
			<p class="text-[13px] text-text-muted mt-1">Manage catalog connections for your workspaces.</p>
		</div>
		<button class="btn-primary flex items-center gap-1.5 bg-primary text-white border-none rounded-md px-4 h-9 text-[13px] font-semibold cursor-pointer transition-colors duration-150 hover:bg-primary-hover" onclick={openCreate}>
			<svg viewBox="0 0 24 24" class="w-[13px] h-[13px] stroke-white"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
			New connection
		</button>
	</div>

	<div class="toolbar flex items-center gap-2.5 mb-5">
		<div class="search-box flex items-center gap-1.5 bg-bg-panel border border-border rounded-md px-2.5 h-[34px] w-60 transition-shadow duration-150 focus-within:border-primary focus-within:shadow-[0_0_0_3px_rgba(59,130,246,.08)]">
			<svg viewBox="0 0 24 24" class="w-[13px] h-[13px] text-text-muted shrink-0"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
			<input type="text" placeholder="Search connections..." bind:value={search} class="border-none bg-transparent outline-none text-xs text-text-main w-full placeholder:text-text-muted">
		</div>
		<span class="ml-auto text-xs text-text-muted font-mono">{filtered.length} connection{filtered.length !== 1 ? 's' : ''}</span>
	</div>

	<div class="ds-grid grid gap-3" style="grid-template-columns: repeat(auto-fill, minmax(300px, 1fr))">
		{#if loading}
			<p class="text-text-muted text-sm py-12 text-center col-span-full">Loading connections...</p>
		{:else if error}
			<p class="text-red-600 text-sm py-12 text-center col-span-full">{error}</p>
		{:else if filtered.length === 0}
			<p class="text-text-muted text-sm py-12 text-center col-span-full">No connections yet. <button class="text-primary font-semibold cursor-pointer bg-none border-none" onclick={openCreate}>Create one</button></p>
		{:else}
			{#each filtered as conn (conn.id)}
				<div class="ds-card bg-bg-panel border border-border rounded-lg p-4 relative transition-all duration-150 hover:border-border-hover hover:shadow-[0_2px_8px_rgba(0,0,0,.06)]">
					<div class="flex items-start gap-3 mb-3 pr-[72px]">
						<div class="engine-badge w-9 h-9 rounded-lg flex items-center justify-center font-mono text-[11px] font-bold shrink-0 {engineClasses[conn.type] || 'bg-slate-50 text-slate-600'}">{engineShort(conn.type)}</div>
						<div class="min-w-0 flex-1">
							<div class="text-[13px] font-semibold truncate mb-0.5">{conn.name}</div>
							<div class="text-[11px] text-text-muted font-mono truncate">{displayUrl(conn)} · {engineLabels[conn.type] || conn.type}</div>
						</div>
					</div>
					<div class="ds-status absolute top-3.5 right-3.5 flex items-center gap-1 text-[10px] font-semibold font-mono px-2 py-0.5 rounded-full bg-green-50 text-green-700"><span class="w-[5px] h-[5px] rounded-full bg-green-500 shadow-[0_0_0_2px_rgba(34,197,94,.2)] shrink-0"></span>Active</div>
					<div class="flex gap-1.5">
						<button class="ds-btn flex-1 h-[30px] rounded-[5px] border border-border bg-transparent text-[11px] font-semibold text-text-muted cursor-pointer transition-all duration-150 flex items-center justify-center gap-1 hover:border-border-hover hover:text-text-main hover:bg-bg-toolbar" onclick={() => openEdit(conn)}>
							<svg viewBox="0 0 24 24" class="w-2.5 h-2.5"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg> Edit
						</button>
						<button class="ds-btn flex-1 h-[30px] rounded-[5px] border border-border bg-transparent text-[11px] font-semibold text-text-muted cursor-pointer transition-all duration-150 flex items-center justify-center gap-1 hover:border-red-300 hover:text-red-600 hover:bg-red-50" onclick={() => deleteTarget = conn}>
							<svg viewBox="0 0 24 24" class="w-2.5 h-2.5"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg> Delete
						</button>
					</div>
				</div>
			{/each}
		{/if}
	</div>
</div>

{#if modalOpen}
	<div class="modal-overlay fixed inset-0 bg-black/30 backdrop-blur-[2px] z-[100] flex items-center justify-center p-6" onclick={(e) => e.target === e.currentTarget && closeModal()}>
		<div class="modal bg-bg-panel border border-border rounded-xl w-full max-w-[500px] max-h-[90vh] overflow-y-auto shadow-[0_24px_64px_rgba(0,0,0,.2)]">
			<div class="modal-header flex items-center px-5 py-[18px] border-b border-border sticky top-0 bg-bg-panel rounded-t-xl z-[1]">
				<h2 class="text-sm font-semibold flex-1">{editingId ? 'Edit — ' + form.name : 'New connection'}</h2>
				<button class="w-[26px] h-[26px] rounded-md border-none bg-transparent cursor-pointer flex items-center justify-center text-text-muted hover:bg-bg-toolbar hover:text-text-main" onclick={closeModal}>
					<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
				</button>
			</div>
			<div class="modal-body p-5">
				<div class="section text-[10px] font-bold uppercase tracking-[0.8px] text-text-muted mb-3 mt-4 flex items-center gap-2.5 after:content-[''] after:flex-1 after:h-px after:bg-border">Engine</div>
				<div class="engine-grid grid grid-cols-3 gap-2 mb-4">
					{#each Object.entries(engineLabels) as [key, label]}
						<div class="engine-opt flex flex-col items-center gap-[5px] py-2.5 px-1.5 rounded-md border border-border cursor-pointer transition-colors duration-150 hover:border-border-hover {form.type === key ? 'border-primary bg-blue-50' : ''}" onclick={() => form.type = key}>
							<div class="eicon w-7 h-7 rounded-md flex items-center justify-center font-mono text-[10px] font-bold {engineClasses[key]}">{engineShort(key)}</div>
							<span class="text-[10px] font-semibold {form.type === key ? 'text-primary' : 'text-text-muted'}">{label}</span>
						</div>
					{/each}
				</div>
				<div class="mfield mb-3.5"><label class="block text-[11px] font-semibold mb-[5px]">Connection name</label><input class="minput w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 focus:bg-bg-panel focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] placeholder:text-text-muted" type="text" placeholder="e.g. analytics-prod" bind:value={form.name}></div>

				{#if isIcebergType()}
					<div class="mfield mb-3.5"><label class="block text-[11px] font-semibold mb-[5px]">REST API URL</label><input class="minput w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 focus:bg-bg-panel focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] font-mono placeholder:text-text-muted" type="text" placeholder="http://polaris:8181" bind:value={form.url}></div>
				{:else if isSqlType()}
					<div class="grid grid-cols-3 gap-2 mb-3.5">
						<div class="col-span-2"><label class="block text-[11px] font-semibold mb-[5px]">Host</label><input class="minput w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 focus:bg-bg-panel focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] font-mono placeholder:text-text-muted" type="text" placeholder="postgres" bind:value={form.host}></div>
						<div><label class="block text-[11px] font-semibold mb-[5px]">Port</label><input class="minput w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 focus:bg-bg-panel focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] font-mono placeholder:text-text-muted" type="text" placeholder={form.type === 'postgresql' ? '5432' : '3306'} bind:value={form.port}></div>
					</div>
					<div class="mfield mb-3.5"><label class="block text-[11px] font-semibold mb-[5px]">Database</label><input class="minput w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 focus:bg-bg-panel focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] font-mono placeholder:text-text-muted" type="text" placeholder="baleia" bind:value={form.database}></div>
				{:else}
					<div class="mfield mb-3.5"><label class="block text-[11px] font-semibold mb-[5px]">URL</label><input class="minput w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 focus:bg-bg-panel focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] font-mono placeholder:text-text-muted" type="text" placeholder="http://host:port" bind:value={form.url}></div>
				{/if}

				{#if showIcebergCreds}
					<div class="section text-[10px] font-bold uppercase tracking-[0.8px] text-text-muted mb-3 mt-4 flex items-center gap-2.5 after:content-[''] after:flex-1 after:h-px after:bg-border">Credentials</div>
					{#if editingId}
						<p class="text-[10px] text-text-muted -mt-2 mb-3">Leave blank to keep current values.</p>
					{/if}
					<div class="grid grid-cols-2 gap-2 mb-3.5">
						<div><label class="block text-[11px] font-semibold mb-[5px]">Client ID</label><input class="minput w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 focus:bg-bg-panel focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)]" type="text" bind:value={form.client_id}></div>
						<div><label class="block text-[11px] font-semibold mb-[5px]">Client Secret</label><input class="minput w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 focus:bg-bg-panel focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)]" type="password" bind:value={form.client_secret}></div>
					</div>
				{/if}

				{#if showSqlCreds}
					<div class="section text-[10px] font-bold uppercase tracking-[0.8px] text-text-muted mb-3 mt-4 flex items-center gap-2.5 after:content-[''] after:flex-1 after:h-px after:bg-border">Credentials</div>
					{#if editingId}
						<p class="text-[10px] text-text-muted -mt-2 mb-3">Leave blank to keep current values.</p>
					{/if}
					<div class="grid grid-cols-2 gap-2 mb-3.5">
						<div><label class="block text-[11px] font-semibold mb-[5px]">Username</label><input class="minput w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 focus:bg-bg-panel focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)]" type="text" bind:value={form.username}></div>
						<div><label class="block text-[11px] font-semibold mb-[5px]">Password</label><input class="minput w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs text-text-main outline-none transition-shadow duration-150 focus:bg-bg-panel focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)]" type="password" bind:value={form.password}></div>
					</div>
				{/if}
			</div>
			<div class="modal-footer flex items-center justify-between px-5 py-3.5 border-t border-border">
				<div>
					{#if testStatus === 'testing'}
						<span class="text-xs text-text-muted">Testing connection…</span>
					{:else if testStatus === 'success'}
						<span class="text-xs text-green-600 font-medium">✓ Connection successful</span>
					{:else if testStatus === 'fail'}
						<span class="text-xs text-red-600 font-medium">✗ {testMessage}</span>
					{/if}
				</div>
				<div class="flex gap-1.5">
					<button class="h-[34px] px-3.5 rounded-md border border-border bg-transparent text-xs font-medium text-text-muted cursor-pointer transition-colors duration-150 hover:border-primary hover:text-primary disabled:opacity-40 disabled:cursor-not-allowed"
						onclick={testConnection}
						disabled={testStatus === 'testing' || !isFormValid()}>
						{testStatus === 'testing' ? 'Testing…' : 'Test Connection'}
					</button>
					<button class="btn-cancel h-[34px] px-3.5 rounded-md border border-border bg-transparent text-xs font-medium text-text-muted cursor-pointer transition-colors duration-150 hover:border-border-hover hover:text-text-main" onclick={closeModal}>Cancel</button>
					<button class="btn-save flex items-center gap-1.5 h-[34px] px-4 rounded-md border-none bg-primary text-white text-xs font-semibold cursor-pointer transition-colors duration-150 hover:bg-primary-hover disabled:opacity-40 disabled:cursor-not-allowed"
						onclick={saveConnection}
						disabled={!editingId && testStatus !== 'success'}>
						{editingId ? (testStatus === 'success' ? 'Save' : 'Save anyway') : (testStatus === 'success' ? 'Create' : 'Save anyway')}
					</button>
				</div>
			</div>
		</div>
	</div>
{/if}

{#if deleteTarget}
	<div class="modal-overlay fixed inset-0 bg-black/30 backdrop-blur-[2px] z-[100] flex items-center justify-center p-6" onclick={(e) => e.target === e.currentTarget && (deleteTarget = null)}>
		<div class="modal bg-bg-panel border border-border rounded-xl w-full max-w-[360px] shadow-[0_24px_64px_rgba(0,0,0,.2)]">
			<div class="modal-header flex items-center px-5 py-[18px] border-b border-border">
				<h2 class="text-sm font-semibold flex-1">Remove connection</h2>
				<button class="w-[26px] h-[26px] rounded-md border-none bg-transparent cursor-pointer flex items-center justify-center text-text-muted hover:bg-bg-toolbar hover:text-text-main" onclick={() => deleteTarget = null}>
					<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
				</button>
			</div>
			<div class="px-5 pt-6 pb-5 text-center">
				<div class="w-11 h-11 rounded-full bg-red-50 flex items-center justify-center mx-auto mb-3.5">
					<svg viewBox="0 0 24 24" class="w-5 h-5 stroke-red-600"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>
				</div>
				<h3 class="text-[15px] font-semibold mb-2">Remove this connection?</h3>
				<p class="text-[13px] text-text-muted leading-[1.55]"><span class="font-mono font-semibold text-text-main">{deleteTarget.name}</span> will be permanently removed.</p>
			</div>
			<div class="modal-footer flex items-center justify-between px-5 py-3.5 border-t border-border">
				<span></span>
				<div class="flex gap-1.5">
					<button class="btn-cancel h-[34px] px-3.5 rounded-md border border-border bg-transparent text-xs font-medium text-text-muted cursor-pointer transition-colors duration-150 hover:border-border-hover hover:text-text-main" onclick={() => deleteTarget = null}>Cancel</button>
					<button class="h-[34px] px-4 rounded-md border-none bg-red-600 text-white text-xs font-semibold cursor-pointer hover:bg-red-700" onclick={deleteConnection}>Remove</button>
				</div>
			</div>
		</div>
	</div>
{/if}
