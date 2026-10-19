<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Button from '../Button.svelte';
	import Checkbox from '../Checkbox.svelte';
	import Modal from '../Modal.svelte';
	import CopyButton from './CopyButton.svelte';

	let {
		open = $bindable(false),
		secret,
		title = m.onetimesecret_copy_this_secret_now(),
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
		<p class="text-text-muted">{m.common_this_value_is_shown_once_and_is_never_stor()}</p>
		<div
			class="flex items-center gap-2 rounded-md border border-border bg-bg-toolbar px-3 py-2 font-mono text-xs"
		>
			<span class="flex-1 break-all">{secret}</span>
			<CopyButton value={secret} label={m.onetimesecret_secret()} />
		</div>
		<Checkbox bind:checked={acknowledged} label={m.common_i_have_copied_this_value()} />
	</div>
	{#snippet footer()}
		<Button variant="primary" onclick={close} disabled={!acknowledged}>{m.common_done()}</Button>
	{/snippet}
</Modal>
