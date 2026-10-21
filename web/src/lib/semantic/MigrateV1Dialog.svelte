<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import {
		SemanticRequestError,
		migrateV1,
		type MigrateV1Item,
		type MigrateV1Report
	} from './explore';

	// v1 -> v2 migration (#91, #121): a dry run lists each table and what would
	// happen; the author picks primary keys for new models and applies. Upsert
	// by slug: nothing is deleted and v1 metrics keep working.
	let {
		open = $bindable(false),
		workspaceId,
		onmigrated
	}: { open?: boolean; workspaceId: string; onmigrated: () => void } = $props();

	let report = $state<MigrateV1Report | null>(null);
	let keys = $state<Record<string, string[]>>({});
	let loading = $state(false);
	let applying = $state(false);
	let applied = $state(false);
	// A failed request, rendered through the error catalog (FD7).
	let error = $state<{ code: string; error_native?: string } | null>(null);
	let wasOpen = false;

	const tableKey = (item: MigrateV1Item) =>
		`${item.catalog_id}|${item.namespace.join('.')}|${item.table}`;
	const pending = $derived(
		(report?.items ?? []).filter((item) => item.action === 'CREATE' || item.action === 'UPDATE')
	);
	// A new model can be created once it has a primary key; the API reports a
	// missing one as an error item.
	const needsKey = (item: MigrateV1Item) => !item.model_id;

	async function run(dryRun: boolean) {
		error = null;
		const tables = (report?.items ?? [])
			.filter((item) => needsKey(item) && (keys[tableKey(item)] ?? []).length > 0)
			.map((item) => ({
				catalog_id: item.catalog_id,
				namespace: item.namespace,
				table: item.table,
				primary_key: keys[tableKey(item)]
			}));
		try {
			report = await migrateV1(workspaceId, { dry_run: dryRun, tables });
			for (const item of report.items) {
				const key = tableKey(item);
				if (!keys[key]) keys[key] = [...item.primary_key];
			}
			if (!dryRun) {
				applied = true;
				onmigrated();
			}
		} catch (e) {
			error = {
				code: e instanceof SemanticRequestError ? (e.code ?? 'internal') : 'internal',
				error_native: e instanceof Error ? e.message : String(e)
			};
		}
	}

	async function preview() {
		loading = true;
		await run(true);
		loading = false;
	}

	async function apply() {
		applying = true;
		await run(false);
		applying = false;
	}

	function toggleKey(item: MigrateV1Item, column: string) {
		const key = tableKey(item);
		const current = keys[key] ?? [];
		keys[key] = current.includes(column)
			? current.filter((c) => c !== column)
			: [...current, column];
	}

	const actionLabel = {
		CREATE: m.models_migrate_action_create,
		UPDATE: m.models_migrate_action_update,
		NO_CHANGES: m.models_migrate_action_no_changes,
		ERROR: m.models_migrate_action_error
	};
	const errorLabel: Record<NonNullable<MigrateV1Item['error_code']>, () => string> = {
		catalog_not_granted: m.models_migrate_error_catalog_not_granted,
		primary_key_required: m.models_migrate_error_primary_key_required,
		primary_key_invalid: m.models_migrate_error_primary_key_invalid,
		space_forbidden: m.models_migrate_error_space_forbidden,
		invalid_metric: m.models_migrate_error_invalid_metric,
		invalid_definition: m.models_migrate_error_invalid_definition,
		slug_conflict: m.models_migrate_error_slug_conflict,
		write_failed: m.models_migrate_error_write_failed
	};

	const tones = {
		CREATE: 'info',
		UPDATE: 'info',
		NO_CHANGES: 'neutral',
		ERROR: 'danger'
	} as const;

	$effect(() => {
		if (open && !wasOpen) {
			report = null;
			keys = {};
			applied = false;
			void preview();
		}
		wasOpen = open;
	});
</script>

<Modal bind:open title={m.models_migrate_title()}>
	<div class="flex flex-col gap-3 text-[12px]">
		<p class="text-text-muted">{m.models_migrate_intro()}</p>
		{#if loading && !report}
			<Skeleton class="h-24 w-full" />
		{:else if error}
			<ErrorPanel {error} />
		{:else if report && report.items.length === 0}
			<p>{m.models_migrate_nothing()}</p>
		{:else if report}
			{#if applied}
				<p role="status" class="font-semibold">{m.models_migrate_done()}</p>
			{/if}
			<ul class="flex flex-col gap-2" aria-label={m.models_migrate_tables()}>
				{#each report.items as item (tableKey(item))}
					<li class="rounded border border-border p-2">
						<div class="flex flex-wrap items-center justify-between gap-2">
							<span class="font-mono">{[...item.namespace, item.table].join('.')}</span>
							<Badge tone={tones[item.action]}>{actionLabel[item.action]()}</Badge>
						</div>
						<div class="mt-1 text-text-muted">
							{m.models_migrate_into({ model: item.model_slug, metrics: item.metrics.join(', ') })}
						</div>
						{#if item.error_code}
							<p class="mt-1 text-danger">{errorLabel[item.error_code]()}</p>
						{/if}
						{#if item.error}
							<p class="mt-0.5 font-mono text-[10px] text-text-muted">{item.error}</p>
						{/if}
						{#if needsKey(item) && !applied}
							<fieldset class="mt-2">
								<legend class="text-[11px] font-semibold text-text-muted">
									{m.models_primary_key()}
								</legend>
								<div class="mt-1 flex flex-wrap gap-3">
									{#each item.columns as column (column)}
										<Checkbox
											label={column}
											checked={(keys[tableKey(item)] ?? []).includes(column)}
											onchange={() => toggleKey(item, column)}
										/>
									{/each}
								</div>
							</fieldset>
						{/if}
					</li>
				{/each}
			</ul>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (open = false)}
				>{applied ? m.models_close() : m.common_cancel()}</Button
			>
			{#if !applied}
				<Button onclick={preview} {loading} disabled={loading || applying}>
					{m.models_migrate_recheck()}
				</Button>
				<Button
					variant="primary"
					onclick={apply}
					loading={applying}
					disabled={applying || loading || pending.length === 0}
				>
					{m.models_migrate_apply({ count: pending.length })}
				</Button>
			{/if}
		</div>
	{/snippet}
</Modal>
