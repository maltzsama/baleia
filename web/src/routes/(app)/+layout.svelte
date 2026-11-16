<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import GlobalNav from '$lib/components/GlobalNav.svelte';
	import Header from '$lib/components/Header.svelte';
	import SessionExpired from '$lib/components/SessionExpired.svelte';
	import GlobalSearch from '$lib/search/GlobalSearch.svelte';
	import Toaster from '$lib/ui/Toaster.svelte';
	import { browser } from '$app/environment';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { configureAuth } from '$lib/api/client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { catalogAccessStore } from '$lib/stores/catalogAccess.svelte';

	let { children } = $props();
	let dark = $state(browser && localStorage.getItem('theme') === 'dark');
	let expired = $state(false);
	let searchOpen = $state(false);

	function signInAgain() {
		const next = `${window.location.pathname}${window.location.search}`;
		window.location.href = `/login?next=${encodeURIComponent(next)}`;
	}

	$effect(() => {
		if (!browser) return;
		document.body.classList.toggle('dark', dark);
		localStorage.setItem('theme', dark ? 'dark' : 'light');
	});

	onMount(async () => {
		// A failed refresh opens the modal; drafts survive because they are
		// persisted per user and workspace (#112, #290).
		configureAuth({ onSessionExpired: () => (expired = true) });
		authStore.load();
		await workspaceStore.load();
	});

	// The global search shortcut (#1061): Mod-K outside the Workbench, which
	// keeps Mod-K for its command palette. A focused text field keeps the key.
	onMount(() => {
		function onKey(event: KeyboardEvent) {
			if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'k') return;
			if (page.url.pathname.startsWith('/sql')) return;
			const target = event.target as HTMLElement | null;
			if (target?.closest('input, textarea, select, [contenteditable="true"], .cm-editor')) return;
			event.preventDefault();
			searchOpen = true;
		}
		window.addEventListener('keydown', onKey);
		return () => window.removeEventListener('keydown', onKey);
	});
</script>

<!-- A grant made in another tab or screen is picked up when this window is used
     again (#975). -->
<svelte:window onfocus={() => catalogAccessStore.focused()} />
<svelte:document
	onvisibilitychange={() => {
		if (document.visibilityState === 'visible') catalogAccessStore.focused();
	}}
/>

<!-- Skips the repeated navigation (#466); visible when focused. -->
<a
	href="#main-content"
	class="sr-only z-[200] rounded-md bg-primary-solid px-3 py-2 text-[13px] font-semibold text-white focus:not-sr-only focus:fixed focus:left-2 focus:top-2"
>
	{m.layout_skip_to_content()}
</a>
<div class="h-screen flex overflow-hidden">
	<GlobalNav />
	<div class="flex-1 flex flex-col overflow-hidden">
		<Header onToggleDark={() => (dark = !dark)} {dark} onsearch={() => (searchOpen = true)} />
		<!-- The one main landmark of every app page. -->
		<main
			id="main-content"
			tabindex="-1"
			class="flex min-h-0 flex-1 flex-col overflow-hidden outline-none"
		>
			{@render children()}
		</main>
	</div>
</div>

<GlobalSearch
	bind:open={searchOpen}
	workspaceId={workspaceStore.current?.id ?? ''}
	workspaceName={workspaceStore.current?.name}
/>
<SessionExpired bind:open={expired} onsignin={signInAgain} />
<Toaster />
