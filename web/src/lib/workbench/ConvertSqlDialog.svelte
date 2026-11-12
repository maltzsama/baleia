<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Modal from '$lib/ui/Modal.svelte';
	import KindIcon from '$lib/ui/patterns/KindIcon.svelte';
	import { kindForDialect } from '$lib/ui/patterns/kindIcon';
	import Button from '$lib/ui/Button.svelte';
	import CodeBlock from '$lib/ui/patterns/CodeBlock.svelte';
	import { toasts } from '$lib/ui/toast.svelte';
	import { DEFAULT_TARGET_DIALECT, DIALECT_NAMES, optimizeSQL, transpileSQL } from './sqlingo';

	// Label functions, not strings, so a language switch re-renders the options
	// (AGENTS.md). The fallback is the dialect name itself.
	const DIALECT_TEXT: Record<string, () => string> = {
		trino: m.dialect_trino,
		presto: m.dialect_presto,
		postgresql: m.dialect_postgresql,
		mysql: m.dialect_mysql,
		mariadb: m.dialect_mariadb,
		tsql: m.dialect_tsql,
		oracle: m.dialect_oracle,
		sqlite: m.dialect_sqlite,
		clickhouse: m.dialect_clickhouse,
		starrocks: m.dialect_starrocks,
		doris: m.dialect_doris,
		dremio: m.dialect_dremio
	};

	let {
		open = $bindable(false),
		mode = 'convert',
		sql,
		sourceDialect,
		codemirrorBase,
		target = $bindable(DEFAULT_TARGET_DIALECT)
	}: {
		open?: boolean;
		mode?: 'convert' | 'optimize';
		/** The SQL snapshot taken when the dialog opened. */
		sql: string;
		/** The current engine's dialect name (the read side of a conversion). */
		sourceDialect: string | null;
		/** The engine's CodeMirror base, for syntax highlighting. */
		codemirrorBase?: string | null;
		/** The target dialect; the parent picks a default before opening. */
		target?: string;
	} = $props();

	let result = $state('');
	let error = $state('');
	let loading = $state(false);
	// Invalidates a pending run when the dialog closes or the target changes.
	let runId = 0;

	const options = $derived(
		DIALECT_NAMES.map((name) => ({ value: name, label: DIALECT_TEXT[name]?.() ?? name }))
	);
	const title = $derived(
		mode === 'convert'
			? m.convert_sql_dialog_title_convert()
			: m.convert_sql_dialog_title_optimize()
	);

	async function run() {
		const id = ++runId;
		const text = sql ?? '';
		error = '';
		result = '';
		if (!text.trim()) {
			loading = false;
			return;
		}
		loading = true;
		try {
			const next =
				mode === 'optimize'
					? await optimizeSQL(sourceDialect, text)
					: await transpileSQL(sourceDialect, target, text);
			if (id !== runId) return;
			result = next;
		} catch {
			if (id !== runId) return;
			error =
				mode === 'optimize'
					? m.convert_sql_dialog_failed_optimize()
					: m.convert_sql_dialog_failed_convert();
		} finally {
			if (id === runId) loading = false;
		}
	}

	// Recompute when the dialog opens, the mode changes or the target changes.
	$effect(() => {
		if (!open) {
			runId++;
			return;
		}
		void run();
	});

	async function copy() {
		if (!result) return;
		try {
			await navigator.clipboard.writeText(result);
			toasts.success(m.components_resultgrid_copied_to_clipboard());
		} catch {
			toasts.error(m.components_resultgrid_could_not_copy_to_the_clipboard());
		}
	}
</script>

<Modal bind:open {title} size="lg">
	<div class="flex flex-col gap-3">
		{#if mode === 'convert'}
			<fieldset class="flex flex-col gap-1.5">
				<legend class="text-[11px] font-semibold text-text-muted">
					{m.convert_sql_dialog_target_dialect()}
				</legend>
				<div class="flex flex-wrap gap-1.5">
					{#each options as option (option.value)}
						<label
							class="inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-md border px-2.5 text-xs text-text-main transition-colors duration-150 has-[:checked]:border-primary has-[:checked]:bg-primary/10 has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-primary {target ===
							option.value
								? 'border-primary'
								: 'border-border hover:bg-bg-toolbar'}"
						>
							<input
								type="radio"
								name="convert-target-dialect"
								class="sr-only"
								value={option.value}
								bind:group={target}
							/>
							<KindIcon kind={kindForDialect(option.value)} family="engine" />
							{option.label}
						</label>
					{/each}
				</div>
			</fieldset>
		{/if}

		{#if loading}
			<p class="text-xs text-text-muted" role="status">{m.convert_sql_dialog_working()}</p>
		{:else if error}
			<p class="text-xs text-danger" role="alert">{error}</p>
		{:else if !result}
			<p class="text-xs text-text-muted">{m.convert_sql_dialog_nothing_to_convert()}</p>
		{:else}
			<CodeBlock
				code={result}
				language="sql"
				dialect={codemirrorBase}
				label={m.convert_sql_dialog_result()}
			/>
		{/if}

		<p class="text-[11px] text-text-muted">{m.convert_sql_dialog_best_effort()}</p>
	</div>

	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button variant="secondary" onclick={() => (open = false)}>{m.common_close()}</Button>
			<Button variant="primary" disabled={!result || loading} onclick={copy}>
				{m.convert_sql_dialog_copy()}
			</Button>
		</div>
	{/snippet}
</Modal>
