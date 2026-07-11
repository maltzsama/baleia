<script lang="ts">
	import GlobalNav from '$lib/components/GlobalNav.svelte';
	import Header from '$lib/components/Header.svelte';
	import LeftSidebar from '$lib/components/LeftSidebar.svelte';
	import RightSidebar from '$lib/components/RightSidebar.svelte';
	import QueryEditor from '$lib/components/QueryEditor.svelte';
	import { browser } from '$app/environment';

	let leftCollapsed = $state(false);
	let rightCollapsed = $state(false);
	let sidebarTab = $state<'database' | 'queries'>('database');
	let datasource = $state('polaris-prod');
	let dark = $state(browser && localStorage.getItem('theme') === 'dark');

	$effect(() => {
		if (!browser) return;
		document.body.classList.toggle('dark', dark);
		localStorage.setItem('theme', dark ? 'dark' : 'light');
	});
</script>

<div class="app-shell">
	<GlobalNav {dark} onToggleDark={() => dark = !dark} />

	<div class="app-content">
		<Header
			{datasource}
			onChangeDatasource={(ds) => datasource = ds}
			onToggleDark={() => dark = !dark}
			{dark}
		/>

		<div class="main-workspace">
			<LeftSidebar
				bind:collapsed={leftCollapsed}
				bind:activeTab={sidebarTab}
			/>

			<div class="center-panel panel">
				<QueryEditor {dark} />
			</div>

			<RightSidebar
				bind:collapsed={rightCollapsed}
			/>
		</div>
	</div>
</div>

<style>
	.app-shell { height: 100vh; display: flex; overflow: hidden; }
	.app-content { flex: 1; display: flex; flex-direction: column; overflow: hidden; }
	.main-workspace { flex: 1; display: flex; overflow: hidden; padding: 10px; gap: 10px; }
	.panel { background: var(--bg-panel); border-radius: var(--radius); border: 1px solid var(--border-light); box-shadow: var(--shadow-sm); }
	.center-panel { flex: 1; min-width: 0; display: flex; flex-direction: column; overflow: hidden; }
</style>
