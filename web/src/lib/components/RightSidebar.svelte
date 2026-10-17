<script lang="ts">
	import TableDetail from './TableDetail.svelte';
	import type { TableRef } from '$lib/metadata/client';

	let {
		collapsed = $bindable(true),
		selectedTable
	}: {
		collapsed?: boolean;
		selectedTable?: TableRef | null;
	} = $props();
</script>

<aside
	class="bg-bg-panel border border-border rounded-[var(--radius)] shadow-sm flex flex-col overflow-hidden transition-[width] duration-200 shrink-0
	{collapsed ? 'w-9' : 'w-[320px]'}"
>
	{#if collapsed}
		<div class="flex flex-col items-center py-2.5 gap-2.5 flex-1">
			<button
				onclick={() => (collapsed = false)}
				title="Expand"
				aria-label="Expand the inspector"
				class="w-[22px] h-[22px] rounded-[5px] cursor-pointer flex items-center justify-center text-text-muted bg-none border-none
					hover:bg-blue-50 hover:text-primary dark:hover:bg-[#152236]"
			>
				<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"
					><circle cx="12" cy="12" r="10" /><line x1="12" y1="8" x2="12" y2="12" /><line
						x1="12"
						y1="16"
						x2="12.01"
						y2="16"
					/></svg
				>
			</button>
			<span
				class="[writing-mode:vertical-rl] text-[10px] font-semibold tracking-wide uppercase text-text-muted rotate-180 mt-1"
				>Inspector</span
			>
		</div>
	{:else}
		<div class="flex flex-col flex-1 overflow-hidden">
			<div class="flex items-center gap-2 border-b border-border bg-bg-toolbar px-2.5 py-[7px]">
				<span class="flex-1 text-[10px] font-bold uppercase tracking-wide text-text-muted"
					>Inspector</span
				>
				<button
					onclick={() => (collapsed = true)}
					title="Collapse"
					aria-label="Collapse the inspector"
					class="w-5 h-5 rounded flex items-center justify-center text-text-muted bg-none border-none cursor-pointer hover:bg-border hover:text-text-main"
				>
					<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M9 18l6-6-6-6" /></svg>
				</button>
			</div>
			<TableDetail table={selectedTable ?? null} />
		</div>
	{/if}
</aside>
