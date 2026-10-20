<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Button from '$lib/ui/Button.svelte';
	import Modal from '$lib/ui/Modal.svelte';

	let { open = $bindable(false), onsignin }: { open?: boolean; onsignin: () => void } = $props();
</script>

<Modal bind:open title={m.components_sessionexpired_your_session_has_expired()}>
	<p class="text-[13px] text-text-muted">
		{m.components_sessionexpired_sign_in_again_to_continue_unsaved_drafts_a()}
	</p>
	{#snippet footer()}
		<Button variant="primary" onclick={onsignin}
			>{m.components_sessionexpired_sign_in_again()}</Button
		>
	{/snippet}
</Modal>
