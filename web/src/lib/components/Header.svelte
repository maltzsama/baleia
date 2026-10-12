<script lang="ts">
	import { workspaceStore } from '$lib/stores/workspace.svelte';

	let { onToggleDark, dark }: {
		onToggleDark: () => void;
		dark: boolean;
	} = $props();

	let wsOpen = $state(false);
	let showCreate = $state(false);
	let createName = $state('');
	let creating = $state(false);

	function toggleWs() { wsOpen = !wsOpen; closeCreate(); }

	function selectWs(ws: any) {
		workspaceStore.select(ws);
		wsOpen = false;
	}

	function openCreate(e: Event) {
		e.stopPropagation();
		createName = workspaceStore.workspaces.length === 0 ? 'Personal' : '';
		showCreate = true;
		wsOpen = false;
	}

	function closeCreate() { showCreate = false; createName = ''; }

	async function doCreate() {
		if (!createName.trim()) return;
		creating = true;
		const ws = await workspaceStore.create(createName.trim());
		creating = false;
		if (ws) closeCreate();
	}
</script>

<header class="h-12 bg-bg-panel flex items-center px-4 border-b border-border gap-3 shrink-0 relative z-40">

	<span class="font-semibold text-sm">SQL Editor</span>

	<div class="relative">
		{#if workspaceStore.current}
			<button onclick={toggleWs}
				class="flex items-center gap-2 bg-slate-50 dark:bg-slate-800 border border-border hover:border-border-hover rounded-md px-[10px] py-[5px] cursor-pointer text-xs font-semibold transition-all duration-150">
				<div class="w-2 h-2 rounded-full bg-success shrink-0"></div>
				<span>{workspaceStore.current.name}</span>
				<span class="text-text-muted font-normal">· Workspace</span>
				<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M6 9l6 6 6-6"/></svg>
			</button>
		{:else}
			<button onclick={toggleWs}
				class="flex items-center gap-2 border border-dashed border-border hover:border-border-hover rounded-md px-[10px] py-[5px] cursor-pointer text-xs font-semibold transition-all duration-150 text-text-muted hover:text-text-main">
				<svg viewBox="0 0 24 24" class="w-3 h-3"><circle cx="12" cy="12" r="10"/><path d="M12 8v8M8 12h8"/></svg>
				{workspaceStore.workspaces.length > 0 ? 'Select workspace' : 'Create workspace'}
			</button>
		{/if}

		{#if wsOpen}
			<div class="absolute top-full left-0 mt-1 w-64 bg-bg-panel border border-border rounded-lg shadow-lg z-50 py-1">
				{#if workspaceStore.workspaces.length > 0}
					{#each workspaceStore.workspaces as ws}
						<button onclick={() => selectWs(ws)}
							class="w-full text-left px-3 py-2 text-xs hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-2 cursor-pointer bg-none border-none text-inherit">
							<span class="w-1.5 h-1.5 rounded-full {ws.id === workspaceStore.current?.id ? 'bg-primary' : 'bg-transparent border border-border'}"></span>
							<span class="font-medium">{ws.name}</span>
							<span class="text-text-muted ml-auto text-[10px]">{ws.type}</span>
						</button>
					{/each}
				{:else}
					<div class="px-3 py-3 text-xs text-text-muted text-center">No workspaces yet</div>
				{/if}
				<div class="border-t border-border my-1"></div>
				<button onclick={openCreate}
					class="w-full text-left px-3 py-2 text-xs hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-2 cursor-pointer bg-none border-none text-inherit text-primary font-medium">
					<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><path d="M12 5v14M5 12h14"/></svg>
					Create workspace
				</button>
			</div>
		{/if}
	</div>

	<div class="flex-1"></div>

	<div class="flex items-center gap-1.5 text-[11px] text-text-muted">
		<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
		<div
			role="switch"
			onclick={onToggleDark}
			class="w-8 h-[18px] rounded-full cursor-pointer relative transition-colors duration-200 shrink-0
				{dark ? 'bg-primary' : 'bg-border'}">
			<span class="absolute w-[14px] h-[14px] bg-white rounded-full top-[2px] left-[2px] shadow-sm transition-transform duration-200
				{dark ? 'translate-x-[14px]' : 'translate-x-0'}">
			</span>
		</div>
	</div>

</header>

{#if showCreate}
	<div class="fixed inset-0 bg-black/30 backdrop-blur-[2px] z-[100] flex items-center justify-center p-6" onclick={(e) => e.target === e.currentTarget && closeCreate()}>
		<div class="bg-bg-panel border border-border rounded-xl w-full max-w-[360px] shadow-[0_24px_64px_rgba(0,0,0,.2)]" onclick={(e) => e.stopPropagation()}>
			<div class="flex items-center px-5 py-[18px] border-b border-border">
				<h2 class="text-sm font-semibold flex-1">Create workspace</h2>
				<button class="w-[26px] h-[26px] rounded-md border-none bg-transparent cursor-pointer flex items-center justify-center text-text-muted hover:bg-bg-toolbar" onclick={closeCreate}>×</button>
			</div>
			<div class="p-5">
				<input class="w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs outline-none focus:border-primary mb-3.5" type="text" placeholder="Workspace name" bind:value={createName} onkeydown={(e) => e.key === 'Enter' && doCreate()}>
				<div class="flex justify-end gap-1.5">
					<button class="h-[34px] px-3.5 rounded-md border border-border bg-transparent text-xs font-medium text-text-muted cursor-pointer hover:border-border-hover" onclick={closeCreate}>Cancel</button>
					<button class="h-[34px] px-4 rounded-md border-none bg-primary text-white text-xs font-semibold cursor-pointer hover:bg-primary-hover disabled:opacity-50 disabled:cursor-not-allowed" disabled={!createName.trim() || creating} onclick={doCreate}>
						{creating ? 'Creating...' : 'Create'}
					</button>
				</div>
			</div>
		</div>
	</div>
{/if}
