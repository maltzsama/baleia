<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { apiErrorMessage, client } from '$lib/api/client';
	import type { components } from '$lib/api/schema';
	import { formatBytes } from '$lib/util/size';
	import { formatDuration } from '$lib/util/time';
	import Badge from '$lib/ui/Badge.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import { hasQueryProfile } from '$lib/ui/patterns/kindIcon';

	type Details = components['schemas']['ExecutionDetails'];

	let { workspaceId, executionId }: { workspaceId: string; executionId: string } = $props();

	let details = $state<Details | null>(null);
	let loading = $state(true);
	let error = $state('');

	$effect(() => {
		const ws = workspaceId;
		const id = executionId;
		if (!ws || !id) return;
		let cancelled = false;
		loading = true;
		error = '';
		details = null;
		void client
			.GET('/workspaces/{id}/executions/{execution_id}/details', {
				params: { path: { id: ws, execution_id: id } }
			})
			.then(({ data, error: readError }) => {
				if (cancelled) return;
				if (readError || !data) {
					error = apiErrorMessage(readError, m.execution_details_failed_to_load());
					return;
				}
				details = data;
			})
			.catch((e) => {
				if (!cancelled) error = e instanceof Error ? e.message : String(e);
			})
			.finally(() => {
				if (!cancelled) loading = false;
			});
		return () => {
			cancelled = true;
		};
	});

	type Phase = { key: string; label: string; ms: number | null };
	const phases = $derived<Phase[]>(
		details
			? [
					{
						key: 'queue',
						label: m.execution_details_queue_wait(),
						ms: details.timeline.queue_wait_ms ?? null
					},
					{
						key: 'queued',
						label: m.execution_details_engine_queued(),
						ms: details.timeline.engine_queued_ms ?? null
					},
					{
						key: 'planning',
						label: m.execution_details_planning(),
						ms: details.timeline.planning_ms ?? null
					},
					{
						key: 'execution',
						label: m.execution_details_execution(),
						ms: details.timeline.execution_ms ?? null
					}
				]
			: []
	);
	const phaseMax = $derived(Math.max(1, ...phases.map((phase) => phase.ms ?? 0)));
	const direct = $derived(!!details && !hasQueryProfile(details.engine_kind));

	function duration(ms: number | null | undefined): string {
		return ms == null ? '—' : formatDuration(ms);
	}

	function bytes(value: number | null | undefined): string {
		return value == null ? '—' : formatBytes(value);
	}

	function pct(ms: number | null): string {
		return `${Math.min(100, ((ms ?? 0) / phaseMax) * 100)}%`;
	}

	function tableName(input: { catalog?: string; schema?: string; table?: string }): string {
		return [input.catalog, input.schema, input.table].filter(Boolean).join('.');
	}
</script>

{#if loading}
	<div class="space-y-2" role="status" aria-label={m.execution_details_timeline()}>
		<Skeleton class="h-4 w-full" />
		<Skeleton class="h-4 w-2/3" />
	</div>
{:else if error}
	<ErrorState title={m.execution_details_failed_to_load()} message={error} />
{:else if details}
	<div class="flex flex-col gap-4">
		<section aria-label={m.execution_details_timeline()}>
			<h3 class="mb-2 text-[10px] font-bold uppercase tracking-wide text-text-muted">
				{m.execution_details_timeline()}
			</h3>
			<ul class="flex flex-col gap-1.5">
				{#each phases as phase (phase.key)}
					<li class="flex items-center gap-2 text-[11px]">
						<span class="w-28 shrink-0 text-text-muted">{phase.label}</span>
						<span class="h-2 flex-1 rounded bg-bg-toolbar">
							<span class="block h-2 rounded bg-primary" style:width={pct(phase.ms)}></span>
						</span>
						<span class="w-16 shrink-0 text-right font-mono">{duration(phase.ms)}</span>
					</li>
				{/each}
				<li class="flex items-center gap-2 border-t border-border pt-1.5 text-[11px] font-semibold">
					<span class="w-28 shrink-0">{m.execution_details_total()}</span>
					<span class="flex-1"></span>
					<span class="w-16 shrink-0 text-right font-mono"
						>{duration(details.timeline.total_ms ?? details.duration_ms)}</span
					>
				</li>
			</ul>
		</section>

		{#if direct}
			<Badge tone="neutral">{m.execution_details_direct_engine()}</Badge>
		{/if}

		<section aria-label={m.history_statements()}>
			<h3 class="mb-2 text-[10px] font-bold uppercase tracking-wide text-text-muted">
				{m.history_statements()}
			</h3>
			<div class="flex flex-col gap-2">
				{#each details.statements as statement (statement.id)}
					<article class="rounded-md border border-border px-3 py-2">
						<div class="flex flex-wrap items-center gap-2 text-[11px] text-text-muted">
							<span class="font-semibold text-text-main">#{statement.position}</span>
							<StatusBadge kind="statement" status={statement.status} />
							{#if statement.duration_ms != null}
								<span class="font-mono">{formatDuration(statement.duration_ms)}</span>
							{/if}
							{#if statement.rows_returned != null}
								<span class="font-mono">{statement.rows_returned.toLocaleString()}</span>
							{/if}
							{#if statement.result_bytes != null}
								<span class="font-mono">{bytes(statement.result_bytes)}</span>
							{/if}
						</div>
						{#if statement.profile}
							<dl class="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 text-[11px] sm:grid-cols-3">
								<div>
									<dt class="text-text-muted">{m.execution_details_cpu()}</dt>
									<dd class="font-mono">{duration(statement.profile.cpu_ms)}</dd>
								</div>
								<div>
									<dt class="text-text-muted">{m.execution_details_peak_memory()}</dt>
									<dd class="font-mono">{bytes(statement.profile.peak_memory_bytes)}</dd>
								</div>
								<div>
									<dt class="text-text-muted">{m.execution_details_spilled()}</dt>
									<dd class="font-mono">{bytes(statement.profile.spilled_bytes)}</dd>
								</div>
								<div>
									<dt class="text-text-muted">{m.execution_details_input()}</dt>
									<dd class="font-mono">
										{bytes(statement.profile.input_bytes)} ·
										{statement.profile.input_rows?.toLocaleString() ?? '—'}
									</dd>
								</div>
								<div>
									<dt class="text-text-muted">{m.execution_details_output()}</dt>
									<dd class="font-mono">
										{bytes(statement.profile.output_bytes)} ·
										{statement.profile.output_rows?.toLocaleString() ?? '—'}
									</dd>
								</div>
							</dl>
							{#if statement.profile.inputs?.length}
								<table class="mt-2 w-full text-left text-[11px]">
									<thead>
										<tr class="text-text-muted">
											<th class="py-0.5 font-semibold">{m.execution_details_table()}</th>
											<th class="py-0.5 text-right font-semibold">{m.execution_details_input()}</th>
										</tr>
									</thead>
									<tbody>
										{#each statement.profile.inputs as input (tableName(input))}
											<tr>
												<td class="font-mono">{tableName(input)}</td>
												<td class="text-right font-mono">
													{bytes(input.input_bytes)} ·
													{input.input_rows?.toLocaleString() ?? '—'}
												</td>
											</tr>
										{/each}
									</tbody>
								</table>
							{/if}
						{:else if !direct}
							<p class="mt-1 text-[11px] text-text-muted">
								{m.execution_details_no_profile()}
							</p>
						{/if}
					</article>
				{/each}
			</div>
		</section>
	</div>
{/if}
