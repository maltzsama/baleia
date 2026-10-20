<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import type { components } from '$lib/api/schema';
	import { client } from '$lib/api/client';
	import { describeError } from '$lib/errors/catalog';
	import { poll, type PollController } from '$lib/util/poll';

	type Item = components['schemas']['AttentionItem'];

	let items = $state<Item[]>([]);
	let open = $state(false);
	let error = $state('');
	let controller: PollController<Item[]> | undefined;

	const failures = $derived(items.filter((item) => item.severity === 'error'));
	const warnings = $derived(items.filter((item) => item.severity === 'warning'));
	// Errors win the badge; warnings show only when there is no error.
	const count = $derived(failures.length > 0 ? failures.length : warnings.length);

	async function load(): Promise<Item[]> {
		try {
			const { data, error: err } = await client.GET('/attention');
			if (err || !data) throw new Error('attention');
			error = '';
			return data;
		} catch (err) {
			error = m.components_attentionbell_could_not_load_attention_items();
			throw err;
		}
	}

	async function refresh() {
		try {
			items = await load();
		} catch {
			/* error is set by load */
		}
	}

	onMount(() => {
		// FD10: the poll pauses while the tab is hidden and refreshes while the
		// attention panel is visible; focus also refreshes immediately.
		controller = poll(load, {
			until: () => false,
			intervals: [120_000],
			maxDurationMs: Number.POSITIVE_INFINITY,
			onValue: (value) => {
				items = value;
				error = '';
			}
		});
		const onFocus = () => void refresh();
		window.addEventListener('focus', onFocus);
		return () => {
			controller?.stop();
			window.removeEventListener('focus', onFocus);
		};
	});

	function message(kind: string): string {
		const described = describeError(`attention.${kind}`);
		return `${described.title}: ${described.explanation}`;
	}

	function toggle() {
		open = !open;
	}

	function close() {
		open = false;
	}

	function onkeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') close();
	}
</script>

<div class="relative">
	<button
		type="button"
		onclick={toggle}
		{onkeydown}
		aria-label={count > 0 ? `Attention: ${count} items` : 'Attention'}
		aria-expanded={open}
		aria-haspopup="dialog"
		title={m.components_attentionbell_attention()}
		class="relative w-9 h-9 flex items-center justify-center rounded-[10px] border-none bg-transparent cursor-pointer text-slate-400 transition-all duration-150 hover:bg-slate-800 hover:text-white"
	>
		<svg viewBox="0 0 24 24" class="w-4 h-4"
			><path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9" /><path
				d="M13.73 21a2 2 0 0 1-3.46 0"
			/></svg
		>
		{#if count > 0}
			<span
				class="absolute -right-0.5 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-danger px-1 text-[9px] font-bold text-white"
			>
				{count}
			</span>
		{/if}
	</button>

	{#if open}
		<div
			role="dialog"
			aria-label={m.components_attentionbell_attention()}
			tabindex="-1"
			{onkeydown}
			class="absolute left-full top-0 z-50 ml-2 flex max-h-[70vh] w-80 flex-col overflow-y-auto rounded-lg border border-border bg-bg-panel p-3 shadow-xl"
		>
			<div class="mb-2 flex items-center justify-between">
				<h2 class="text-[13px] font-semibold">{m.components_attentionbell_needs_attention()}</h2>
				<button
					type="button"
					onclick={close}
					aria-label={m.components_attentionbell_close_attention()}
					class="cursor-pointer border-none bg-transparent text-text-muted hover:text-text-main"
				>
					×
				</button>
			</div>

			{#if error}
				<p
					class="mb-2 rounded-md border border-danger/40 bg-danger/5 px-2 py-1 text-[11px] text-danger"
				>
					{error}
				</p>
			{/if}

			{#if items.length === 0 && !error}
				<p class="text-[12px] text-text-muted">
					{m.components_attentionbell_nothing_needs_your_attention()}
				</p>
			{/if}

			{#if failures.length > 0}
				<h3 class="mt-1 text-[10px] font-semibold uppercase tracking-wide text-danger">
					{m.components_attentionbell_errors()}
				</h3>
				<ul class="mb-2 flex flex-col gap-1">
					{#each failures as item (item.resource.type + item.resource.id)}
						<li class="text-[12px]">
							{#if item.href}
								<a
									href={resolve(item.href as '/settings/audit')}
									class="block rounded px-1 py-0.5 no-underline hover:bg-bg-toolbar"
								>
									<span class="font-medium">{message(item.kind)}</span>
									<span class="block text-[11px] text-text-muted">{item.resource.name}</span>
								</a>
							{:else}
								<span class="block px-1 py-0.5">
									<span class="font-medium">{message(item.kind)}</span>
									<span class="block text-[11px] text-text-muted">{item.resource.name}</span>
								</span>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}

			{#if warnings.length > 0}
				<h3 class="mt-1 text-[10px] font-semibold uppercase tracking-wide text-amber-600">
					{m.components_attentionbell_warnings()}
				</h3>
				<ul class="flex flex-col gap-1">
					{#each warnings as item (item.resource.type + item.resource.id)}
						<li class="text-[12px]">
							{#if item.href}
								<a
									href={resolve(item.href as '/settings/audit')}
									class="block rounded px-1 py-0.5 no-underline hover:bg-bg-toolbar"
								>
									<span class="font-medium">{message(item.kind)}</span>
									<span class="block text-[11px] text-text-muted">{item.resource.name}</span>
								</a>
							{:else}
								<span class="block px-1 py-0.5">
									<span class="font-medium">{message(item.kind)}</span>
									<span class="block text-[11px] text-text-muted">{item.resource.name}</span>
								</span>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	{/if}
</div>
