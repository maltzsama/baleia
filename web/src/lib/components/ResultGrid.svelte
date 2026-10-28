<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { toasts } from '$lib/ui/toast.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import { cellText, formatCell, fullJson, type ColumnMeta } from '$lib/results/format';
	import { RESULT_PAGE_SIZE, type ResultsStore } from '$lib/results/store.svelte';
	import type { Snippet } from 'svelte';

	// `actions` sits at the end of the toolbar (export), so the results area
	// has one bar (#393).
	let { store, actions }: { store: ResultsStore; actions?: Snippet } = $props();

	const ROW_HEIGHT = 28;
	const HEADER_HEIGHT = 32;
	const DEFAULT_WIDTH = 160;
	const OVERSCAN = 6;

	let scrollEl: HTMLDivElement | undefined = $state();
	let scrollTop = $state(0);
	let scrollLeft = $state(0);
	let viewportWidth = $state(0);
	let viewportHeight = $state(0);

	let widths = $state<Record<string, number>>({});
	let selected = $state<{ row: number; col: number } | null>(null);
	let expanded = $state<{ value: unknown; column: ColumnMeta } | null>(null);
	let retryTick = $state(0);

	type Layout = { column: ColumnMeta; index: number; start: number; width: number };

	const columnLayouts = $derived.by<Layout[]>(() => {
		let x = 0;
		return store.columns.map((column, index) => {
			const width = widths[column.name] ?? DEFAULT_WIDTH;
			const layout = { column, index, start: x, width };
			x += width;
			return layout;
		});
	});

	const totalWidth = $derived(columnLayouts.reduce((sum, layout) => sum + layout.width, 0));
	const totalHeight = $derived(HEADER_HEIGHT + store.rowCount * ROW_HEIGHT);

	const visibleColumns = $derived.by(() => {
		const left = scrollLeft - DEFAULT_WIDTH;
		const right = scrollLeft + viewportWidth + DEFAULT_WIDTH;
		return columnLayouts.filter(
			(layout) => layout.start + layout.width >= left && layout.start <= right
		);
	});

	const visibleRows = $derived.by(() => {
		const first = Math.max(0, Math.floor((scrollTop - HEADER_HEIGHT) / ROW_HEIGHT) - OVERSCAN);
		const count = Math.ceil(viewportHeight / ROW_HEIGHT) + OVERSCAN * 2;
		const last = Math.min(store.rowCount, first + Math.max(count, 1));
		return Array.from({ length: Math.max(0, last - first) }, (_, i) => first + i);
	});

	// Fetch the pages that cover the visible rows. Rows whose page has not
	// arrived yet render blank until it lands (#238).
	$effect(() => {
		const rows = visibleRows;
		void retryTick;
		if (rows.length === 0 || store.rowCount === 0) return;
		const firstPage = Math.floor(rows[0] / RESULT_PAGE_SIZE);
		const lastPage = Math.floor(rows[rows.length - 1] / RESULT_PAGE_SIZE);
		for (let page = firstPage; page <= lastPage; page++) {
			void store.ensurePage(page).catch(() => {});
		}
	});

	function onScroll() {
		if (!scrollEl) return;
		scrollTop = scrollEl.scrollTop;
		scrollLeft = scrollEl.scrollLeft;
	}

	function select(row: number, col: number) {
		selected = { row, col };
	}

	function handleKey(event: KeyboardEvent, row: number, col: number) {
		const keys: Record<string, [number, number]> = {
			ArrowDown: [1, 0],
			ArrowUp: [-1, 0],
			ArrowRight: [0, 1],
			ArrowLeft: [0, -1]
		};
		const delta = keys[event.key];
		if (delta) {
			event.preventDefault();
			selected = {
				row: Math.min(Math.max(0, row + delta[0]), Math.max(0, store.rowCount - 1)),
				col: Math.min(Math.max(0, col + delta[1]), store.columns.length - 1)
			};
			return;
		}
		if (event.key === 'Enter') {
			event.preventDefault();
			void expandAt(row, col);
		} else if (event.key === 'c' && (event.ctrlKey || event.metaKey)) {
			event.preventDefault();
			void copyCell();
		}
	}

	async function expandAt(row: number, col: number) {
		const column = store.columns[col];
		if (!column?.nested) return;
		// The selected page may have been evicted; load it before reading.
		await store.ensurePage(Math.floor(row / RESULT_PAGE_SIZE)).catch(() => {});
		expanded = { value: store.cell(row, col), column };
	}

	async function copy(text: string) {
		try {
			await navigator.clipboard.writeText(text);
			toasts.success(m.components_resultgrid_copied_to_clipboard());
		} catch {
			toasts.error(m.components_resultgrid_could_not_copy_to_the_clipboard());
		}
	}

	async function copyCell() {
		if (!selected) return;
		const column = store.columns[selected.col];
		if (!column) return;
		await store.ensurePage(Math.floor(selected.row / RESULT_PAGE_SIZE)).catch(() => {});
		void copy(cellText(store.cell(selected.row, selected.col), column));
	}

	async function copyRow() {
		if (!selected) return;
		const row = selected.row;
		await store.ensurePage(Math.floor(row / RESULT_PAGE_SIZE)).catch(() => {});
		const line = store.columns
			.map((column, index) => cellText(store.cell(row, index), column))
			.join('\t');
		void copy(line);
	}

	async function copyColumn() {
		if (!selected) return;
		const column = store.columns[selected.col];
		if (!column) return;
		// Walk the pages so evicted rows are refetched and read in order.
		const lines: string[] = [];
		const pages = Math.ceil(store.rowCount / RESULT_PAGE_SIZE);
		for (let page = 0; page < pages; page++) {
			await store.ensurePage(page).catch(() => {});
			const start = page * RESULT_PAGE_SIZE;
			const end = Math.min(start + RESULT_PAGE_SIZE, store.rowCount);
			for (let row = start; row < end; row++) {
				lines.push(cellText(store.cell(row, selected.col), column));
			}
		}
		void copy(lines.join('\n'));
	}

	function startResize(event: PointerEvent, name: string) {
		event.preventDefault();
		const startX = event.clientX;
		const startWidth = widths[name] ?? DEFAULT_WIDTH;
		const onMove = (moveEvent: PointerEvent) => {
			widths = { ...widths, [name]: Math.max(80, startWidth + moveEvent.clientX - startX) };
		};
		const onUp = () => {
			window.removeEventListener('pointermove', onMove);
			window.removeEventListener('pointerup', onUp);
		};
		window.addEventListener('pointermove', onMove);
		window.addEventListener('pointerup', onUp);
	}

	function formatBytes(bytes: number): string {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
		return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
	}
</script>

<div class="flex min-h-0 flex-1 flex-col">
	<div
		class="flex items-center gap-2 border-b border-border px-3 py-1.5 text-[11px] text-text-muted"
	>
		<!-- The run summary has the row count; here it is the download progress. -->
		{#if store.loading && !store.done}
			<span>{store.rowCount.toLocaleString()} {m.common_rows()}</span>
			<span class="flex items-center gap-1">
				<span class="h-2 w-2 animate-pulse rounded-full bg-primary"></span>
				{formatBytes(store.bytes)}
				{#if store.totalBytes > 0}
					{m.components_resultgrid_of()} {formatBytes(store.totalBytes)}
				{/if}
			</span>
		{/if}
		<span class="flex-1"></span>
		<button
			type="button"
			class="rounded px-1.5 py-0.5 hover:bg-bg-toolbar disabled:opacity-40"
			disabled={!selected}
			onclick={copyCell}>{m.components_resultgrid_copy_cell()}</button
		>
		<button
			type="button"
			class="rounded px-1.5 py-0.5 hover:bg-bg-toolbar disabled:opacity-40"
			disabled={!selected}
			onclick={copyRow}>{m.components_resultgrid_copy_row()}</button
		>
		<button
			type="button"
			class="rounded px-1.5 py-0.5 hover:bg-bg-toolbar disabled:opacity-40"
			disabled={!selected}
			onclick={copyColumn}>{m.components_resultgrid_copy_column()}</button
		>
		{#if selected && store.columns[selected.col]?.nested}
			<button
				type="button"
				class="rounded px-1.5 py-0.5 text-primary hover:bg-bg-toolbar"
				onclick={() => {
					if (selected) void expandAt(selected.row, selected.col);
				}}>{m.common_expand()}</button
			>
		{/if}
		{@render actions?.()}
	</div>

	{#if store.pageError}
		<div
			class="flex items-center gap-2 border-b border-amber-200 bg-amber-50 px-3 py-1 text-[11px] text-amber-700 dark:border-[#4d3600] dark:bg-[#241a06] dark:text-amber-300"
			role="status"
		>
			<span class="flex-1"
				>{m.components_resultgrid_could_not_load_some_rows()} {store.pageError}</span
			>
			<button type="button" class="font-semibold underline" onclick={() => retryTick++}
				>{m.common_retry()}</button
			>
		</div>
	{/if}

	<div
		bind:this={scrollEl}
		bind:clientWidth={viewportWidth}
		bind:clientHeight={viewportHeight}
		class="relative min-h-0 flex-1 overflow-auto outline-none"
		role="grid"
		tabindex="0"
		aria-label={m.components_resultgrid_result_rows()}
		onscroll={onScroll}
	>
		<div style="position: relative; width: {totalWidth}px; height: {totalHeight}px;">
			<div
				style="position: sticky; top: 0; z-index: 10; height: {HEADER_HEIGHT}px; width: {totalWidth}px; background: var(--color-bg-toolbar); border-bottom: 1px solid var(--color-border);"
				role="row"
			>
				{#each visibleColumns as layout (layout.column.name)}
					<div
						role="columnheader"
						title={`${layout.column.nativeType || layout.column.type}`}
						style="position: absolute; left: {layout.start}px; width: {layout.width}px; height: {HEADER_HEIGHT}px; display: flex; align-items: center; gap: 6px; padding: 0 10px; border-right: 1px solid var(--color-border); box-sizing: border-box;"
					>
						<span
							class="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-[11px] font-semibold"
							>{layout.column.name}</span
						>
						<span class="shrink-0 font-mono text-[9px] font-normal text-text-muted"
							>{layout.column.nativeType || layout.column.type}</span
						>
						<div
							role="separator"
							aria-orientation="vertical"
							aria-label={`Resize ${layout.column.name}`}
							class="absolute right-0 top-0 h-full w-1 cursor-col-resize hover:bg-primary"
							onpointerdown={(event) => startResize(event, layout.column.name)}
						></div>
					</div>
				{/each}
			</div>

			{#each visibleRows as row (row)}
				<div
					role="row"
					style="position: absolute; top: {HEADER_HEIGHT +
						row * ROW_HEIGHT}px; left: 0; width: {totalWidth}px; height: {ROW_HEIGHT}px;"
				>
					{#each visibleColumns as layout (layout.column.name)}
						{@const display = formatCell(store.cell(row, layout.index), layout.column)}
						<div
							role="gridcell"
							tabindex={selected?.row === row && selected?.col === layout.index ? 0 : -1}
							aria-selected={selected?.row === row && selected?.col === layout.index}
							style="position: absolute; left: {layout.start}px; width: {layout.width}px; height: {ROW_HEIGHT}px; display: flex; align-items: center; padding: 0 10px; box-sizing: border-box; border-right: 1px solid var(--color-border); border-bottom: 1px solid var(--color-border); cursor: default;"
							class="font-data text-xs {display.null
								? 'italic text-text-muted'
								: ''} {selected?.row === row && selected?.col === layout.index
								? 'bg-blue-50 dark:bg-[#152236]'
								: 'hover:bg-bg-toolbar'}"
							title={display.text}
							onclick={() => select(row, layout.index)}
							onkeydown={(event) => handleKey(event, row, layout.index)}
						>
							<span
								class="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap {display.align ===
								'right'
									? 'flex-1 text-right'
									: 'flex-1 text-left'}">{display.text}</span
							>
						</div>
					{/each}
				</div>
			{/each}
		</div>
	</div>
</div>

{#if expanded}
	<Modal
		open={true}
		title={`${expanded.column.name} · ${expanded.column.type}`}
		size="lg"
		onclose={() => (expanded = null)}
	>
		<pre class="overflow-auto font-mono text-xs">{fullJson(expanded.value)}</pre>
	</Modal>
{/if}
