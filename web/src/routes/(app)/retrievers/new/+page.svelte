<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import RetrieverForm from '$lib/retrievers/RetrieverForm.svelte';
</script>

<div class="flex-1 overflow-y-auto p-7">
	<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.common_new_retriever()}</h1>
	<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
		{m.common_run_a_select_on_a_schedule_and_keep_the_re()}
	</p>
	<div class="mt-4 max-w-3xl">
		<RetrieverForm
			ondone={(saved) => {
				window.location.href = `/retrievers/${saved.id}`;
			}}
		/>
	</div>
</div>
