<script lang="ts">
	import { apiErrorMessage } from '$lib/api/client';
	import Button from '../Button.svelte';
	import Input from '../Input.svelte';
	import Modal from '../Modal.svelte';

	let {
		open = $bindable(false),
		title,
		resourceName,
		consequences = [],
		confirmLabel = 'Confirm',
		typeToConfirm = false,
		danger = true,
		onconfirm
	}: {
		open?: boolean;
		title: string;
		resourceName: string;
		consequences?: string[];
		confirmLabel?: string;
		typeToConfirm?: boolean;
		danger?: boolean;
		onconfirm: () => Promise<void>;
	} = $props();

	let typed = $state('');
	let busy = $state(false);
	let error = $state('');

	const canConfirm = $derived(!typeToConfirm || typed === resourceName);

	async function confirm() {
		if (!canConfirm || busy) return;
		busy = true;
		error = '';
		try {
			await onconfirm();
			open = false;
			typed = '';
		} catch (e) {
			error = apiErrorMessage(e, 'The action failed');
		} finally {
			busy = false;
		}
	}
</script>

<Modal bind:open {title}>
	<div class="flex flex-col gap-3 text-[13px]">
		{#if consequences.length}
			<ul class="list-disc pl-5 leading-[1.55] text-text-muted">
				{#each consequences as item (item)}
					<li>{item}</li>
				{/each}
			</ul>
		{/if}
		{#if typeToConfirm}
			<Input label={`Type "${resourceName}" to confirm`} bind:value={typed} />
		{/if}
		{#if error}
			<p class="text-danger">{error}</p>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (open = false)} disabled={busy}>Cancel</Button>
			<Button
				variant={danger ? 'danger' : 'primary'}
				onclick={confirm}
				disabled={!canConfirm || busy}
				loading={busy}
			>
				{confirmLabel}
			</Button>
		</div>
	{/snippet}
</Modal>
