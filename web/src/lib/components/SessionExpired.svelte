<script lang="ts">
	import Button from '$lib/ui/Button.svelte';
	import Modal from '$lib/ui/Modal.svelte';

	let { open = $bindable(false), onsignin }: { open?: boolean; onsignin: () => void } = $props();
</script>

<Modal bind:open title="Your session has expired">
	<p class="text-[13px] text-text-muted">
		Sign in again to continue. Unsaved drafts are kept for this workspace.
	</p>
	{#snippet footer()}
		<Button variant="primary" onclick={onsignin}>Sign in again</Button>
	{/snippet}
</Modal>
