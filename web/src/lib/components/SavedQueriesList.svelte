<script lang="ts">
	import { onMount } from 'svelte';

	const token = localStorage.getItem('token') || '';
	const apiBase = '/api/v1';
	let workspaceId = $state('');

	let queries = $state<any[]>([]);
	let loading = $state(true);
	let error = $state('');
	let menuOpen = $state<string | null>(null);
	let renameId = $state<string | null>(null);
	let renameValue = $state('');

	export function loadQueries() {
		if (!workspaceId) return;
		loading = true;
		fetch(`${apiBase}/workspaces/${workspaceId}/queries`, { headers: { Authorization: `Bearer ${token}` } })
			.then(r => r.ok ? r.json() : Promise.reject(`HTTP ${r.status}`))
			.then(data => { queries = data; loading = false; })
			.catch(e => { error = e; loading = false; });
	}

	onMount(async () => {
		try {
			const res = await fetch(`${apiBase}/workspaces`, { headers: { Authorization: `Bearer ${token}` } });
			if (res.ok) {
				const wss = await res.json();
				if (wss.length > 0) { workspaceId = wss[0].id; loadQueries(); }
			}
		} catch {}
	});

	if (typeof window !== 'undefined') {
		window.addEventListener('query-saved', () => loadQueries());
	}

	async function deleteQuery(id: string) {
		await fetch(`${apiBase}/workspaces/${workspaceId}/queries/${id}`, { method: 'DELETE', headers: { Authorization: `Bearer ${token}` } });
		menuOpen = null;
		loadQueries();
	}

	async function renameQuery(q: any) {
		await fetch(`${apiBase}/workspaces/${workspaceId}/queries/${q.id}`, {
			method: 'PATCH',
			headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
			body: JSON.stringify({ name: renameValue, sql: q.sql })
		});
		renameId = null;
		loadQueries();
	}

	function startRename(q: any) {
		renameId = q.id;
		renameValue = q.name;
		menuOpen = null;
	}
</script>

<div class="overflow-y-auto flex-1 px-1.5 pt-1.5 pb-2">
	{#if loading}
		<p class="text-xs text-text-muted px-2 py-4">Loading...</p>
	{:else if error}
		<p class="text-xs text-red-500 px-2 py-4">{error}</p>
	{:else if queries.length === 0}
		<p class="text-xs text-text-muted px-2 py-6 text-center">No saved queries yet.</p>
	{:else}
		<div class="text-[10px] font-bold tracking-wide uppercase text-text-muted pt-2 px-1.5 pb-1 flex items-center gap-1.5 after:content-[''] after:flex-1 after:h-px after:bg-border">Saved</div>
		{#each queries as q (q.id)}
			<div class="relative group">
				<button class="flex items-center w-full text-left py-1.5 px-2 rounded-md gap-2 cursor-pointer mb-px hover:bg-slate-100 dark:hover:bg-[#1c2128] bg-none border-none text-inherit"
					onclick={() => window.dispatchEvent(new CustomEvent('query-load', { detail: q }))}>
					<svg viewBox="0 0 24 24" class="shrink-0 text-text-muted w-3.5 h-3.5"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
					<div class="flex-1 min-w-0">
						{#if renameId === q.id}
							<input class="w-full h-6 bg-bg-toolbar border border-primary rounded px-1.5 text-xs outline-none" type="text" bind:value={renameValue} onkeydown={(e) => e.key === 'Enter' && renameQuery(q)} onblur={() => renameId = null} autofocus>
						{:else}
							<div class="text-xs font-medium overflow-hidden text-ellipsis whitespace-nowrap">{q.name}</div>
							<div class="text-[10px] text-text-muted mt-px">{q.sql.substring(0, 40)}{q.sql.length > 40 ? '...' : ''}</div>
						{/if}
					</div>
				</button>
				<button class="absolute right-0 top-1 w-5 h-5 rounded flex items-center justify-center text-text-muted bg-none border-none cursor-pointer opacity-0 group-hover:opacity-100 hover:bg-border hover:text-text-main"
					onclick={(e) => { e.stopPropagation(); menuOpen = menuOpen === q.id ? null : q.id; }}>
					<svg viewBox="0 0 24 24" class="w-3 h-3"><circle cx="12" cy="5" r="1.5"/><circle cx="12" cy="12" r="1.5"/><circle cx="12" cy="19" r="1.5"/></svg>
				</button>
				{#if menuOpen === q.id}
					<div class="absolute right-0 top-7 bg-bg-panel border border-border rounded-lg shadow-[0_8px_24px_rgba(0,0,0,.1)] z-50 min-w-[100px] overflow-hidden" onclick={() => menuOpen = null}>
						<button class="flex items-center gap-2 w-full text-left px-3 py-[7px] text-xs font-medium hover:bg-slate-50 dark:hover:bg-[#1c2128] bg-none border-none text-text-main cursor-pointer" onclick={() => startRename(q)}>
							<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
							Rename
						</button>
						<button class="flex items-center gap-2 w-full text-left px-3 py-[7px] text-xs font-medium hover:bg-red-50 dark:hover:bg-[#1f0f0f] bg-none border-none text-red-600 cursor-pointer" onclick={() => deleteQuery(q.id)}>
							<svg viewBox="0 0 24 24" class="w-3 h-3"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>
							Delete
						</button>
					</div>
				{/if}
			</div>
		{/each}
	{/if}
</div>
