<script lang="ts">
	// The workspace's search glossary (#943): sets of words that mean the same.
	// Searching for one also looks for the others in the command palette, the
	// sidebar and the agents' search tool. Any member reads it; editors change
	// it, and the server says which (allowed_actions), not the role.
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { m } from '$lib/paraglide/messages';
	import {
		addGlossaryEntry,
		loadGlossary,
		removeGlossaryEntry,
		SearchRequestError,
		type SearchGlossary,
		type SearchGlossaryEntry
	} from '$lib/search/client';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import { toasts } from '$lib/ui/toast.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	let glossary = $state.raw<SearchGlossary | null>(null);
	let loading = $state(true);
	let loadError = $state('');

	let term = $state('');
	let synonyms = $state('');
	let saving = $state(false);
	let formError = $state('');

	let removeOpen = $state(false);
	let removing = $state<SearchGlossaryEntry | null>(null);

	const workspaceId = $derived(workspaceStore.current?.id ?? '');
	const canEdit = $derived(glossary?.allowed_actions.includes('edit') ?? false);

	async function load(id: string) {
		loading = true;
		loadError = '';
		try {
			glossary = await loadGlossary(id);
		} catch (e) {
			loadError = e instanceof Error ? e.message : m.search_glossary_load_failed();
		} finally {
			loading = false;
		}
	}

	/** The synonyms typed as "a, b, c", without empty ones. */
	function typedSynonyms(): string[] {
		return synonyms
			.split(',')
			.map((word) => word.trim())
			.filter(Boolean);
	}

	async function add(event: SubmitEvent) {
		event.preventDefault();
		if (saving || !workspaceId) return;
		saving = true;
		formError = '';
		try {
			await addGlossaryEntry(workspaceId, term.trim(), typedSynonyms());
			term = '';
			synonyms = '';
			toasts.success(m.search_glossary_added());
			await load(workspaceId);
		} catch (e) {
			formError =
				e instanceof SearchRequestError && e.status === 409
					? m.search_glossary_duplicate()
					: e instanceof Error
						? e.message
						: m.search_glossary_add_failed();
		} finally {
			saving = false;
		}
	}

	async function remove() {
		if (!removing || !workspaceId) return;
		await removeGlossaryEntry(workspaceId, removing.id);
		removing = null;
		toasts.success(m.search_glossary_removed());
		await load(workspaceId);
	}

	onMount(() => {
		void workspaceStore.load();
	});

	// Loads again when the workspace the person works in changes.
	$effect(() => {
		if (workspaceId) void load(workspaceId);
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4">
		<a
			class="inline-flex items-center gap-1 text-[12px] font-semibold text-primary hover:underline"
			href={resolve('/models')}
		>
			<svg
				viewBox="0 0 16 16"
				class="h-3 w-3"
				fill="none"
				stroke="currentColor"
				stroke-width="2"
				stroke-linecap="round"
				stroke-linejoin="round"
				aria-hidden="true"><path d="m10 4-4 4 4 4" /></svg
			>
			{m.models_title()}
		</a>
		<h1 class="mt-1 text-xl font-semibold tracking-[-0.3px]">{m.search_glossary_title()}</h1>
		<p class="mt-1 max-w-2xl text-[13px] text-text-muted">{m.search_glossary_intro()}</p>
	</div>

	{#if !workspaceId}
		<EmptyState title={m.search_glossary_no_workspace()} />
	{:else if loading}
		<Skeleton height="6rem" />
	{:else if loadError}
		<ErrorState message={loadError} onretry={() => load(workspaceId)} />
	{:else if glossary}
		{#if canEdit}
			<form
				class="mb-4 grid items-end gap-x-2 gap-y-1 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-[14rem_1fr_auto]"
				onsubmit={add}
			>
				<Input label={m.search_glossary_term()} bind:value={term} required />
				<Input label={m.search_glossary_synonyms()} bind:value={synonyms} required />
				<Button type="submit" variant="primary" disabled={saving}>{m.search_glossary_add()}</Button>
				<p class="text-[11px] text-text-muted sm:col-span-3">
					{m.search_glossary_synonyms_hint()}
				</p>
				{#if formError}
					<p role="alert" class="text-[11px] text-danger sm:col-span-3">{formError}</p>
				{/if}
			</form>
		{:else}
			<p class="mb-4 text-[12px] text-text-muted">{m.search_glossary_read_only()}</p>
		{/if}

		{#if glossary.entries.length === 0}
			<EmptyState title={m.search_glossary_none()} description={m.search_glossary_none_hint()} />
		{:else}
			<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
				<table class="w-full text-left text-[11px]">
					<thead
						class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
					>
						<tr>
							<th class="px-3 py-2">{m.search_glossary_term()}</th>
							<th class="px-3 py-2">{m.search_glossary_synonyms()}</th>
							<th class="px-3 py-2">{m.search_glossary_created()}</th>
							<th class="px-3 py-2"><span class="sr-only">{m.common_action()}</span></th>
						</tr>
					</thead>
					<tbody>
						{#each glossary.entries as entry (entry.id)}
							<tr class="border-b border-border/60 last:border-b-0">
								<td class="px-3 py-1.5 font-medium">{entry.term}</td>
								<td class="px-3 py-1.5">{entry.synonyms.join(', ')}</td>
								<td class="px-3 py-1.5"><RelativeTime at={entry.created_at} /></td>
								<td class="px-3 py-1.5 text-right">
									{#if canEdit}
										<Button
											size="sm"
											variant="danger"
											onclick={() => {
												removing = entry;
												removeOpen = true;
											}}
										>
											{m.search_glossary_remove()}
										</Button>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	{/if}
</div>

{#if removing}
	<ConfirmDialog
		bind:open={removeOpen}
		title={m.search_glossary_remove_title()}
		resourceName={removing.term}
		consequences={[
			m.search_glossary_remove_consequence({
				term: removing.term,
				synonyms: removing.synonyms.join(', ')
			})
		]}
		confirmLabel={m.search_glossary_remove()}
		onconfirm={remove}
	/>
{/if}
