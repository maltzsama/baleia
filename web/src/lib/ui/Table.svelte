<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import type { Snippet } from 'svelte';

	type Column = {
		key: string;
		label: string;
		sortable?: boolean;
		align?: 'left' | 'right';
		class?: string;
	};
	type Row = Record<string, unknown>;
	type Sort = { key: string; direction: 'asc' | 'desc' };

	let {
		columns,
		rows,
		rowKey,
		rowHref,
		sort,
		onsort,
		loading = false,
		empty,
		cell
	}: {
		columns: Column[];
		rows: Row[];
		rowKey: (row: Row) => string;
		rowHref?: (row: Row) => string;
		sort?: Sort;
		onsort?: (key: string) => void;
		loading?: boolean;
		empty?: Snippet;
		cell?: Snippet<[Row, Column]>;
	} = $props();

	function headerClass(column: Column): string {
		return `sticky top-0 z-[1] border-b border-border bg-bg-toolbar px-3.5 py-2 text-[11px] font-semibold uppercase text-text-muted ${
			column.align === 'right' ? 'text-right' : 'text-left'
		} ${column.class ?? ''}`;
	}

	function cellClass(column: Column): string {
		return `border-b border-border px-3.5 py-2 text-xs ${
			column.align === 'right' ? 'text-right' : 'text-left'
		}`;
	}
</script>

<div class="overflow-auto rounded-lg border border-border bg-bg-panel">
	<table class="w-full border-separate border-spacing-0">
		<thead>
			<tr>
				{#each columns as column (column.key)}
					<th class={headerClass(column)}>
						{#if column.sortable && onsort}
							<button
								type="button"
								class="inline-flex items-center gap-1 font-semibold uppercase transition-colors hover:text-text-main"
								onclick={() => onsort?.(column.key)}
							>
								{column.label}
								{#if sort?.key === column.key}
									<span aria-hidden="true">{sort.direction === 'asc' ? '▲' : '▼'}</span>
								{/if}
							</button>
						{:else}
							{column.label}
						{/if}
					</th>
				{/each}
			</tr>
		</thead>
		<tbody>
			{#if loading}
				<tr>
					<td colspan={columns.length} class="px-3.5 py-8 text-center text-xs text-text-muted"
						>{m.common_loading_2()}</td
					>
				</tr>
			{:else if rows.length === 0}
				<tr>
					<td colspan={columns.length} class="px-3.5 py-8 text-center text-xs text-text-muted">
						{#if empty}
							{@render empty()}
						{:else}
							No rows.
						{/if}
					</td>
				</tr>
			{:else}
				{#each rows as row (rowKey(row))}
					<tr class="hover:[&>td]:bg-bg-toolbar">
						{#each columns as column, columnIndex (column.key)}
							<td class={cellClass(column)}>
								{#if cell}
									{@render cell(row, column)}
								{:else if rowHref && columnIndex === 0}
									<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- href supplied by the caller (#105) -->
									<a class="text-primary hover:underline" href={rowHref(row)}>
										{String(row[column.key] ?? '')}
									</a>
								{:else}
									{String(row[column.key] ?? '')}
								{/if}
							</td>
						{/each}
					</tr>
				{/each}
			{/if}
		</tbody>
	</table>
</div>
