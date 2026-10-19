<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import ErrorPanel from '$lib/ui/patterns/ErrorPanel.svelte';
	import { formatDuration } from '$lib/util/time';
	import { orderedStatements, type RunState, type StatementState } from './reducer';

	let {
		state,
		executing,
		canCancel,
		oncancel,
		onrerun,
		onpick
	}: {
		state: RunState;
		executing: boolean;
		canCancel: boolean;
		oncancel: () => void;
		onrerun: (position: number) => void;
		onpick?: (position: number) => void;
	} = $props();

	const statements = $derived(orderedStatements(state));
	const failed = $derived(
		statements.find((s) => s.status === 'failed' || s.status === 'cancelled')
	);
	const skipped = $derived(statements.filter((s) => s.status === 'skipped'));
	const interrupted = $derived(statements.find((s) => s.errorCode === 'interrupted'));
	const readOnlyRejected = $derived(
		statements.find(
			(s) => s.errorCode === 'permission_denied' || s.errorCode === 'read_only_unsupported'
		)
	);

	function firstLine(sql: string): string {
		return (
			sql
				.split('\n')
				.find((line) => line.trim())
				?.trim() ?? ''
		);
	}

	function rowsLabel(statement: StatementState): string {
		if (statement.rowsReturned == null) return '';
		return `${statement.rowsReturned} rows`;
	}
</script>

<section
	class="flex flex-col border-t border-border bg-bg-panel"
	aria-label={m.run_runpanel_run_progress()}
>
	<header
		class="flex flex-wrap items-center gap-3 border-b border-border px-3.5 py-1.5 text-[11px]"
	>
		<StatusBadge kind="execution" status={state.status} />
		{#if state.target}
			<span class="text-slate-600 dark:text-slate-300">
				{m.common_ran_on()}
				{state.target.engine_name} · {state.target.catalog_name}{state.target.schema
					? `.${state.target.schema}`
					: ''}
			</span>
		{/if}
		{#if state.readOnly}
			<Badge tone="warning">{m.common_read_only()}</Badge>
		{/if}
		<span class="text-slate-600 dark:text-slate-300">
			{statements.length}
			{m.run_runpanel_statement()}{statements.length === 1 ? '' : 's'}
		</span>
		{#if executing && canCancel}
			<Button size="sm" onclick={oncancel}>{m.common_cancel()}</Button>
		{/if}
	</header>

	{#if interrupted}
		<div
			class="border-b border-amber-200 bg-amber-50 px-3.5 py-2 text-[11px] text-amber-900 dark:border-[#4d3600] dark:bg-[#241a06] dark:text-amber-300"
		>
			<p>
				{m.run_runpanel_the_run_stopped_because_the_baleia_worker()}
				{interrupted.position}
				{m.run_runpanel_may_or_may_not_have_completed_on_the_engin()}
			</p>
			<div class="mt-1 flex gap-2">
				<Button size="sm" onclick={() => onrerun(interrupted.position)}>
					{m.run_runpanel_re_run_from_statement()}
					{interrupted.position}
				</Button>
				<Button size="sm" onclick={() => onrerun(interrupted.position + 1)}>
					{m.run_runpanel_re_run_from_statement()}
					{interrupted.position + 1}
				</Button>
			</div>
		</div>
	{:else if failed && skipped.length > 0}
		<div
			class="border-b border-red-200 bg-red-50 px-3.5 py-2 text-[11px] text-red-800 dark:border-[#4d1111] dark:bg-[#1f0f0f] dark:text-red-300"
		>
			<p>
				Statement {failed.position} failed. Statements {skipped[0].position}–{skipped[
					skipped.length - 1
				].position} were not run.
			</p>
			<div class="mt-1 flex gap-2">
				<Button size="sm" onclick={() => onrerun(failed.position)}>
					Re-run from statement {failed.position}
				</Button>
				<Button size="sm" onclick={() => onrerun(skipped[0].position)}>
					Re-run from statement {skipped[0].position}
				</Button>
			</div>
		</div>
	{:else if readOnlyRejected && state.readOnly}
		<div
			class="border-b border-amber-200 bg-amber-50 px-3.5 py-2 text-[11px] text-amber-900 dark:border-[#4d3600] dark:bg-[#241a06] dark:text-amber-300"
		>
			This workspace has read access to {state.target?.catalog_name ??
				m.run_runpanel_this_catalog()}; statements that modify data are rejected.
		</div>
	{/if}

	<ol class="max-h-56 overflow-y-auto px-3.5 py-2 text-[11px]">
		{#each statements as statement (statement.position)}
			<li class="flex flex-col gap-1 border-b border-border/60 py-1.5 last:border-b-0">
				<div class="flex items-center gap-2">
					<span class="w-5 shrink-0 font-mono text-slate-600 dark:text-slate-300">
						{statement.position}
					</span>
					<StatusBadge kind="statement" status={statement.status} />
					<button
						type="button"
						class="min-w-0 flex-1 truncate text-left font-mono hover:text-primary"
						title={statement.sql}
						onclick={() => onpick?.(statement.position)}
					>
						{firstLine(statement.sql)}
					</button>
					{#if statement.truncated}
						<Badge tone="warning">{m.run_runpanel_truncated()}</Badge>
					{/if}
					{#if statement.durationMs != null}
						<span class="shrink-0 text-slate-600 dark:text-slate-300">
							{formatDuration(statement.durationMs)}
						</span>
					{/if}
					{#if rowsLabel(statement)}
						<span class="shrink-0 text-slate-600 dark:text-slate-300">{rowsLabel(statement)}</span>
					{/if}
				</div>
				{#if statement.status === 'failed' && statement.errorCode}
					<div class="pl-7">
						<ErrorPanel
							error={{ code: statement.errorCode, message: statement.errorMessage ?? '' }}
						/>
					</div>
				{/if}
			</li>
		{/each}
		{#if statements.length === 0}
			<li class="py-2 text-slate-600 dark:text-slate-300">
				{m.run_runpanel_waiting_for_the_first_statement()}
			</li>
		{/if}
	</ol>
</section>
