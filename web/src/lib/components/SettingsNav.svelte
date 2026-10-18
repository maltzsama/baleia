<script lang="ts">
	import { resolve } from '$app/paths';
	import { authStore } from '$lib/stores/auth.svelte';
	import { visibleSections, type SettingsSection } from '$lib/settings/sections';

	let { current = '', sections: provided }: { current?: string; sections?: SettingsSection[] } =
		$props();

	const sections = $derived(provided ?? visibleSections(authStore.user));

	function isActive(href: string): boolean {
		return current === href || current.startsWith(`${href}/`);
	}
</script>

<nav
	aria-label="Settings"
	class="w-[220px] shrink-0 border-r border-border bg-bg-panel overflow-y-auto p-4"
>
	<p class="mb-3 text-[10px] font-semibold uppercase tracking-wide text-text-muted">Settings</p>
	<ul class="flex flex-col gap-1">
		{#each sections as section (section.href)}
			<li>
				<a
					href={resolve(section.href as '/settings/audit')}
					aria-current={isActive(section.href) ? 'page' : undefined}
					class="block rounded-md px-3 py-2 text-[13px] no-underline transition-colors
						{isActive(section.href)
						? 'bg-primary/10 font-semibold text-primary'
						: 'text-text-main hover:bg-slate-100 dark:hover:bg-slate-800'}"
				>
					{section.label}
					<span class="block text-[11px] font-normal text-text-muted">{section.description}</span>
				</a>
			</li>
		{/each}
	</ul>
</nav>
