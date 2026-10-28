<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	let {
		value = $bindable({}),
		protectedKeys = [],
		validateKey
	}: {
		value?: Record<string, string>;
		protectedKeys?: string[];
		validateKey?: (key: string) => string | undefined;
	} = $props();

	type Row = { id: number; key: string; value: string };

	let nextId = 1;
	let rows = $state<Row[]>(
		Object.entries(value).map(([key, val]) => ({ id: nextId++, key, value: val }))
	);
	let error = $state('');

	function sync() {
		const next: Record<string, string> = {};
		for (const row of rows) {
			if (!row.key) continue;
			next[row.key] = row.value;
		}
		value = next;
	}

	function add() {
		rows.push({ id: nextId++, key: '', value: '' });
	}

	function remove(id: number) {
		rows = rows.filter((row) => row.id !== id);
		sync();
	}

	function checkKey(row: Row) {
		if (!row.key) return;
		if (rows.filter((item) => item.key === row.key).length > 1) {
			error = m.keyvalueeditor_duplicate_key({ key: row.key });
			return;
		}
		error = validateKey?.(row.key) ?? '';
	}

	function handlePaste(event: ClipboardEvent) {
		const text = event.clipboardData?.getData('text') ?? '';
		const lines = text
			.split('\n')
			.map((line) => line.trim())
			.filter((line) => line.includes('='));
		if (lines.length < 2) return;
		event.preventDefault();
		rows = lines.map((line) => {
			const [key, ...rest] = line.split('=');
			return { id: nextId++, key: key.trim(), value: rest.join('=').trim() };
		});
		sync();
	}
</script>

<div class="flex flex-col gap-2" onpaste={handlePaste}>
	{#each rows as row (row.id)}
		{@const locked = protectedKeys.includes(row.key)}
		<div class="flex items-center gap-2">
			<input
				class="h-8 w-40 rounded-md border border-border bg-bg-toolbar px-2 font-mono text-xs outline-none focus:border-primary read-only:opacity-70"
				placeholder={m.keyvalueeditor_key()}
				bind:value={row.key}
				readonly={locked}
				onblur={() => {
					checkKey(row);
					sync();
				}}
			/>
			<input
				class="h-8 flex-1 rounded-md border border-border bg-bg-toolbar px-2 font-mono text-xs outline-none focus:border-primary"
				placeholder={m.keyvalueeditor_value()}
				bind:value={row.value}
				onblur={sync}
			/>
			{#if locked}
				<span
					class="text-[10px] text-text-muted"
					title={m.keyvalueeditor_set_by_baleia_cannot_be_overridden()}>🔒</span
				>
			{:else}
				<button
					type="button"
					class="rounded px-1.5 text-text-muted hover:text-danger"
					aria-label={m.keyvalueeditor_remove({ value: row.key || m.keyvalueeditor_row() })}
					onclick={() => remove(row.id)}
				>
					×
				</button>
			{/if}
		</div>
	{/each}
	<div class="flex items-center gap-2">
		<button
			type="button"
			class="rounded-md border border-border px-2.5 py-1 text-[11px] font-semibold text-text-muted hover:border-border-hover hover:text-text-main"
			onclick={add}
		>
			{m.keyvalueeditor_add_row()}
		</button>
		{#if error}<span class="text-[11px] text-danger">{error}</span>{/if}
	</div>
</div>
