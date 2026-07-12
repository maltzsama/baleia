<script lang="ts">
	let { datasource, onChangeDatasource, onToggleDark, dark }: {
		datasource: string;
		onChangeDatasource: (ds: string) => void;
		onToggleDark: () => void;
		dark: boolean;
	} = $props();
</script>

<header class="h-12 bg-bg-panel flex items-center px-4 border-b border-border gap-3 shrink-0">

	<span class="font-semibold text-sm">SQL Editor</span>

	<div class="flex items-center gap-2 bg-slate-50 dark:bg-slate-800 border border-border hover:border-border-hover rounded-md px-[10px] py-[5px] cursor-pointer text-xs font-semibold transition-all duration-150">
		<div class="w-2 h-2 rounded-full bg-success shrink-0"></div>
		<span>{datasource}</span>
		<span class="text-text-muted font-normal">· Catalog</span>
		<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M6 9l6 6 6-6"/></svg>
	</div>

	<button
		onclick={() => window.location.href = '/setup'}
		class="flex items-center gap-1.5 h-8 px-[10px] rounded-md border border-dashed border-border text-xs font-semibold text-text-muted bg-transparent cursor-pointer transition-all duration-150 hover:border-primary hover:text-primary">
		<svg viewBox="0 0 24 24" class="w-3 h-3"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
		Add source
	</button>

	<div class="flex-1"></div>

	<div class="flex items-center gap-1.5 text-[11px] text-text-muted">
		<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
		<div
			role="switch"
			onclick={onToggleDark}
			class="w-8 h-[18px] rounded-full cursor-pointer relative transition-colors duration-200 shrink-0
				{dark ? 'bg-primary' : 'bg-border'}">
			<span class="absolute w-[14px] h-[14px] bg-white rounded-full top-[2px] left-[2px] shadow-sm transition-transform duration-200
				{dark ? 'translate-x-[14px]' : 'translate-x-0'}">
			</span>
		</div>
	</div>

</header>