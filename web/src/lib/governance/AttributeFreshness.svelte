<script lang="ts">
	import { m } from '$lib/paraglide/messages';
</script>

<!-- ADR 0028, "Subjects and their freshness": where each attribute comes
     from and when a change reaches the engines. -->
<section aria-labelledby="governance-freshness" class="rounded-lg border border-border bg-bg-panel">
	<h2 id="governance-freshness" class="border-b border-border px-3 py-2 text-[13px] font-semibold">
		{m.governance_freshness_title()}
	</h2>
	<table class="w-full text-left text-[12px]">
		<thead class="text-[10px] uppercase tracking-wide text-text-muted">
			<tr>
				<th class="px-3 py-2">{m.governance_attribute()}</th>
				<th class="px-3 py-2">{m.governance_source()}</th>
				<th class="px-3 py-2">{m.governance_reaches_engines()}</th>
			</tr>
		</thead>
		<tbody>
			<tr class="border-t border-border/60">
				<td class="px-3 py-2">{m.governance_attr_baleia()}</td>
				<td class="px-3 py-2">{m.governance_source_baleia()}</td>
				<td class="px-3 py-2">{m.governance_reach_next_build()}</td>
			</tr>
			<tr class="border-t border-border/60">
				<td class="px-3 py-2">{m.governance_attr_groups()}</td>
				<td class="px-3 py-2">{m.governance_source_sign_in()}</td>
				<td class="px-3 py-2">{m.governance_reach_next_sign_in()}</td>
			</tr>
		</tbody>
	</table>
	<p class="border-t border-border px-3 py-2 text-[12px] text-text-muted">
		{m.governance_suspension_immediate()}
	</p>
</section>
