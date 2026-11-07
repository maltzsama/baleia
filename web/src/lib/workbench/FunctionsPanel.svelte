<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { loadBindingFunctions, type DialectFunction } from '$lib/engines/client';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';

	let {
		workspaceId,
		catalogId,
		bindingId,
		oninsert
	}: {
		workspaceId: string;
		catalogId: string;
		bindingId: string;
		oninsert: (fn: DialectFunction) => void;
	} = $props();

	let functions = $state<DialectFunction[]>([]);
	let loading = $state(true);
	let error = $state('');
	let query = $state('');
	let kind = $state('all');

	$effect(() => {
		const ws = workspaceId;
		const cat = catalogId;
		const bind = bindingId;
		if (!ws || !cat || !bind) {
			functions = [];
			loading = false;
			error = '';
			return;
		}
		let cancelled = false;
		loading = true;
		error = '';
		void loadBindingFunctions(ws, cat, bind)
			.then((data) => {
				if (!cancelled) functions = data ?? [];
			})
			.catch((e) => {
				if (!cancelled) error = e instanceof Error ? e.message : String(e);
			})
			.finally(() => {
				if (!cancelled) loading = false;
			});
		return () => {
			cancelled = true;
		};
	});

	const kinds = $derived([...new Set(functions.map((fn) => fn.kind))].sort());
	const shown = $derived.by(() => {
		const q = query.trim().toLowerCase();
		return functions
			.filter(
				(fn) =>
					(kind === 'all' || fn.kind === kind) &&
					(!q ||
						matches(fn.name, q) ||
						(fn.signature?.toLowerCase().includes(q) ?? false) ||
						(fn.description?.toLowerCase().includes(q) ?? false))
			)
			.slice(0, 300);
	});

	// matches finds a function by name, also when the query is a verb form:
	// "truncate" finds date_trunc (#556).
	function matches(name: string, query: string): boolean {
		const n = name.toLowerCase();
		if (n.includes(query) || query.includes(n)) return true;
		const stem = query.replace(/(ate|ing|ed|es|s)$/, '');
		return stem.length >= 3 && n.includes(stem);
	}
</script>

<div class="flex h-full flex-col gap-3">
	<div class="flex flex-wrap items-center gap-2">
		<input
			type="search"
			placeholder={m.functions_panel_search()}
			aria-label={m.functions_panel_search()}
			bind:value={query}
			class="min-w-[180px] flex-1 rounded-md border border-border bg-bg-toolbar px-2 py-1 text-xs text-text-main outline-none focus:border-primary"
		/>
		<label class="flex items-center gap-1 text-[11px] text-text-muted">
			{m.functions_panel_kind()}
			<select
				bind:value={kind}
				class="rounded-md border border-border bg-bg-toolbar px-1.5 py-1 text-xs text-text-main"
			>
				<option value="all">{m.functions_panel_all()}</option>
				{#each kinds as k (k)}
					<option value={k}>{k}</option>
				{/each}
			</select>
		</label>
	</div>
	<p class="text-[11px] text-text-muted">{m.functions_panel_insert_hint()}</p>

	{#if loading}
		<div class="space-y-2" role="status" aria-label={m.functions_panel_title()}>
			<Skeleton class="h-6 w-full" />
			<Skeleton class="h-6 w-5/6" />
		</div>
	{:else if error}
		<ErrorState title={m.functions_panel_failed()} message={error} />
	{:else if shown.length === 0}
		<EmptyState title={m.functions_panel_empty()} description={m.functions_panel_search()} />
	{:else}
		<ul class="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto">
			{#each shown as fn (fn.name)}
				<li>
					<button
						type="button"
						class="flex w-full flex-col gap-0.5 rounded-md px-2 py-1 text-left hover:bg-bg-toolbar"
						onclick={() => oninsert(fn)}
					>
						<span class="font-mono text-xs text-text-main">{fn.name}</span>
						{#if fn.signature}
							<span class="font-mono text-[11px] text-text-muted">{fn.signature}</span>
						{/if}
						{#if fn.description}
							<span class="text-[11px] text-text-muted">{fn.description}</span>
						{/if}
					</button>
				</li>
			{/each}
		</ul>
	{/if}
</div>
