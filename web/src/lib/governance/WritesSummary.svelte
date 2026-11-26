<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Badge from '$lib/ui/Badge.svelte';
	import type { DataPolicyWriteEffect, DataPolicyWrites } from './api';

	let { writes }: { writes: DataPolicyWrites } = $props();

	const ops = $derived([
		{ key: 'insert', sql: 'INSERT', effect: writes.insert },
		{ key: 'update', sql: 'UPDATE', effect: writes.update },
		{ key: 'delete', sql: 'DELETE', effect: writes.delete },
		{ key: 'merge', sql: 'MERGE', effect: writes.merge }
	]);

	function effectLabel(effect: DataPolicyWriteEffect): string {
		if (effect === 'checked') return m.governance_write_checked();
		if (effect === 'visible_rows') return m.governance_write_visible_rows();
		return m.governance_write_refused();
	}
</script>

<!-- Trino 484's write rules on a policied table (ADR 0028). -->
<div class="flex flex-col gap-1">
	<dl class="flex flex-wrap gap-x-3 gap-y-1 text-[11px]">
		{#each ops as op (op.key)}
			<div class="flex items-center gap-1">
				<dt class="font-mono">{op.sql}</dt>
				<dd>
					<Badge tone={op.effect === 'refused' ? 'danger' : 'warning'}
						>{effectLabel(op.effect)}</Badge
					>
				</dd>
			</div>
		{/each}
	</dl>
	<p class="text-[11px] text-text-muted">{m.governance_writes_help()}</p>
</div>
