<script lang="ts">
	import Toast from './Toast.svelte';
	import { toasts } from './toast.svelte';
</script>

<div
	class="pointer-events-none fixed bottom-4 right-4 z-[200] flex w-full max-w-sm flex-col gap-2"
	aria-live="polite"
	aria-atomic="false"
>
	{#each toasts.items as item (item.id)}
		<div class="pointer-events-auto">
			<Toast message={item.message} tone={item.tone} ondismiss={() => toasts.dismiss(item.id)} />
		</div>
	{/each}
</div>
