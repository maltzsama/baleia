<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Badge from '$lib/ui/Badge.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import type { DataPolicyStatus } from './api';
	import { healthReasonLabel } from './labels';

	let { status }: { status: DataPolicyStatus } = $props();

	// Draft, published and active are shown apart (ADR 0028): the published
	// revision is the pointer, the active one is what each engine's OPA runs.
	const published = $derived(status.published?.id ?? null);
</script>

<section aria-labelledby="governance-engines" class="rounded-lg border border-border bg-bg-panel">
	<h2 id="governance-engines" class="border-b border-border px-3 py-2 text-[13px] font-semibold">
		{m.governance_active_revision_per_engine()}
	</h2>
	{#if status.engines.length === 0}
		<p class="px-3 py-3 text-[12px] text-text-muted">{m.governance_no_engines()}</p>
	{:else}
		<table class="w-full text-left text-[12px]">
			<thead class="text-[10px] uppercase tracking-wide text-text-muted">
				<tr>
					<th class="px-3 py-2">{m.common_engine()}</th>
					<th class="px-3 py-2">{m.governance_health()}</th>
					<th class="px-3 py-2">{m.governance_active_revision()}</th>
					<th class="px-3 py-2">{m.governance_last_report()}</th>
				</tr>
			</thead>
			<tbody>
				{#each status.engines as engine (engine.engine_id)}
					<tr class="border-t border-border/60">
						<td class="px-3 py-2 font-medium">{engine.engine_name}</td>
						<td class="px-3 py-2">
							{#if engine.health === 'healthy'}
								<Badge tone="success">{m.governance_health_healthy()}</Badge>
							{:else if engine.health === 'unhealthy'}
								<Badge tone="danger">{m.governance_health_unhealthy()}</Badge>
								<span class="ml-1 text-text-muted">{healthReasonLabel(engine.reason)}</span>
							{:else}
								<Badge tone="neutral">{m.governance_health_disabled()}</Badge>
							{/if}
						</td>
						<td class="px-3 py-2">
							{#if engine.active_revision}
								<code class="font-mono text-[11px]">{engine.active_revision.slice(0, 8)}</code>
								{#if published && engine.active_revision === published}
									<Badge tone="success">{m.governance_matches_published()}</Badge>
								{:else if published}
									<Badge tone="warning">{m.governance_not_published_yet()}</Badge>
								{/if}
							{:else}
								<span class="text-text-muted">—</span>
							{/if}
						</td>
						<td class="px-3 py-2">
							{#if engine.reported_at}
								<RelativeTime at={engine.reported_at} />
							{:else}
								<span class="text-text-muted">{m.governance_never_reported()}</span>
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</section>
