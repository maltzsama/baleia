<script lang="ts">
	import { m } from '$lib/paraglide/messages';
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

<Modal bind:open title={m.components_movetospacedialog_move_to_space()}>
	<div class="flex flex-col gap-3">
		<p class="text-xs text-text-muted">
			{m.common_move()} <strong>{query?.name}</strong>
			{m.components_movetospacedialog_to_another_space_where_you_can_edit()}
		</p>
		{#if options.length === 0}
			<p class="text-xs text-text-muted">
				{m.components_movetospacedialog_there_is_no_other_space_you_can_edit()}
			</p>
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
			<Button onclick={() => (open = false)} disabled={busy}>{m.common_cancel()}</Button>
			<Button variant="primary" onclick={move} disabled={!target || busy} loading={busy}>
				{m.common_move()}
			</Button>
		</div>
	{/snippet}
</Modal>
