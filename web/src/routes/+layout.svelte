<script lang="ts">
	let { children } = $props();
</script>

{@render children()}

<style>
	:root {
		--bg-app: #f0f2f5;
		--bg-panel: #ffffff;
		--bg-toolbar: #fafafa;
		--border-light: #e2e8f0;
		--border-hover: #cbd5e1;
		--text-main: #1e293b;
		--text-muted: #64748b;
		--primary: #3b82f6;
		--primary-hover: #2563eb;
		--success: #10b981;
		--success-hover: #059669;
		--brand-dark: #0f172a;
		--shadow-sm: 0 1px 2px 0 rgb(0 0 0 / 0.05);
		--radius: 8px;
	}
	body.dark {
		--bg-app: #0d1117;
		--bg-panel: #161b22;
		--bg-toolbar: #1c2128;
		--border-light: #30363d;
		--border-hover: #484f58;
		--text-main: #e6edf3;
		--text-muted: #7d8590;
	}
	*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
	body { background: var(--bg-app); color: var(--text-main); height: 100vh; overflow: hidden; font-family: 'IBM Plex Sans', -apple-system, sans-serif; }
	.mono, code, pre, .type-chip, .inspector-breadcrumb, .inspector-entity-name, .col-name, .stat-cell-val, .ds-dot { font-family: 'JetBrains Mono', monospace; }
	svg { width: 14px; height: 14px; stroke: currentColor; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; fill: none; flex-shrink: 0; }
</style>
