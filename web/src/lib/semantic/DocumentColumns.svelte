<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { normalizeTags, tagValid, upsertDoc, type MetadataColumn } from '$lib/semantic/docs';
	import Button from '$lib/ui/Button.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Textarea from '$lib/ui/Textarea.svelte';
	import Input from '$lib/ui/Input.svelte';

	let {
		open = $bindable(false),
		catalogId,
		namespace,
		table,
		columns = [],
		onSaved
	}: {
		open?: boolean;
		catalogId: string;
		namespace: string[];
		table: string;
		columns?: MetadataColumn[];
		onSaved?: () => void;
	} = $props();

	const workspaceId = $derived(workspaceStore.current?.id ?? '');

	type Row = {
		name: string;
		description: string;
		tags: string;
		originalDescription: string;
		originalTags: string;
	};

	let rows = $state<Row[]>([]);
	let saving = $state(false);
	let progress = $state('');
	let failures = $state<{ column: string; message: string }[]>([]);

	$effect(() => {
		if (!open) return;
		failures = [];
		progress = '';
		rows = columns.map((column) => {
			const description = column.doc?.description ?? '';
			const tags = (column.doc?.tags ?? []).join(', ');
			return {
				name: column.name,
				description,
				tags,
				originalDescription: description,
				originalTags: tags
			};
		});
	});

	async function saveAll() {
		if (!workspaceId) return;
		saving = true;
		failures = [];
		const changed = rows.filter(
			(row) => row.description !== row.originalDescription || row.tags !== row.originalTags
		);
		let done = 0;
		for (const row of changed) {
			done++;
			progress = `Saving ${done} of ${changed.length}: ${row.name}`;
			const tags = normalizeTags(row.tags);
			const invalid = tags.find((tag) => !tagValid(tag));
			if (invalid) {
				failures = [...failures, { column: row.name, message: `invalid tag ${invalid}` }];
				continue;
			}
			try {
				await upsertDoc(
					workspaceId,
					{ catalogId, namespace, table, column: row.name },
					{ description: row.description, tags }
				);
			} catch (e) {
				failures = [
					...failures,
					{ column: row.name, message: e instanceof Error ? e.message : String(e) }
				];
			}
		}
		progress = '';
		saving = false;
		if (failures.length === 0) {
			open = false;
			onSaved?.();
		}
	}
</script>

<Modal bind:open title={m.semantic_documentcolumns_document_columns()}>
	<div class="flex flex-col gap-3 text-[12px]">
		<p class="text-[11px] text-slate-600 dark:text-slate-300">
			{m.semantic_documentcolumns_only_changed_columns_are_saved_failures_ar()}
		</p>
		<div class="max-h-[60vh] overflow-y-auto">
			<table class="w-full text-left text-[11px]">
				<thead
					class="sticky top-0 bg-bg-panel text-[10px] uppercase text-slate-600 dark:text-slate-300"
				>
					<tr>
						<th scope="col" class="w-32 px-1 py-1">{m.common_column()}</th>
						<th scope="col" class="px-1 py-1">{m.common_description()}</th>
						<th scope="col" class="w-40 px-1 py-1">{m.semantic_documentcolumns_tags()}</th>
					</tr>
				</thead>
				<tbody>
					{#each rows as row (row.name)}
						<tr class="border-t border-border/60 align-top">
							<th scope="row" class="px-1 py-1 font-mono font-normal">{row.name}</th>
							<td class="px-1 py-1">
								<Textarea
									bind:value={row.description}
									rows={2}
									ariaLabel={m.semantic_documentcolumns_description_of({ column: row.name })}
								/>
							</td>
							<td class="px-1 py-1">
								<Input
									bind:value={row.tags}
									placeholder={m.semantic_documentcolumns_tag_tag()}
									ariaLabel={m.semantic_documentcolumns_tags_of({ column: row.name })}
								/>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		{#if progress}<p class="text-[11px] text-slate-600 dark:text-slate-300">{progress}</p>{/if}
		{#if failures.length > 0}
			<ul class="text-[11px] text-danger" role="alert">
				{#each failures as failure (failure.column)}
					<li>{failure.column}: {failure.message}</li>
				{/each}
			</ul>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (open = false)} disabled={saving}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={saving} disabled={saving} onclick={saveAll}
				>{m.semantic_documentcolumns_save_all()}</Button
			>
		</div>
	{/snippet}
</Modal>
