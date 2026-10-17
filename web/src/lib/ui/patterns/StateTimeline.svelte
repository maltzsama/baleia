<script lang="ts">
	import RelativeTime from './RelativeTime.svelte';

	type Step = { label: string; status: string; at?: string; detail?: string };

	let { steps }: { steps: Step[] } = $props();

	const tone: Record<string, string> = {
		ok: 'border-success text-success',
		finished: 'border-success text-success',
		done: 'border-success text-success',
		running: 'border-primary text-primary',
		pending: 'border-border text-text-muted',
		failed: 'border-danger text-danger',
		error: 'border-danger text-danger',
		skipped: 'border-border text-text-muted'
	};
</script>

<ol class="flex flex-col">
	{#each steps as step, index (index)}
		<li class="flex gap-3">
			<div class="flex flex-col items-center">
				<span
					class="mt-1 flex h-4 w-4 items-center justify-center rounded-full border-2 {tone[
						step.status
					] ?? 'border-border text-text-muted'}"
					aria-hidden="true"
				></span>
				{#if index < steps.length - 1}
					<span class="my-1 w-px flex-1 bg-border"></span>
				{/if}
			</div>
			<div class="pb-3">
				<div class="text-[13px] font-medium text-text-main">{step.label}</div>
				{#if step.at}
					<div class="text-[11px] text-text-muted"><RelativeTime at={step.at} /></div>
				{/if}
				{#if step.detail}
					<div class="text-[11px] text-text-muted">{step.detail}</div>
				{/if}
			</div>
		</li>
	{/each}
</ol>
