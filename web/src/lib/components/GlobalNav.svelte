<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { locate, navAreas, type AreaKey } from '$lib/nav/sections';
	import { authStore } from '$lib/stores/auth.svelte';

	let menuOpen = $state(false);

	// One rail entry per area (#464); the area's pages are tabs in the header.
	const current = $derived(locate(page.url.pathname)?.area.key ?? null);
	const mainAreas = navAreas.filter((area) => area.key !== 'settings');
	const settingsArea = navAreas.find((area) => area.key === 'settings')!;

	// Icon paths per area (24×24, stroked like the rest of the shell).
	const icons: Record<AreaKey, string> = {
		sql: 'M4 17l6-6-6-6M12 19h8',
		data: 'M3 5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2zM3 9h18M9 21V9',
		explore: 'M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14zM21 21l-4.3-4.3M8 11h6M11 8v6',
		content: 'M4 5h7l2 2h7v12H4zM4 9h16',
		connections:
			'M3 5c0-1.66 4-3 9-3s9 1.34 9 3-4 3-9 3-9-1.34-9-3zM21 12c0 1.66-4 3-9 3s-9-1.34-9-3M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5',
		monitoring: 'M18 20V10M12 20V4M6 20v-6',
		settings:
			'M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM19.07 4.93A10 10 0 0 0 4.93 19.07M4.93 4.93a10 10 0 0 0 14.14 14.14'
	};

	function toggleMenu() {
		menuOpen = !menuOpen;
	}

	function handleClickOutside(e: MouseEvent) {
		const target = e.target as HTMLElement;
		if (!target.closest('[data-avatar-menu]')) {
			menuOpen = false;
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') menuOpen = false;
	}

	$effect(() => {
		if (menuOpen) {
			document.addEventListener('click', handleClickOutside);
			document.addEventListener('keydown', handleKeydown);
			return () => {
				document.removeEventListener('click', handleClickOutside);
				document.removeEventListener('keydown', handleKeydown);
			};
		}
	});
</script>

{#snippet entry(key: AreaKey, href: string, label: string)}
	<a
		href={resolve(href as '/')}
		aria-current={current === key ? 'page' : undefined}
		class="flex w-[88px] flex-col items-center gap-1 rounded-[10px] px-1 py-1.5 text-[10px] font-medium leading-none no-underline transition-colors duration-150
			{current === key ? 'bg-primary text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
	>
		<svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true"><path d={icons[key]} /></svg>
		<span class="max-w-full truncate">{label}</span>
	</a>
{/snippet}

<nav
	aria-label={m.nav_primary()}
	class="z-10 flex w-[96px] shrink-0 flex-col items-center justify-between bg-brand-dark py-3"
>
	<div class="flex flex-col items-center gap-1">
		<a href={resolve('/')} title={m.common_baleia()} class="mb-3 flex text-white">
			<svg viewBox="0 0 24 24" class="h-[22px] w-[22px]" aria-hidden="true"
				><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" /></svg
			>
			<span class="sr-only">{m.common_baleia()}</span>
		</a>
		{#each mainAreas as area (area.key)}
			{@render entry(area.key, area.href, area.label())}
		{/each}
	</div>

	<div class="flex flex-col items-center gap-2">
		{@render entry(settingsArea.key, settingsArea.href, settingsArea.label())}
		<div data-avatar-menu class="relative">
			<button
				onclick={toggleMenu}
				title={authStore.user?.name ?? ''}
				class="w-[30px] h-[30px] rounded-full bg-slate-700 flex items-center justify-center text-[11px] font-bold text-slate-200 no-underline border-none cursor-pointer"
			>
				{authStore.initials}
			</button>
			{#if menuOpen}
				<div
					class="absolute bottom-full left-0 mb-2 w-36 bg-slate-800 border border-slate-700 rounded-lg shadow-xl overflow-hidden z-50"
				>
					<a
						href={resolve('/settings/profile')}
						onclick={() => (menuOpen = false)}
						class="flex items-center gap-2.5 px-3.5 py-2.5 text-[13px] text-slate-200 no-underline transition-colors duration-150 hover:bg-slate-700"
					>
						<svg viewBox="0 0 24 24" class="w-4 h-4"
							><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" /><circle
								cx="12"
								cy="7"
								r="4"
							/></svg
						>
						{m.components_globalnav_profile()}
					</a>
					<button
						onclick={authStore.logout}
						class="flex items-center gap-2.5 w-full px-3.5 py-2.5 text-[13px] text-slate-200 transition-colors duration-150 hover:bg-slate-700 border-none bg-transparent cursor-pointer text-left"
					>
						<svg viewBox="0 0 24 24" class="w-4 h-4"
							><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" /></svg
						>
						{m.components_globalnav_logout()}
					</button>
				</div>
			{/if}
		</div>
	</div>
</nav>
