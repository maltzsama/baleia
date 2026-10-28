<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import GlobalNav from '$lib/components/GlobalNav.svelte';
	import Header from '$lib/components/Header.svelte';
	import SessionExpired from '$lib/components/SessionExpired.svelte';
	import Toaster from '$lib/ui/Toaster.svelte';
	import { browser } from '$app/environment';
	import { onMount } from 'svelte';
	import { configureAuth } from '$lib/api/client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';

	let { children } = $props();
	let dark = $state(browser && localStorage.getItem('theme') === 'dark');
	let expired = $state(false);

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
</script>

<!-- Skips the repeated navigation (#466); visible when focused. -->
<a
	href="#main-content"
	class="sr-only z-[200] rounded-md bg-primary px-3 py-2 text-[13px] font-semibold text-white focus:not-sr-only focus:fixed focus:left-2 focus:top-2"
>
	{m.layout_skip_to_content()}
</a>
<div class="h-screen flex overflow-hidden">
	<GlobalNav />
	<div class="flex-1 flex flex-col overflow-hidden">
		<Header onToggleDark={() => (dark = !dark)} {dark} />
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

<SessionExpired bind:open={expired} onsignin={signInAgain} />
<Toaster />
