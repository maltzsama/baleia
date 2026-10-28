<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { browser } from '$app/environment';
	import PanelResizer from '$lib/ui/PanelResizer.svelte';
	import { loadWidth } from '$lib/ui/panelWidth';
	import { page } from '$app/stores';
	import Forbidden from '$lib/components/Forbidden.svelte';
	import SettingsNav from '$lib/components/SettingsNav.svelte';
	import { canAccessSection, sectionForPath } from '$lib/settings/sections';
	import { authStore } from '$lib/stores/auth.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';

	let { children } = $props();
	// The section list can be widened (#468).
	const NAV = { min: 180, max: 360, initial: 220 };
	let navWidth = $state(browser ? loadWidth('settings-nav', NAV) : NAV.initial);

	const section = $derived(sectionForPath($page.url.pathname));
	const allowed = $derived(canAccessSection(section, authStore.user));
</script>

<div class="flex-1 flex overflow-hidden">
	<SettingsNav current={$page.url.pathname} width={navWidth} />
	<PanelResizer
		bind:value={navWidth}
		bounds={NAV}
		side="left"
		storageKey="settings-nav"
		label={m.ui_panelresizer_resize_settings_nav()}
	/>
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
