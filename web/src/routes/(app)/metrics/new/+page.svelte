<script lang="ts">
	import MetricBuilder from '$lib/metrics/MetricBuilder.svelte';
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">New metric</h1>
		<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
			One aggregation over one table. The SQL is rendered for the engine you choose.
		</p>
	</div>
	<MetricBuilder />
</div>
