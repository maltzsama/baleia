<script lang="ts">
	import Button from '$lib/ui/Button.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import { savedQueries, type SavedQuery } from '$lib/stores/savedQueries.svelte';

	let { open = $bindable(false), query }: { open?: boolean; query: SavedQuery | null } = $props();

	let target = $state('');
	let busy = $state(false);
	let error = $state('');

	const options = $derived(
		savedQueries.spaces
			.filter((s) => s.id !== query?.space_id && savedQueries.canEdit(s.id))
			.map((s) => ({ value: s.id, label: s.name }))
	);

	$effect(() => {
		if (!open) return;
		target = '';
		error = '';
	});

	async function move() {
		if (!query || !target || busy) return;
		busy = true;
		error = '';
		const res = await savedQueries.moveQuery(query, target);
		busy = false;
		if (res.error) {
			error = res.error;
			return;
		}
		open = false;
	}
</script>

<Modal bind:open title="Move to space">
	<div class="flex flex-col gap-3">
		<p class="text-xs text-text-muted">
			Move <strong>{query?.name}</strong> to another space where you can edit.
		</p>
		{#if options.length === 0}
			<p class="text-xs text-text-muted">There is no other space you can edit.</p>
		{:else}
			<Select
				label="Destination space"
				bind:value={target}
				{options}
				placeholder="Choose a space"
			/>
		{/if}
		{#if error}
			<p class="text-[11px] text-danger" role="alert">{error}</p>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (open = false)} disabled={busy}>Cancel</Button>
			<Button variant="primary" onclick={move} disabled={!target || busy} loading={busy}>
				Move
			</Button>
		</div>
	{/snippet}
</Modal>
