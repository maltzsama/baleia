<script lang="ts">
	import { page } from '$app/stores';
	import { resolve } from '$app/paths';
	import AttentionBell from '$lib/components/AttentionBell.svelte';
	import { t } from '$lib/i18n';
	import { authStore } from '$lib/stores/auth.svelte';

	let menuOpen = $state(false);

	function isActive(path: string): boolean {
		if (path === '/') return $page.url.pathname === '/';
		return $page.url.pathname.startsWith(path);
	}

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

<nav class="w-[52px] bg-brand-dark flex flex-col items-center py-3 justify-between z-10 shrink-0">
	<div class="flex flex-col items-center gap-1">
		<a href={resolve('/')} title="Baleia" class="text-white mb-4 flex">
			<svg viewBox="0 0 24 24" class="w-[22px] h-[22px]"
				><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" /></svg
			>
		</a>
		<a
			href={resolve('/sql')}
			title={t('nav_sql')}
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/sql') ? 'bg-primary text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"><path d="M4 17l6-6-6-6M12 19h8" /></svg>
		</a>
		<a
			href={resolve('/data')}
			title={t('nav_data')}
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/data')
				? 'bg-primary text-white'
				: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"
				><rect x="3" y="3" width="18" height="18" rx="2" /><line
					x1="3"
					y1="9"
					x2="21"
					y2="9"
				/><line x1="9" y1="21" x2="9" y2="9" /></svg
			>
		</a>
		<a
			href={resolve('/connections')}
			title={t('nav_connections')}
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/connections')
				? 'bg-primary text-white'
				: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"
				><ellipse cx="12" cy="5" rx="9" ry="3" /><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3" /><path
					d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"
				/></svg
			>
		</a>
		<a
			href={resolve('/content')}
			title="Content"
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/content')
				? 'bg-primary text-white'
				: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"
				><path d="M4 5h7l2 2h7v12H4z" /><path d="M4 9h16" /></svg
			>
		</a>
		<a
			href={resolve('/metrics')}
			title="Metrics"
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/metrics')
				? 'bg-primary text-white'
				: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"
				><path d="M4 19V5M4 19h16" /><path d="M7 15l4-5 3 3 5-7" /></svg
			>
		</a>
		<a
			href={resolve('/data/docs')}
			title="Docs"
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/data/docs')
				? 'bg-primary text-white'
				: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"
				><path d="M5 4h11l3 3v13H5z" /><path d="M8 10h8M8 14h6" /></svg
			>
		</a>
		<a
			href={resolve('/engines')}
			title="Engines"
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/engines')
				? 'bg-primary text-white'
				: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"
				><rect x="3" y="4" width="18" height="6" rx="1.5" /><rect
					x="3"
					y="14"
					width="18"
					height="6"
					rx="1.5"
				/><circle cx="7" cy="7" r=".8" /><circle cx="7" cy="17" r=".8" /></svg
			>
		</a>
		<a
			href={resolve('/catalogs')}
			title="Catalogs"
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/catalogs')
				? 'bg-primary text-white'
				: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"
				><path d="M4 6h16M4 12h16M4 18h16" /><circle cx="3" cy="6" r=".6" /><circle
					cx="3"
					cy="12"
					r=".6"
				/><circle cx="3" cy="18" r=".6" /></svg
			>
		</a>
		<a
			href={resolve('/monitoring')}
			title={t('nav_monitoring')}
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/monitoring')
				? 'bg-primary text-white'
				: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"
				><line x1="18" y1="20" x2="18" y2="10" /><line x1="12" y1="20" x2="12" y2="4" /><line
					x1="6"
					y1="20"
					x2="6"
					y2="14"
				/></svg
			>
		</a>
		{#if authStore.user?.features?.retrievers}
			<a
				href={resolve('/retrievers')}
				title={t('nav_retrievers')}
				class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
					{isActive('/retrievers')
					? 'bg-primary text-white'
					: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
			>
				<svg viewBox="0 0 24 24" class="w-4 h-4"
					><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg
				>
			</a>
		{/if}
		<a
			href={resolve('/history')}
			title={t('nav_history')}
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/history')
				? 'bg-primary text-white'
				: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"
				><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg
			>
		</a>
		<AttentionBell />
		<a
			href={resolve('/settings')}
			title={t('nav_settings')}
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/settings')
				? 'bg-primary text-white'
				: 'text-slate-400 hover:bg-slate-800 hover:text-white'}"
		>
			<svg viewBox="0 0 24 24" class="w-4 h-4"
				><circle cx="12" cy="12" r="3" /><path
					d="M19.07 4.93A10 10 0 0 0 4.93 19.07M4.93 4.93a10 10 0 0 0 14.14 14.14"
				/></svg
			>
		</a>
	</div>

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
					{t('nav_profile')}
				</a>
				<button
					onclick={authStore.logout}
					class="flex items-center gap-2.5 w-full px-3.5 py-2.5 text-[13px] text-slate-200 transition-colors duration-150 hover:bg-slate-700 border-none bg-transparent cursor-pointer text-left"
				>
					<svg viewBox="0 0 24 24" class="w-4 h-4"
						><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" /></svg
					>
					{t('nav_signout')}
				</button>
			</div>
		{/if}
	</div>
</nav>
