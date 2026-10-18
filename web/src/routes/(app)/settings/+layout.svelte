<script lang="ts">
	import { page } from '$app/stores';
	import Forbidden from '$lib/components/Forbidden.svelte';
	import SettingsNav from '$lib/components/SettingsNav.svelte';
	import { canAccessSection, sectionForPath } from '$lib/settings/sections';
	import { authStore } from '$lib/stores/auth.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';

	let { children } = $props();

	const section = $derived(sectionForPath($page.url.pathname));
	const allowed = $derived(canAccessSection(section, authStore.user));
</script>

<div class="flex-1 flex overflow-hidden">
	<SettingsNav current={$page.url.pathname} />
	<div class="flex-1 flex flex-col overflow-hidden">
		{#if authStore.loading}
			<div class="p-7 flex flex-col gap-3">
				<Skeleton height="1.5rem" width="30%" />
				<Skeleton height="1rem" />
				<Skeleton height="1rem" width="80%" />
			</div>
		{:else if allowed}
			{@render children()}
		{:else}
			<Forbidden />
		{/if}
	</div>
</div>
