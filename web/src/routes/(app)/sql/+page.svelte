<script lang="ts">
	import LeftSidebar from '$lib/components/LeftSidebar.svelte';
	import RightSidebar from '$lib/components/RightSidebar.svelte';
	import QueryEditor from '$lib/components/QueryEditor.svelte';

	let leftCollapsed = $state(false);
	let rightCollapsed = $state(true);
	let sidebarTab = $state<'datasources' | 'queries'>('datasources');
	let selectedTable = $state<{catalog: string, namespace: string, table: string} | null>(null);
</script>

<div class="flex-1 flex overflow-hidden p-2.5 gap-2.5">
	<LeftSidebar bind:collapsed={leftCollapsed} bind:activeTab={sidebarTab} onSelectTable={(t) => selectedTable = t} />
	<div class="flex-1 min-w-0 flex flex-col overflow-hidden bg-bg-panel rounded-[var(--radius)] border border-border shadow-sm">
		<QueryEditor />
	</div>
	<RightSidebar bind:collapsed={rightCollapsed} {selectedTable} />
</div>