<script lang="ts">
	import Button from '../Button.svelte';
	import Checkbox from '../Checkbox.svelte';
	import Modal from '../Modal.svelte';
	import CopyButton from './CopyButton.svelte';

	let {
		open = $bindable(false),
		secret,
		title = 'Copy this secret now',
		onacknowledge
	}: {
		open?: boolean;
		secret: string;
		title?: string;
		onacknowledge?: () => void;
	} = $props();

	let acknowledged = $state(false);

	function close() {
		open = false;
		acknowledged = false;
		onacknowledge?.();
	}
</script>

<Modal bind:open {title}>
	<div class="flex flex-col gap-3 text-[13px]">
		<p class="text-text-muted">This value is shown once and is never stored by the UI.</p>
		<div
			class="flex items-center gap-2 rounded-md border border-border bg-bg-toolbar px-3 py-2 font-mono text-xs"
		>
			<span class="flex-1 break-all">{secret}</span>
			<CopyButton value={secret} label="secret" />
		</div>
		<Checkbox bind:checked={acknowledged} label="I have copied this value" />
	</div>
	{#snippet footer()}
		<Button variant="primary" onclick={close} disabled={!acknowledged}>Done</Button>
	{/snippet}
</Modal>
