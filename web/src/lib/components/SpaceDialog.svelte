<script lang="ts">
	import Button from '$lib/ui/Button.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Textarea from '$lib/ui/Textarea.svelte';
	import { savedQueries, type Space } from '$lib/stores/savedQueries.svelte';

	let {
		open = $bindable(false),
		space = null,
		ondone
	}: {
		open?: boolean;
		space?: Space | null;
		ondone?: () => void;
	} = $props();

	let name = $state('');
	let description = $state('');
	let visibility = $state('workspace');
	let defaultRole = $state('editor');
	let busy = $state(false);
	let error = $state('');

	const editing = $derived(space !== null);

	$effect(() => {
		if (!open) return;
		name = space?.name ?? '';
		description = space?.description ?? '';
		visibility = space?.visibility ?? 'workspace';
		defaultRole = space?.default_role ?? 'editor';
		error = '';
	});

	async function save() {
		if (busy || !name.trim()) return;
		busy = true;
		error = '';
		const body = {
			name: name.trim(),
			description: description.trim() || null,
			visibility: visibility as 'workspace' | 'restricted',
			default_role: defaultRole as 'viewer' | 'editor'
		};
		const res = editing
			? await savedQueries.updateSpace(space!.id, body)
			: await savedQueries.createSpace(body);
		busy = false;
		if (res.error) {
			error = res.error;
			return;
		}
		open = false;
		ondone?.();
	}
</script>

<Modal bind:open title={editing ? 'Rename space' : 'New space'}>
	<div class="flex flex-col gap-3">
		<Input label="Name" bind:value={name} placeholder="Finance" />
		<Textarea label="Description" bind:value={description} rows={2} placeholder="Optional" />
		<Select
			label="Visibility"
			bind:value={visibility}
			options={[
				{ value: 'workspace', label: 'Workspace — every member can see it' },
				{ value: 'restricted', label: 'Restricted — only the access list' }
			]}
		/>
		<Select
			label="Default role"
			bind:value={defaultRole}
			options={[
				{ value: 'viewer', label: 'Viewer — read and run' },
				{ value: 'editor', label: 'Editor — create and change content' }
			]}
		/>
		{#if error}
			<p class="text-[11px] text-danger" role="alert">{error}</p>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (open = false)} disabled={busy}>Cancel</Button>
			<Button variant="primary" onclick={save} disabled={!name.trim() || busy} loading={busy}>
				{editing ? 'Save' : 'Create'}
			</Button>
		</div>
	{/snippet}
</Modal>
