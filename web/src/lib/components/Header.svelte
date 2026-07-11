<script lang="ts">
	let { datasource, onChangeDatasource, onToggleDark, dark }: {
		datasource: string;
		onChangeDatasource: (ds: string) => void;
		onToggleDark: () => void;
		dark: boolean;
	} = $props();
</script>

<header>
	<span class="breadcrumb">SQL Editor</span>

	<div class="datasource-switcher">
		<div class="ds-dot"></div>
		<span>{datasource}</span>
		<span class="ds-type">· Catalog</span>
		<svg viewBox="0 0 24 24"><path d="M6 9l6 6 6-6"/></svg>
	</div>

	<button class="add-source-btn" onclick={() => window.location.href = '/setup'}>
		<svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
		Add source
	</button>

	<div class="spacer"></div>

	<div class="dark-toggle-wrap">
		<svg viewBox="0 0 24 24" class="dark-icon"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
		<div class="dark-toggle" class:on={dark} onclick={onToggleDark} role="switch"></div>
	</div>
</header>

<style>
	header {
		height: 48px; background: var(--bg-panel);
		display: flex; align-items: center;
		padding: 0 16px;
		border-bottom: 1px solid var(--border-light);
		gap: 12px; flex-shrink: 0;
	}
	.breadcrumb { font-weight: 600; font-size: 14px; }
	.datasource-switcher {
		display: flex; align-items: center; gap: 8px;
		background: #f8fafc; border: 1px solid var(--border-light);
		border-radius: 6px; padding: 5px 10px;
		cursor: pointer; font-size: 12px; font-weight: 600;
		transition: 0.15s;
	}
	.dark .datasource-switcher { background: #1c2128; }
	.datasource-switcher:hover { border-color: var(--border-hover); }
	.ds-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--success); flex-shrink: 0; }
	.ds-type { color: var(--text-muted); font-weight: 400; }
	.add-source-btn {
		display: flex; align-items: center; gap: 6px;
		height: 32px; padding: 0 10px;
		border-radius: 6px; border: 1px dashed var(--border-light);
		background: transparent; font-size: 12px; font-weight: 600;
		color: var(--text-muted); cursor: pointer; transition: .15s;
	}
	.add-source-btn:hover { border-color: var(--primary); color: var(--primary); }
	.add-source-btn svg { width: 12px; height: 12px; }
	.spacer { flex: 1; }
	.dark-toggle-wrap { display: flex; align-items: center; gap: 6px; font-size: 11px; color: var(--text-muted); }
	.dark-icon { width: 13px; height: 13px; }
	.dark-toggle {
		width: 32px; height: 18px; background: var(--border-light);
		border-radius: 9px; cursor: pointer; position: relative;
		transition: background 0.2s; flex-shrink: 0;
	}
	.dark-toggle.on { background: var(--primary); }
	.dark-toggle::after {
		content: ''; position: absolute; width: 14px; height: 14px;
		background: white; border-radius: 50%; top: 2px; left: 2px;
		transition: transform 0.2s; box-shadow: 0 1px 3px rgba(0,0,0,0.2);
	}
	.dark-toggle.on::after { transform: translateX(14px); }
</style>
