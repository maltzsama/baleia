<script lang="ts">
	import { resolve } from '$app/paths';
	import { goto } from '$app/navigation';
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import { listPrincipals } from '$lib/content/client';
	import {
		listTags,
		searchDocs,
		type SemanticDocSearchResult,
		type SemanticTag
	} from '$lib/semantic/docs';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';

	const workspaceId = $derived(workspaceStore.current?.id ?? '');

	let q = $state('');
	let tag = $state('');
	let certified = $state(false);
	let ownerId = $state('');
	let tags = $state<SemanticTag[]>([]);
	let owners = $state<{ id: string; name: string }[]>([]);
	let results = $state<SemanticDocSearchResult[]>([]);
	let loading = $state(false);
	let searched = $state(false);
	let error = $state('');

	async function search() {
		if (!workspaceId || q.trim().length < 2) return;
		loading = true;
		error = '';
		searched = true;
		try {
			results = await searchDocs(workspaceId, {
				q: q.trim(),
				tag: tag || undefined,
				certified: certified || undefined,
				owner_id: ownerId || undefined
			});
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	// Opens the table in the Data explorer; a column result also names the
	// column, which the explorer expands and focuses (#474).
	function open(result: SemanticDocSearchResult) {
		const pairs: [string, string][] = [
			['catalog', result.catalog_id],
			...result.namespace.map((part): [string, string] => ['ns', part]),
			['table', result.table],
			...(result.column ? [['column', result.column] as [string, string]] : [])
		];
		const query = pairs.map(([key, value]) => `${key}=${encodeURIComponent(value)}`).join('&');
		// The query string is dynamic, so resolve() cannot type the whole URL.
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(`${resolve('/data')}?${query}`);
	}

	function excerptParts(description: string): { text: string; match: boolean }[] {
		const plain = description
			.replace(/<[^>]*>/g, '')
			.replace(/[#*_`>[\]!-]/g, ' ')
			.replace(/\s+/g, ' ')
			.trim();
		const needle = q.trim();
		if (!needle) return [{ text: plain.slice(0, 220), match: false }];
		const index = plain.toLowerCase().indexOf(needle.toLowerCase());
		const start = Math.max(0, index - 80);
		const slice = plain.slice(start, start + 240);
		const parts: { text: string; match: boolean }[] = [];
		const re = new RegExp(needle.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'ig');
		let pos = 0;
		let match: RegExpExecArray | null;
		while ((match = re.exec(slice)) !== null) {
			if (match.index > pos) parts.push({ text: slice.slice(pos, match.index), match: false });
			parts.push({ text: match[0], match: true });
			pos = match.index + match[0].length;
			if (match[0].length === 0) re.lastIndex++;
		}
		if (pos < slice.length) parts.push({ text: slice.slice(pos), match: false });
		return parts;
	}

	onMount(async () => {
		await workspaceStore.load();
		if (!workspaceId) return;
		try {
			tags = await listTags(workspaceId);
			const users = await listPrincipals(workspaceId, '', 'user');
			owners = users.map((user) => ({ id: user.id, name: user.name }));
		} catch {
			// Filters still work without suggestions.
		}
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.common_docs()}</h1>
		<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
			{m.data_docs_search_table_and_column_documentation_by_t()}
		</p>
	</div>

	<div
		class="mb-4 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-2 lg:grid-cols-5"
	>
		<Input label={m.common_search()} bind:value={q} placeholder={m.data_docs_revenue()} />
		<Select
			label={m.data_docs_tag()}
			bind:value={tag}
			options={[
				{ value: '', label: m.monitoring_any() },
				...tags.map((item) => ({ value: item.tag, label: `${item.tag} (${item.count})` }))
			]}
		/>
		<Select
			label={m.common_owner()}
			bind:value={ownerId}
			options={[
				{ value: '', label: m.monitoring_any() },
				...owners.map((owner) => ({ value: owner.id, label: owner.name }))
			]}
		/>
		<label class="flex items-end gap-2 pb-1 text-[12px]">
			<input type="checkbox" bind:checked={certified} />
			{m.data_docs_certified_only()}
		</label>
		<div class="flex items-end">
			<Button variant="primary" {loading} disabled={loading} onclick={search}
				>{m.common_search()}</Button
			>
		</div>
	</div>

	{#if error}
		<ErrorState title={m.data_docs_could_not_search_docs()} message={error} onretry={search} />
	{:else if loading}
		<Skeleton class="h-32 w-full" />
	{:else if searched && results.length === 0}
		<EmptyState
			title="No documentation matches"
			description="Try different words or remove a filter."
		/>
	{:else if !searched}
		<EmptyState
			title="Search the documentation"
			description="Type at least two characters and press Search."
		/>
	{:else}
		<div class="flex flex-col gap-2">
			{#each results as result (result.id)}
				<article class="rounded-lg border border-border bg-bg-panel p-3">
					<div class="flex flex-wrap items-center gap-2">
						<button
							type="button"
							class="text-left text-[13px] font-semibold hover:underline"
							onclick={() => open(result)}
						>
							{result.namespace.join('.')}.{result.table}{#if result.column}
								· {result.column}{/if}
						</button>
						{#if result.certified}<Badge tone="success">Certified</Badge>{/if}
						<span class="font-mono text-[10px] text-slate-600 dark:text-slate-300"
							>{result.catalog_id}</span
						>
					</div>
					<p class="mt-1 line-clamp-3 text-[11px] text-slate-600 dark:text-slate-300">
						{#each excerptParts(result.description) as part, index (index)}
							{#if part.match}<mark>{part.text}</mark>{:else}{part.text}{/if}
						{/each}
					</p>
					<div class="mt-1 flex flex-wrap gap-1">
						{#each result.tags as item (item)}
							<Badge tone="info">{item}</Badge>
						{/each}
					</div>
				</article>
			{/each}
		</div>
	{/if}
</div>
