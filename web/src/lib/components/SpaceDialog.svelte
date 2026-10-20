<script lang="ts">
	import { m } from '$lib/paraglide/messages';
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

<Modal bind:open title={editing ? m.components_spacedialog_rename_space() : m.content_new_space()}>
	<div class="flex flex-col gap-3">
		<Input
			label={m.common_name()}
			bind:value={name}
			placeholder={m.components_spacedialog_finance()}
		/>
		<Textarea
			label={m.common_description()}
			bind:value={description}
			rows={2}
			placeholder={m.common_optional()}
		/>
		<Select
			label={m.components_spacedialog_visibility()}
			bind:value={visibility}
			options={[
				{ value: 'workspace', label: m.components_spacedialog_workspace_every_member_can_see_it() },
				{ value: 'restricted', label: m.components_spacedialog_restricted_only_the_access_list() }
			]}
		/>
		<Select
			label={m.common_default_role()}
			bind:value={defaultRole}
			options={[
				{ value: 'viewer', label: m.components_spacedialog_viewer_read_and_run() },
				{ value: 'editor', label: m.components_spacedialog_editor_create_and_change_content() }
			]}
		/>
		{#if error}
			<p class="text-[11px] text-danger" role="alert">{error}</p>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (open = false)} disabled={busy}>{m.common_cancel()}</Button>
			<Button variant="primary" onclick={save} disabled={!name.trim() || busy} loading={busy}>
				{editing ? 'Save' : 'Create'}
			</Button>
		</div>
	{/snippet}
</Modal>
