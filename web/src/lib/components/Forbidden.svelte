<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { resolve } from '$app/paths';

	let {
		message = m.components_forbidden_this_section_is_restricted_to_administrato()
	}: { message?: string } = $props();
</script>

<div class="flex-1 overflow-y-auto p-7" role="alert">
	<div class="max-w-lg">
		<p class="text-[11px] font-semibold uppercase tracking-wide text-danger">
			{m.components_forbidden_403_forbidden()}
		</p>
		<h1 class="mt-2 text-xl font-semibold tracking-[-0.3px]">
			{m.components_forbidden_administrator_access_required()}
		</h1>
		<p class="mt-1 text-[13px] text-text-muted">{message}</p>
		<p class="mt-1 text-[13px] text-text-muted">
			{m.components_forbidden_ask_an_administrator_for_the_permission_or()}
		</p>
		<a
			href={resolve('/sql')}
			class="mt-4 inline-block rounded-md bg-primary px-4 py-2 text-[13px] font-semibold text-white no-underline hover:bg-primary-hover"
		>
			{m.components_forbidden_back_to_the_workbench()}
		</a>
	</div>
</div>
