<script lang="ts">
	import LeftSidebar from './LeftSidebar.svelte';
	import RightSidebar from './RightSidebar.svelte';
	import QueryEditor from './QueryEditor.svelte';
	import type { TableRef } from '$lib/metadata/client';
	import { m } from '$lib/paraglide/messages';
	import { browser } from '$app/environment';
	import PanelResizer from '$lib/ui/PanelResizer.svelte';
	import { loadWidth } from '$lib/ui/panelWidth';

	// Both sidebars can be widened on a large screen (#468).
	const LEFT = { min: 200, max: 560, initial: 260 };
	const RIGHT = { min: 280, max: 720, initial: 340 };
	let leftWidth = $state(browser ? loadWidth('workbench-left', LEFT) : LEFT.initial);
	let rightWidth = $state(browser ? loadWidth('workbench-right', RIGHT) : RIGHT.initial);

	let leftCollapsed = $state(false);
	let rightCollapsed = $state(true);
	let sidebarTab = $state<'datasources' | 'queries'>('datasources');
	let selectedTable = $state<TableRef | null>(null);
</script>

<div class="flex flex-1 gap-2.5 overflow-hidden p-2.5">
	<LeftSidebar
		bind:collapsed={leftCollapsed}
		bind:activeTab={sidebarTab}
		onSelectTable={(table) => (selectedTable = table)}
		width={leftWidth}
	/>
	{#if !leftCollapsed}
		<PanelResizer
			bind:value={leftWidth}
			bounds={LEFT}
			side="left"
			storageKey="workbench-left"
			label={m.ui_panelresizer_resize_sidebar()}
		/>
	{/if}
	<div
		class="flex min-w-0 flex-1 flex-col overflow-hidden rounded-[var(--radius)] border border-border bg-bg-panel shadow-sm"
	>
		<QueryEditor />
	</div>
	{#if !rightCollapsed}
		<PanelResizer
			bind:value={rightWidth}
			bounds={RIGHT}
			side="right"
			storageKey="workbench-right"
			label={m.ui_panelresizer_resize_inspector()}
		/>
	{/if}
	<RightSidebar bind:collapsed={rightCollapsed} {selectedTable} width={rightWidth} />
</div>
