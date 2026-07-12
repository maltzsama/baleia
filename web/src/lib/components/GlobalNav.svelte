<script lang="ts">
	import { page } from '$app/stores';
	import { authStore } from '$lib/stores/auth.svelte';

	let { dark, onToggleDark }: { dark: boolean; onToggleDark: () => void } = $props();

	function isActive(path: string): boolean {
		if (path === '/') return $page.url.pathname === '/';
		return $page.url.pathname.startsWith(path);
	}
</script>

<nav class="w-[52px] bg-brand-dark flex flex-col items-center py-3 justify-between z-10 shrink-0">
	<div class="flex flex-col items-center gap-1">
		<a href="/" title="Baleia" class="text-white mb-4 flex">
			<svg viewBox="0 0 24 24" class="w-[22px] h-[22px]"><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/></svg>
		</a>
		<a href="/"
			title="SQL Editor"
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/') ? 'bg-primary text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-white'}">
			<svg viewBox="0 0 24 24" class="w-4 h-4"><path d="M4 17l6-6-6-6M12 19h8"/></svg>
		</a>
		<a href="/datasources"
			title="Data Sources"
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/datasources') ? 'bg-primary text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-white'}">
			<svg viewBox="0 0 24 24" class="w-4 h-4"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="9" y1="21" x2="9" y2="9"/></svg>
		</a>
		<a href="/lens"
			title="Monitoring"
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/lens') ? 'bg-primary text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-white'}">
			<svg viewBox="0 0 24 24" class="w-4 h-4"><line x1="18" y1="20" x2="18" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/><line x1="6" y1="20" x2="6" y2="14"/></svg>
		</a>
		<a href="/settings"
			title="Settings"
			class="w-9 h-9 flex items-center justify-center rounded-[10px] no-underline transition-all duration-150
				{isActive('/settings') ? 'bg-primary text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-white'}">
			<svg viewBox="0 0 24 24" class="w-4 h-4"><circle cx="12" cy="12" r="3"/><path d="M19.07 4.93A10 10 0 0 0 4.93 19.07M4.93 4.93a10 10 0 0 0 14.14 14.14"/></svg>
		</a>
	</div>

	<a href="/profile"
		title={authStore.user?.name ?? ''}
		class="w-[30px] h-[30px] rounded-full bg-slate-700 flex items-center justify-center text-[11px] font-bold text-slate-200 no-underline">
		{authStore.initials}
	</a>
</nav>