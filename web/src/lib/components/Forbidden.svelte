<script lang="ts">
	import { resolve } from '$app/paths';

	let { message = 'This section is restricted to administrators.' }: { message?: string } =
		$props();
</script>

<div class="flex-1 overflow-y-auto p-7" role="alert">
	<div class="max-w-lg">
		<p class="text-[11px] font-semibold uppercase tracking-wide text-danger">403 · Forbidden</p>
		<h1 class="mt-2 text-xl font-semibold tracking-[-0.3px]">Administrator access required</h1>
		<p class="mt-1 text-[13px] text-text-muted">{message}</p>
		<p class="mt-1 text-[13px] text-text-muted">
			Ask an administrator for the permission, or go back to the workbench.
		</p>
		<a
			href={resolve('/sql')}
			class="mt-4 inline-block rounded-md bg-primary px-4 py-2 text-[13px] font-semibold text-white no-underline hover:bg-primary-hover"
		>
			Back to the workbench
		</a>
	</div>
</div>
