<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import type { Snippet } from 'svelte';
	import Button from '../Button.svelte';
	import Table from '../Table.svelte';

	type Column = {
		key: string;
		label: string;
		sortable?: boolean;
		align?: 'left' | 'right';
		class?: string;
	};
	type Row = Record<string, unknown>;

	let {
		columns,
		rows,
		rowKey,
		rowHref,
		loading = false,
		hasMore = false,
		onloadmore,
		empty,
		cell
	}: {
		columns: Column[];
		rows: Row[];
		rowKey: (row: Row) => string;
		rowHref?: (row: Row) => string;
		loading?: boolean;
		hasMore?: boolean;
		onloadmore?: () => void;
		empty?: Snippet;
		cell?: Snippet<[Row, Column]>;
	} = $props();
</script>

<div class="flex flex-col gap-2">
	<Table {columns} {rows} {rowKey} {rowHref} {loading} {empty} {cell} />
	{#if hasMore}
		<div class="flex justify-center">
			<Button size="sm" onclick={() => onloadmore?.()} disabled={loading} {loading}>
				{m.datatable_load_more()}
			</Button>
		</div>
	{/if}
</div>
