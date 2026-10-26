<script lang="ts">
	import '../app.css';
	// Query-editor code font (Sometype Mono), self-hosted via Fontsource.
	// Only the query editor uses it; everything else stays on --font-mono.
	import '@fontsource/sometype-mono/400.css';
	import '@fontsource/sometype-mono/500.css';
	import '@fontsource/sometype-mono/600.css';
	import '@fontsource/sometype-mono/700.css';
	import { onMount } from 'svelte';
	import { configureSession } from '$lib/auth/session';
	import { initLocale } from '$lib/i18n/locale.svelte';
	import { authStore } from '$lib/stores/auth.svelte';

	let { children } = $props();

	onMount(() => {
		// Keep <html lang> in sync with the resolved locale (#67).
		initLocale();
		// A logout in another tab clears this tab too (#263).
		configureSession({
			onLogout: () => {
				authStore.clear();
				if (!window.location.pathname.startsWith('/login')) window.location.href = '/login';
			}
		});

		// Apply the persisted theme on every route so it survives navigation.
		const dark = localStorage.getItem('theme') === 'dark';
		document.body.classList.toggle('dark', dark);
	});
</script>

{@render children()}
