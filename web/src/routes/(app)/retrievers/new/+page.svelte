<script lang="ts">
	import RetrieverForm from '$lib/retrievers/RetrieverForm.svelte';
</script>

<div class="flex-1 overflow-y-auto p-7">
	<h1 class="text-xl font-semibold tracking-[-0.3px]">New retriever</h1>
	<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
		Run a SELECT on a schedule and keep the result as an Iceberg table.
	</p>
	<div class="mt-4 max-w-3xl">
		<RetrieverForm
			ondone={(saved) => {
				window.location.href = `/retrievers/${saved.id}`;
			}}
		/>
	</div>
</div>
