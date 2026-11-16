<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { goto } from '$app/navigation';
	import { apiErrorMessage } from '$lib/api/errors';
	import Modal from '$lib/ui/Modal.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import { searchWorkspace, type WorkspaceSearchItem } from './client';
	import { searchItemHref, searchKindLabel } from './items';

	// The global search surface (#1061): one ranked list over everything the
	// caller may see in the current workspace. It reuses the federated search
	// endpoint and its ranking; the workspace and the glossary are applied on
	// the server, so a result is never outside the caller's visibility.
	let {
		open = $bindable(false),
		workspaceId,
		workspaceName
	}: {
		open?: boolean;
		workspaceId: string;
		workspaceName?: string;
	} = $props();

	/** The server ignores shorter queries; the surface says so instead. */
	const minQuery = 2;
	const resultLimit = 25;

	let query = $state('');
	let items = $state<WorkspaceSearchItem[]>([]);
	let expandedWith = $state<string[]>([]);
	let loading = $state(false);
	let error = $state('');
	let active = $state(0);
	let retryTick = $state(0);
	let input: HTMLInputElement | undefined = $state();

	// A combobox (#472): the input keeps the focus and names the active option
	// through aria-activedescendant.
	const uid = $props.id();
	const listId = `${uid}-list`;
	const optionId = (index: number) => `${uid}-option-${index}`;
	const activeId = $derived(!loading && !error && items[active] ? optionId(active) : undefined);
	const tooShort = $derived(query.trim().length < minQuery);

	// Reset the previous search when the dialog opens.
	$effect(() => {
		if (!open) return;
		query = '';
		items = [];
		expandedWith = [];
		error = '';
		active = 0;
		loading = false;
		setTimeout(() => input?.focus(), 0);
	});

	$effect(() => {
		if (!open) return;
		const q = query.trim();
		void retryTick;
		if (q.length < minQuery || !workspaceId) {
			items = [];
			expandedWith = [];
			error = '';
			loading = false;
			return;
		}
		const controller = new AbortController();
		let cancelled = false;
		loading = true;
		error = '';
		const timer = setTimeout(async () => {
			try {
				const found = await searchWorkspace(workspaceId, q, {
					limit: resultLimit,
					signal: controller.signal
				});
				if (cancelled) return;
				items = found.items;
				expandedWith = found.expanded_with;
				active = 0;
			} catch (e) {
				if (cancelled) return;
				error = apiErrorMessage(e, m.search_failed());
				items = [];
				expandedWith = [];
			} finally {
				if (!cancelled) loading = false;
			}
		}, 150);
		return () => {
			cancelled = true;
			controller.abort();
			clearTimeout(timer);
		};
	});

	function moveTo(index: number) {
		active = index;
		document.getElementById(optionId(index))?.scrollIntoView?.({ block: 'nearest' });
	}

	function onkeydown(event: KeyboardEvent) {
		if (event.key === 'ArrowDown') {
			event.preventDefault();
			if (items.length) moveTo(Math.min(active + 1, items.length - 1));
		} else if (event.key === 'ArrowUp') {
			event.preventDefault();
			if (items.length) moveTo(Math.max(active - 1, 0));
		} else if (event.key === 'Enter') {
			event.preventDefault();
			void choose(items[active]);
		}
	}

	async function choose(item: WorkspaceSearchItem | undefined) {
		if (!item) return;
		const href = searchItemHref(item);
		if (!href) return;
		open = false;
		// The href is dynamic (a table's query string, or a content route).
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		await goto(href);
	}
</script>

<Modal bind:open title={m.search_global_title()} size="lg">
	<div class="flex flex-col gap-3">
		<input
			bind:this={input}
			bind:value={query}
			{onkeydown}
			class="h-10 w-full rounded-md border border-border bg-bg-toolbar px-3 text-sm outline-none focus:border-primary"
			placeholder={m.search_global_placeholder()}
			aria-label={m.search_global_title()}
			role="combobox"
			aria-expanded={!tooShort && !error && items.length > 0}
			aria-controls={listId}
			aria-autocomplete="list"
			aria-activedescendant={activeId}
		/>

		{#if tooShort}
			<p class="px-1 text-[12px] text-text-muted" role="status">{m.search_global_hint()}</p>
		{:else if loading}
			<p class="px-1 text-[12px] text-text-muted" role="status">{m.common_searching()}</p>
		{:else if error}
			<ErrorState title={m.search_failed()} message={error} onretry={() => (retryTick += 1)} />
		{:else if items.length === 0}
			<p class="px-1 text-[12px] text-text-muted" role="status">
				{m.workbench_commandpalette_no_matches()}
			</p>
		{:else}
			{#if expandedWith.length > 0}
				<p class="px-1 text-[11px] text-text-muted">
					{m.search_expanded_with({ terms: expandedWith.join(', ') })}
				</p>
			{/if}
			<div
				id={listId}
				class="max-h-[55vh] overflow-y-auto"
				role="listbox"
				aria-label={m.common_results()}
			>
				{#each items as item, index (item.id)}
					<button
						type="button"
						role="option"
						id={optionId(index)}
						tabindex="-1"
						aria-selected={active === index}
						class="flex w-full items-center justify-between gap-2 rounded px-2 py-2 text-left text-[12px] {active ===
						index
							? 'bg-primary/10 text-primary'
							: 'hover:bg-slate-100 dark:hover:bg-[#1c2128]'}"
						onmousemove={() => (active = index)}
						onclick={() => choose(item)}
					>
						<span class="min-w-0 flex-1">
							<span class="block truncate font-medium">{item.title}</span>
							{#if item.subtitle}
								<span class="block truncate text-[10px] text-text-muted">{item.subtitle}</span>
							{/if}
						</span>
						<span class="shrink-0 text-[9px] uppercase text-text-muted">
							{searchKindLabel(item.kind)}
						</span>
					</button>
				{/each}
			</div>
		{/if}

		{#if workspaceName}
			<p class="px-1 text-[10px] text-text-muted">
				{m.search_global_in_workspace({ workspace: workspaceName })}
			</p>
		{/if}
	</div>
</Modal>
