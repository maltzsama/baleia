<script lang="ts">
	import ShieldAlertIcon from '@lucide/svelte/icons/shield-alert';
	import { m } from '$lib/paraglide/messages';
</script>

<!-- ADR 0028, "Scope of the guarantee": shown next to every policy. -->
<section
	aria-labelledby="governance-guarantee"
	class="flex items-start gap-3 rounded-lg border border-amber-300 bg-amber-50 p-3 text-[12px] text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100"
>
	<ShieldAlertIcon class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
	<div class="flex flex-col gap-1">
		<h2 id="governance-guarantee" class="text-[13px] font-semibold">
			{m.governance_trino_path_only_title()}
		</h2>
		<p>{m.governance_trino_path_only()}</p>
		<p>{m.governance_side_door_requirement()}</p>
	</div>
</section>
