<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import TableDetail from './TableDetail.svelte';
	import AssistantPanel from '$lib/assistant/Panel.svelte';
	import { assistantStore } from '$lib/assistant/store.svelte';
	import type { TableRef } from '$lib/metadata/client';

	let {
		collapsed = $bindable(true),
		selectedTable,
		width = 340
	}: {
		collapsed?: boolean;
		selectedTable?: TableRef | null;
		/** Expanded width in px (#468); the workbench makes it resizable. */
		width?: number;
	} = $props();

	let tab = $state<'inspector' | 'assistant'>('inspector');

	onMount(() => {
		void assistantStore.loadStatus();
		const onKey = (event: KeyboardEvent) => {
			if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'i') {
				event.preventDefault();
				if (assistantStore.enabled) {
					collapsed = false;
					tab = 'assistant';
				}
			}
		};
		window.addEventListener('keydown', onKey);
		return () => window.removeEventListener('keydown', onKey);
	});
</script>

<aside
	class="bg-bg-panel border border-border rounded-[var(--radius)] shadow-sm flex flex-col overflow-hidden shrink-0
	{collapsed ? 'w-9' : ''}"
	style:width={collapsed ? undefined : `${width}px`}
>
	{#if collapsed}
		<div class="flex flex-col items-center py-2.5 gap-2.5 flex-1">
			<button
				onclick={() => (collapsed = false)}
				title={m.common_expand()}
				aria-label={m.components_rightsidebar_expand_the_inspector()}
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
				>{m.components_rightsidebar_inspector()}</span
			>
		</div>
	{:else}
		<div class="flex flex-col flex-1 overflow-hidden">
			<div class="flex items-center gap-2 border-b border-border bg-bg-toolbar px-2.5 py-[7px]">
				{#if assistantStore.enabled}
					<div
						class="flex flex-1 gap-1"
						role="tablist"
						aria-label={m.components_rightsidebar_right_sidebar()}
					>
						<button
							type="button"
							role="tab"
							aria-selected={tab === 'inspector'}
							class="rounded px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wide {tab ===
							'inspector'
								? 'bg-bg-panel text-text-main'
								: 'text-text-muted hover:text-text-main'}"
							onclick={() => (tab = 'inspector')}
						>
							{m.components_rightsidebar_inspector()}
						</button>
						<button
							type="button"
							role="tab"
							aria-selected={tab === 'assistant'}
							class="rounded px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wide {tab ===
							'assistant'
								? 'bg-bg-panel text-text-main'
								: 'text-text-muted hover:text-text-main'}"
							onclick={() => (tab = 'assistant')}
						>
							{m.components_rightsidebar_assistant()}
						</button>
					</div>
				{:else}
					<span class="flex-1 text-[10px] font-bold uppercase tracking-wide text-text-muted"
						>{m.components_rightsidebar_inspector()}</span
					>
				{/if}
				<button
					onclick={() => (collapsed = true)}
					title={m.components_rightsidebar_collapse()}
					aria-label={m.components_rightsidebar_collapse_the_inspector()}
					class="w-5 h-5 rounded flex items-center justify-center text-text-muted bg-none border-none cursor-pointer hover:bg-border hover:text-text-main"
				>
					<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M9 18l6-6-6-6" /></svg>
				</button>
			</div>
			{#if tab === 'assistant' && assistantStore.enabled}
				<AssistantPanel />
			{:else}
				<TableDetail table={selectedTable ?? null} />
			{/if}
		</div>
	{/if}
</aside>
