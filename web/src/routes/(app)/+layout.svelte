<script lang="ts">
	import GlobalNav from '$lib/components/GlobalNav.svelte';
	import Header from '$lib/components/Header.svelte';
	import { browser } from '$app/environment';
	import { onMount } from 'svelte';
	import { authStore } from '$lib/stores/auth.svelte';

	let { children } = $props();
	let datasource = $state('polaris-prod');
	let dark = $state(browser && localStorage.getItem('theme') === 'dark');

	$effect(() => {
		if (!browser) return;
		document.body.classList.toggle('dark', dark);
		localStorage.setItem('theme', dark ? 'dark' : 'light');
	});

	onMount(() => {
		authStore.load();
	});
</script>

<div class="h-screen flex overflow-hidden">
	<GlobalNav {dark} onToggleDark={() => dark = !dark} />
	<div class="flex-1 flex flex-col overflow-hidden">
		<Header {datasource} onChangeDatasource={(ds) => datasource = ds} onToggleDark={() => dark = !dark} {dark} />
		{@render children()}
	</div>
</div>