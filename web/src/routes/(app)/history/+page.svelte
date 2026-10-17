<script lang="ts">
	import EmptyState from '$lib/ui/EmptyState.svelte';
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-5xl">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">Query history</h1>
		<p class="mt-1 text-[13px] text-text-muted">
			Searchable query history arrives with #36. Executions are already stored server-side.
		</p>
		<div class="mt-6 rounded-lg border border-border bg-bg-panel">
			<EmptyState
				title="No history view yet"
				description="This route is ready for the history table and per-statement drawer."
			/>
		</div>
	</div>
</div>
