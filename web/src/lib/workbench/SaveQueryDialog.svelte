<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { apiErrorMessage, client } from '$lib/api/client';
	import type { components } from '$lib/api/schema';
	import { savedQueries, type QueryFolder, type Space } from '$lib/stores/savedQueries.svelte';
	import type { EditorTab } from '$lib/stores/editor.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import type { Conflict } from './conflict';

	let {
		open = $bindable(false),
		tab,
		workspaceId,
		spaces,
		folders,
		mode = 'save',
		prefillName = '',
		onconflict,
		onsaved
	}: {
		open?: boolean;
		tab: EditorTab;
		workspaceId: string;
		spaces: Space[];
		folders: QueryFolder[];
		mode?: 'save' | 'copy';
		prefillName?: string;
		onconflict: (conflict: Conflict) => void;
		onsaved: (query: components['schemas']['SavedQuery']) => void;
	} = $props();

	function slugify(name: string): string {
		const base = name
			.toLowerCase()
			.normalize('NFKD')
			.replace(/[^a-z0-9]+/g, '_')
			.replace(/^_+|_+$/g, '')
			.slice(0, 63);
		if (!base) return '';
		return /^[a-z]/.test(base) ? base : `q_${base}`.slice(0, 63);
	}

	const editableSpaces = $derived(
		spaces.filter((space) => space.my_role === 'editor' || space.my_role === 'manager')
	);

	let name = $state('');
	let slug = $state('');
	let slugTouched = $state(false);
	let spaceId = $state('');
	let folderId = $state('');
	let rememberTarget = $state(true);
	let saving = $state(false);
	let error = $state('');
	let fieldError = $state<Record<string, string>>({});
	let wasOpen = $state(false);

	$effect(() => {
		// Reset only when the dialog opens, not when spaces/folders arrive late.
		if (open && !wasOpen) {
			const saved = tab.savedRef;
			name = prefillName || (mode === 'copy' ? `${tab.name} (copy)` : tab.name);
			slug = saved && mode === 'save' ? saved.slug : slugify(name);
			slugTouched = saved !== null && mode === 'save';
			spaceId =
				saved?.space_id && spaces.some((s) => s.id === saved.space_id)
					? saved.space_id
					: (editableSpaces[0]?.id ?? '');
			folderId = mode === 'save' ? (saved?.folder_id ?? '') : '';
			rememberTarget = true;
			error = '';
			fieldError = {};
		}
		wasOpen = open;
	});

	const spaceFolders = $derived(folders.filter((folder) => folder.space_id === spaceId));

	function onName(value: string) {
		name = value;
		if (!slugTouched) slug = slugify(value);
	}

	async function save() {
		if (!workspaceId || !name.trim() || !slug.trim()) return;
		saving = true;
		error = '';
		fieldError = {};
		try {
			const target =
				rememberTarget && tab.bindingId
					? { binding_id: tab.bindingId, ...(tab.schema ? { schema: tab.schema } : {}) }
					: undefined;
			const body: components['schemas']['UpdateSavedQueryRequest'] = {
				name: name.trim(),
				slug,
				space_id: spaceId || null,
				sql: tab.sql,
				target,
				expected_version: tab.savedRef?.version
			};
			if (folderId) body.folder_id = folderId;
			else body.move_to_root = true;

			const saved =
				tab.savedRef && mode === 'save'
					? await client.PATCH('/workspaces/{id}/queries/{query_id}', {
							params: { path: { id: workspaceId, query_id: tab.savedRef.id } },
							body
						})
					: await client.POST('/workspaces/{id}/queries', {
							params: { path: { id: workspaceId } },
							body: {
								name: name.trim(),
								slug,
								space_id: spaceId || null,
								sql: tab.sql,
								target,
								...(folderId ? { folder_id: folderId } : {})
							}
						});
			if (saved.error || !saved.data) {
				const err = saved.error as {
					code?: string;
					params?: {
						current_version?: number;
						updated_by?: { id?: string; name?: string };
						updated_at?: string;
					};
				};
				if (err?.code === 'version_conflict' && tab.savedRef) {
					const current = await client.GET('/workspaces/{id}/queries/{query_id}', {
						params: { path: { id: workspaceId, query_id: tab.savedRef.id } }
					});
					if (current.data) {
						open = false;
						onconflict({
							currentVersion: Number(err.params?.current_version ?? current.data.version),
							updatedBy: err.params?.updated_by,
							updatedAt: err.params?.updated_at,
							theirs: current.data
						});
						return;
					}
				}
				if (err?.code === 'name_conflict' || err?.code === 'slug_conflict_other_space') {
					fieldError = {
						name: apiErrorMessage(saved.error, m.workbench_savequerydialog_name_already_in_use())
					};
				} else {
					error = apiErrorMessage(
						saved.error,
						m.workbench_savequerydialog_could_not_save_the_query()
					);
				}
				return;
			}
			savedQueries.onSaved(saved.data);
			open = false;
			onsaved(saved.data);
		} finally {
			saving = false;
		}
	}
</script>

<Modal
	bind:open
	title={mode === 'copy'
		? m.components_queryeditor_save_as_a_copy()
		: m.workbench_savequerydialog_save_query()}
>
	<div class="flex flex-col gap-3">
		<Input
			label={m.workbench_savequerydialog_name()}
			value={name}
			error={fieldError.name}
			oninput={(e) => onName((e.target as HTMLInputElement).value)}
		/>
		<Input
			label={m.workbench_savequerydialog_slug()}
			value={slug}
			error={fieldError.slug}
			oninput={(e) => {
				slugTouched = true;
				slug = (e.target as HTMLInputElement).value;
			}}
			class="font-mono"
		/>
		<div class="grid gap-2 sm:grid-cols-2">
			<Select
				label={m.common_space()}
				bind:value={spaceId}
				options={editableSpaces.map((space) => ({ value: space.id, label: space.name }))}
			/>
			<Select
				label={m.workbench_savequerydialog_folder()}
				bind:value={folderId}
				options={[
					{ value: '', label: m.workbench_savequerydialog_space_root() },
					...spaceFolders.map((folder) => ({ value: folder.id, label: folder.name }))
				]}
			/>
		</div>
		<Checkbox
			label={m.workbench_savequerydialog_remember_where_this_runs()}
			bind:checked={rememberTarget}
		/>
		{#if error}
			<p class="text-[12px] text-danger" role="alert">{error}</p>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (open = false)} disabled={saving}>{m.common_cancel()}</Button>
			<Button
				variant="primary"
				loading={saving}
				disabled={saving || !name.trim() || !slug.trim()}
				onclick={save}
			>
				{m.common_save()}
			</Button>
		</div>
	{/snippet}
</Modal>
