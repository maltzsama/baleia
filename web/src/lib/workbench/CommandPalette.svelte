<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Modal from '$lib/ui/Modal.svelte';
	import { providersList, type PaletteItem } from '$lib/palette/registry';

	let {
		open = $bindable(false),
		mode = 'commands'
	}: {
		open?: boolean;
		mode?: 'commands' | 'quick';
	} = $props();

	let query = $state('');
	let items = $state<PaletteItem[]>([]);
	let active = $state(0);
	let loading = $state(false);
	let input: HTMLInputElement | undefined = $state();

	// A combobox (#472): the input keeps the focus and names the option that
	// Enter runs through aria-activedescendant.
	const uid = $props.id();
	const listId = `${uid}-list`;
	const optionId = (index: number) => `${uid}-option-${index}`;
	const activeId = $derived(items[active] && !loading ? optionId(active) : undefined);

	function moveTo(index: number) {
		active = index;
		document.getElementById(optionId(index))?.scrollIntoView?.({ block: 'nearest' });
	}

	$effect(() => {
		if (!open) return;
		query = '';
		active = 0;
		setTimeout(() => input?.focus(), 0);
	});

	$effect(() => {
		if (!open) return;
		const q = query.trim();
		const selected =
			mode === 'quick' ? providersList().filter((p) => p.id === 'queries') : providersList();
		let cancelled = false;
		const timer = setTimeout(async () => {
			loading = true;
			const lists = await Promise.all(
				selected.map(async (provider) => {
					try {
						return await provider.search(q);
					} catch {
						return [];
					}
				})
			);
			if (cancelled) return;
			items = lists.flat().slice(0, 50);
			active = 0;
			loading = false;
		}, 150);
		return () => {
			cancelled = true;
			clearTimeout(timer);
		};
	});

	async function run(item: PaletteItem | undefined) {
		if (!item) return;
		open = false;
		await item.run();
	}

	function onkeydown(event: KeyboardEvent) {
		if (event.key === 'ArrowDown') {
			event.preventDefault();
			moveTo(Math.min(active + 1, items.length - 1));
		} else if (event.key === 'ArrowUp') {
			event.preventDefault();
			moveTo(Math.max(active - 1, 0));
		} else if (event.key === 'Enter') {
			event.preventDefault();
			void run(items[active]);
		}
	}
</script>

<Modal
	bind:open
	title={mode === 'quick'
		? m.workbench_commandpalette_quick_open()
		: m.workbench_commandpalette_command_palette()}
>
	<div class="flex flex-col gap-2">
		<input
			bind:this={input}
			bind:value={query}
			{onkeydown}
			class="h-9 w-full rounded-md border border-border bg-bg-toolbar px-2.5 text-xs outline-none focus:border-primary"
			placeholder={mode === 'quick'
				? m.workbench_commandpalette_find_a_saved_query()
				: m.workbench_commandpalette_type_a_command_or_search()}
			aria-label={mode === 'quick'
				? m.workbench_commandpalette_quick_open()
				: m.workbench_commandpalette_command_palette()}
			role="combobox"
			aria-expanded={items.length > 0 && !loading}
			aria-controls={listId}
			aria-autocomplete="list"
			aria-activedescendant={activeId}
		/>
		<div
			id={listId}
			class="max-h-[50vh] overflow-y-auto"
			role="listbox"
			aria-label={m.common_results()}
		>
			{#if loading}
				<p class="px-2 py-1 text-[11px] text-slate-600 dark:text-slate-300">
					{m.common_searching()}
				</p>
			{:else if items.length === 0}
				<p class="px-2 py-1 text-[11px] text-slate-600 dark:text-slate-300">No matches.</p>
			{:else}
				{#each items as item, index (item.id)}
					<button
						type="button"
						role="option"
						id={optionId(index)}
						tabindex="-1"
						aria-selected={active === index}
						class="flex w-full items-center justify-between gap-2 rounded px-2 py-1.5 text-left text-[12px] {active ===
						index
							? 'bg-blue-50 text-primary dark:bg-[#152236]'
							: 'hover:bg-slate-100 dark:hover:bg-[#1c2128]'}"
						onmousemove={() => (active = index)}
						onclick={() => run(item)}
					>
						<span class="min-w-0 flex-1 truncate">
							{item.label}
							{#if item.detail}
								<span class="ml-1 text-[10px] text-slate-600 dark:text-slate-300"
									>{item.detail}</span
								>
							{/if}
						</span>
						<span class="shrink-0 text-[9px] uppercase text-slate-600 dark:text-slate-300"
							>{item.group}</span
						>
					</button>
				{/each}
			{/if}
		</div>
	</div>
</Modal>
