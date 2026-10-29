<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { resolve } from '$app/paths';
	import { authStore } from '$lib/stores/auth.svelte';
	import { settingsGroups, visibleSections, type SettingsSection } from '$lib/settings/sections';

	let {
		current = '',
		sections: provided,
		width = 200
	}: { current?: string; sections?: SettingsSection[]; width?: number } = $props();

	const sections = $derived(provided ?? visibleSections(authStore.user));
	// Only the groups the user can open a section of.
	const groups = $derived(
		settingsGroups
			.map((group) => ({ ...group, sections: sections.filter((s) => s.group === group.key) }))
			.filter((group) => group.sections.length > 0)
	);

	function isActive(href: string): boolean {
		return current === href || current.startsWith(`${href}/`);
	}
</script>

<nav
	aria-label={m.common_settings()}
	class="shrink-0 overflow-y-auto border-r border-border bg-bg-panel px-3 py-4"
	style:width="{width}px"
>
	{#each groups as group (group.key)}
		<p
			class="mb-1 mt-4 px-2 text-[10px] font-semibold uppercase tracking-wide text-text-muted first:mt-0"
		>
			{group.label()}
		</p>
		<ul class="flex flex-col gap-0.5">
			{#each group.sections as section (section.href)}
				<li>
					<a
						href={resolve(section.href as '/settings/audit')}
						aria-current={isActive(section.href) ? 'page' : undefined}
						class="block rounded-md px-2 py-1.5 text-[13px] no-underline transition-colors
							{isActive(section.href)
							? 'bg-primary/10 font-semibold text-primary-hover dark:text-blue-300'
							: 'text-text-main hover:bg-slate-100 dark:hover:bg-slate-800'}"
					>
						{section.label}
					</a>
				</li>
			{/each}
		</ul>
	{/each}
</nav>
