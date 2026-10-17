<script lang="ts">
	import LeftSidebar from './LeftSidebar.svelte';
	import RightSidebar from './RightSidebar.svelte';
	import QueryEditor from './QueryEditor.svelte';
	import type { TableRef } from '$lib/metadata/client';

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
	/>
	<div
		class="flex min-w-0 flex-1 flex-col overflow-hidden rounded-[var(--radius)] border border-border bg-bg-panel shadow-sm"
	>
		<QueryEditor />
	</div>
	<RightSidebar bind:collapsed={rightCollapsed} {selectedTable} />
</div>
