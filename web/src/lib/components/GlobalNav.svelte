<script lang="ts">
	import ChartNoAxesColumnIcon from '@lucide/svelte/icons/chart-no-axes-column';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import FolderIcon from '@lucide/svelte/icons/folder';
	import KeyIcon from '@lucide/svelte/icons/key';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import Table2Icon from '@lucide/svelte/icons/table-2';
	import TerminalIcon from '@lucide/svelte/icons/terminal';
	import UserIcon from '@lucide/svelte/icons/user';
	import ZoomInIcon from '@lucide/svelte/icons/zoom-in';
	import { m } from '$lib/paraglide/messages';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { locate, railAreas, type AreaKey } from '$lib/nav/sections';
	import { visibleSections } from '$lib/settings/sections';
	import { authStore } from '$lib/stores/auth.svelte';
	import Logo from '$lib/components/Logo.svelte';

	let menuOpen = $state(false);

	// One icon per area (#464): its name is the tooltip and the accessible
	// name, and its pages are tabs in the header. Settings is administration,
	// shown only to someone who can open one of its sections.
	const current = $derived(locate(page.url.pathname)?.area.key ?? null);
	const mainAreas = railAreas.filter((area) => area.key !== 'settings');
	const settingsArea = railAreas.find((area) => area.key === 'settings')!;
	const showSettings = $derived(visibleSections(authStore.user).length > 0);

	// One Lucide icon per area (24×24, stroked like the rest of the shell).
	const icons: Record<AreaKey, typeof TerminalIcon> = {
		sql: TerminalIcon,
		data: Table2Icon,
		explore: ZoomInIcon,
		content: FolderIcon,
		connections: DatabaseIcon,
		monitoring: ChartNoAxesColumnIcon,
		settings: SettingsIcon,
		profile: UserIcon
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
	{@const Glyph = icons[key]}
	<a
		href={resolve(href as '/')}
		title={label}
		aria-current={current === key ? 'page' : undefined}
		class="flex h-10 w-10 items-center justify-center rounded-[10px] no-underline transition-colors duration-150
			{current === key ? 'bg-primary text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
	>
		<Glyph class="h-[18px] w-[18px]" aria-hidden="true" />
		<span class="sr-only">{label}</span>
	</a>
{/snippet}

<nav
	aria-label={m.nav_primary()}
	class="z-10 flex w-14 shrink-0 flex-col items-center justify-between bg-brand-dark py-3"
>
	<div class="flex flex-col items-center gap-1">
		<a href={resolve('/')} title={m.common_baleia()} class="mb-3 flex text-white">
			<Logo class="h-[30px] w-[30px]" />
			<span class="sr-only">{m.common_baleia()}</span>
		</a>
		{#each mainAreas as area (area.key)}
			{@render entry(area.key, area.href, area.label())}
		{/each}
	</div>

	<div class="flex flex-col items-center gap-2">
		{#if showSettings}
			{@render entry(settingsArea.key, settingsArea.href, settingsArea.label())}
		{/if}
		<div data-avatar-menu class="relative">
			<button
				type="button"
				onclick={toggleMenu}
				title={authStore.user?.name ?? ''}
				aria-haspopup="menu"
				aria-expanded={menuOpen}
				class="w-[30px] h-[30px] rounded-full bg-slate-700 flex items-center justify-center text-[11px] font-bold text-slate-200 no-underline border-none cursor-pointer"
			>
				{authStore.initials}
			</button>
			{#if menuOpen}
				<div
					class="absolute bottom-full left-0 mb-2 w-40 bg-slate-800 border border-slate-700 rounded-lg shadow-xl overflow-hidden z-50"
				>
					{#each [{ href: '/profile', label: m.nav_profile(), icon: icons.profile }, { href: '/profile/api-keys', label: m.nav_page_api_keys(), icon: KeyIcon }] as item (item.href)}
						{@const ItemIcon = item.icon}
						<a
							href={resolve(item.href as '/profile')}
							onclick={() => (menuOpen = false)}
							class="flex items-center gap-2.5 px-3.5 py-2.5 text-[13px] text-slate-200 no-underline transition-colors duration-150 hover:bg-slate-700"
						>
							<ItemIcon class="w-4 h-4" aria-hidden="true" />
							{item.label}
						</a>
					{/each}
					<button
						onclick={authStore.logout}
						class="flex items-center gap-2.5 w-full px-3.5 py-2.5 text-[13px] text-slate-200 transition-colors duration-150 hover:bg-slate-700 border-none bg-transparent cursor-pointer text-left"
					>
						<LogOutIcon class="w-4 h-4" />
						{m.components_globalnav_logout()}
					</button>
				</div>
			{/if}
		</div>
	</div>
</nav>
