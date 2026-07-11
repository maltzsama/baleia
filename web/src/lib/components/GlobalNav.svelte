<script lang="ts">
	let { dark, onToggleDark }: { dark: boolean; onToggleDark: () => void } = $props();
</script>

<nav class="global-nav">
	<div class="nav-top">
		<div class="nav-logo">
			<svg viewBox="0 0 24 24"><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/></svg>
		</div>
		<div class="nav-item active" title="SQL Editor">
			<svg viewBox="0 0 24 24"><path d="M4 17l6-6-6-6M12 19h8"/></svg>
		</div>
		<div class="nav-item" title="Data">
			<svg viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="9" y1="21" x2="9" y2="9"/></svg>
		</div>
		<div class="nav-item" title="Monitoring">
			<svg viewBox="0 0 24 24"><line x1="18" y1="20" x2="18" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/><line x1="6" y1="20" x2="6" y2="14"/></svg>
		</div>
		<div class="nav-item" title="Settings">
			<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.07 4.93A10 10 0 0 0 4.93 19.07M4.93 4.93a10 10 0 0 0 14.14 14.14"/></svg>
		</div>
	</div>
	<div class="nav-avatar">DM</div>
</nav>

<style>
	.global-nav {
		width: 52px; background: var(--brand-dark);
		display: flex; flex-direction: column;
		align-items: center; padding: 12px 0;
		justify-content: space-between;
		z-index: 10; flex-shrink: 0;
	}
	.nav-top { display: flex; flex-direction: column; align-items: center; }
	.nav-logo { color: white; margin-bottom: 20px; }
	.nav-logo svg { width: 22px; height: 22px; }
	.nav-item {
		width: 36px; height: 36px;
		display: flex; align-items: center; justify-content: center;
		border-radius: 10px; color: #94a3b8;
		cursor: pointer; transition: 0.15s; margin-bottom: 4px;
	}
	.nav-item:hover { background: #1e293b; color: #fff; }
	.nav-item.active { background: var(--primary); color: #fff; }
	.nav-item svg { width: 16px; height: 16px; }
	.nav-avatar {
		width: 30px; height: 30px; border-radius: 50%;
		background: #334155; display: flex;
		align-items: center; justify-content: center;
		font-size: 11px; font-weight: 700; color: #e2e8f0;
		cursor: pointer;
	}
</style>
