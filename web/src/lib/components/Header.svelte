<script lang="ts">
	import { workspaceStore } from '$lib/stores/workspace.svelte';

	let { onToggleDark, dark }: {
		onToggleDark: () => void;
		dark: boolean;
	} = $props();

	let wsOpen = $state(false);

	function toggleWs() { wsOpen = !wsOpen; }

	function selectWs(ws: any) {
		workspaceStore.select(ws);
		wsOpen = false;
	}
</script>

<header class="h-12 bg-bg-panel flex items-center px-4 border-b border-border gap-3 shrink-0">

	<span class="font-semibold text-sm">SQL Editor</span>

	{#if workspaceStore.current}
		<div class="relative">
			<button onclick={toggleWs}
				class="flex items-center gap-2 bg-slate-50 dark:bg-slate-800 border border-border hover:border-border-hover rounded-md px-[10px] py-[5px] cursor-pointer text-xs font-semibold transition-all duration-150">
				<div class="w-2 h-2 rounded-full bg-success shrink-0"></div>
				<span>{workspaceStore.current.name}</span>
				<span class="text-text-muted font-normal">· Workspace</span>
				<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M6 9l6 6 6-6"/></svg>
			</button>
			{#if wsOpen}
				<div class="absolute top-full left-0 mt-1 w-56 bg-bg-panel border border-border rounded-lg shadow-lg z-50 py-1">
					{#each workspaceStore.workspaces as ws}
						<button onclick={() => selectWs(ws)}
							class="w-full text-left px-3 py-2 text-xs hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-2 cursor-pointer bg-none border-none text-inherit">
							<span class="w-1.5 h-1.5 rounded-full {ws.id === workspaceStore.current?.id ? 'bg-primary' : 'bg-transparent border border-border'}"></span>
							<span class="font-medium">{ws.name}</span>
							<span class="text-text-muted ml-auto text-[10px]">{ws.type}</span>
						</button>
					{/each}
				</div>
			{/if}
		</div>
	{:else}
		<div class="text-xs text-text-muted">No workspace selected</div>
	{/if}

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
