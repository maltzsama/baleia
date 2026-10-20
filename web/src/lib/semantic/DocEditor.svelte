<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { listPrincipals } from '$lib/content/client';
	import {
		DocRequestError,
		normalizeTags,
		tagValid,
		upsertDoc,
		type SemanticDoc
	} from '$lib/semantic/docs';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import MarkdownEditor from '$lib/ui/patterns/MarkdownEditor.svelte';

	let {
		open = $bindable(false),
		catalogId,
		namespace,
		table,
		column = null,
		doc = null,
		onSaved
	}: {
		open?: boolean;
		catalogId: string;
		namespace: string[];
		table: string;
		column?: string | null;
		doc?: SemanticDoc | null;
		onSaved?: () => void;
	} = $props();

	const workspaceId = $derived(workspaceStore.current?.id ?? '');

	let description = $state('');
	let tags = $state('');
	let ownerId = $state('');
	let certified = $state(false);
	let users = $state<{ id: string; name: string }[]>([]);
	let saving = $state(false);
	let error = $state('');
	let conflict = $state(false);
	let overwrite = $state(false);

	const canCertify = $derived(!!doc?.allowed_actions?.includes('certify'));

	$effect(() => {
		if (!open) return;
		description = doc?.description ?? '';
		tags = (doc?.tags ?? []).join(', ');
		ownerId = doc?.owner_user_id ?? '';
		certified = doc?.certified ?? false;
		error = '';
		conflict = false;
		overwrite = false;
		if (workspaceId && users.length === 0) {
			void listPrincipals(workspaceId, '', 'user')
				.then((rows) => (users = rows.map((row) => ({ id: row.id, name: row.name }))))
				.catch(() => (users = []));
		}
	});

	async function save() {
		const parsed = normalizeTags(tags);
		if (parsed.length > 20) {
			error = m.semantic_doceditor_at_most_20_tags_are_allowed();
			return;
		}
		const invalid = parsed.find((tag) => !tagValid(tag));
		if (invalid) {
			error = `Tag "${invalid}" must match ^[a-z0-9][a-z0-9_-]{0,39}$.`;
			return;
		}
		saving = true;
		error = '';
		try {
			await upsertDoc(
				workspaceId,
				{ catalogId, namespace, table, column },
				{
					description,
					tags: parsed,
					owner_user_id: ownerId || null,
					certified: canCertify ? certified : undefined,
					expected_updated_at: overwrite ? null : (doc?.updated_at ?? null)
				}
			);
			open = false;
			onSaved?.();
		} catch (e) {
			if (e instanceof DocRequestError && e.code === 'version_conflict') {
				conflict = true;
			}
			error = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}
</script>

<Modal bind:open title={column ? `Document column ${column}` : 'About this table'}>
	<div class="flex flex-col gap-3 text-[12px]">
		<MarkdownEditor bind:value={description} />
		<Input
			label={m.semantic_doceditor_tags_comma_separated()}
			bind:value={tags}
			placeholder={m.semantic_doceditor_sales_revenue()}
		/>
		{#if users.length > 0}
			<Select
				label={m.common_owner()}
				bind:value={ownerId}
				options={[
					{ value: '', label: m.semantic_doceditor_no_owner() },
					...users.map((user) => ({ value: user.id, label: user.name }))
				]}
			/>
		{:else}
			<Input label="Owner user id" bind:value={ownerId} class="font-mono" />
		{/if}
		{#if canCertify}
			<Checkbox label={m.common_certified()} bind:checked={certified} />
		{:else if doc?.certified}
			<p class="text-[11px] text-amber-700 dark:text-amber-300">
				This doc is certified. Changing the description removes the certification.
			</p>
		{/if}
		{#if conflict}
			<p class="text-[11px] text-amber-700 dark:text-amber-300">
				{m.semantic_doceditor_someone_else_changed_this_doc_reload_to_se()}
			</p>
			<div class="flex gap-2">
				<Button
					size="sm"
					onclick={() => {
						description = doc?.description ?? '';
						tags = (doc?.tags ?? []).join(', ');
						conflict = false;
						error = '';
					}}
				>
					{m.semantic_doceditor_reload()}
				</Button>
				<Button
					size="sm"
					onclick={() => {
						overwrite = true;
						void save();
					}}
				>
					{m.common_overwrite()}
				</Button>
			</div>
		{/if}
		{#if error}<p class="text-danger" role="alert">{error}</p>{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (open = false)} disabled={saving}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={saving} disabled={saving} onclick={save}
				>{m.common_save()}</Button
			>
		</div>
	{/snippet}
</Modal>
