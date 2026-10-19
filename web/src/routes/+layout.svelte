<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { configureSession } from '$lib/auth/session';
	import { initLocale } from '$lib/i18n';
	import { authStore } from '$lib/stores/auth.svelte';

	let { children } = $props();

	onMount(() => {
		// A logout in another tab clears this tab too (#263).
		configureSession({
			onLogout: () => {
				authStore.clear();
				if (!window.location.pathname.startsWith('/login')) window.location.href = '/login';
			}
		});

		// Locale: stored choice → browser (#67). The app layout refines it with
		// the user's saved preference once /auth/me resolves.
		initLocale();

		// Apply the persisted theme on every route so it survives navigation.
		const dark = localStorage.getItem('theme') === 'dark';
		document.body.classList.toggle('dark', dark);
	});
</script>

{@render children()}
