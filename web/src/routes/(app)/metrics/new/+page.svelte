<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import MetricBuilder from '$lib/metrics/MetricBuilder.svelte';
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.common_new_metric()}</h1>
		<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
			{m.metrics_new_one_aggregation_over_one_table_the_sql_is()}
		</p>
	</div>
	<MetricBuilder />
</div>
