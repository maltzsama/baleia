<script lang="ts">
	import KindIcon from './KindIcon.svelte';

	// A select whose button and options show an engine icon beside the name. A
	// native <select> cannot draw an image inside an <option>.
	let {
		value = $bindable(''),
		options,
		label,
		id,
		class: className = ''
	}: {
		value?: string;
		options: { value: string; label: string; kind: string }[];
		label?: string;
		id?: string;
		class?: string;
	} = $props();

	const uid = $props.id();
	const buttonId = $derived(id ?? `icon-select-${uid}`);
	const listId = $derived(`${buttonId}-list`);

	let open = $state(false);
	let active = $state(0);
	let root = $state<HTMLDivElement>();
	let list = $state<HTMLUListElement>();

	const selected = $derived(options.find((o) => o.value === value));

	function show() {
		active = Math.max(
			0,
			options.findIndex((o) => o.value === value)
		);
		open = true;
	}

	function pick(index: number) {
		const option = options[index];
		if (option) value = option.value;
		open = false;
	}

	function onkeydown(event: KeyboardEvent) {
		const last = options.length - 1;
		switch (event.key) {
			case 'ArrowDown':
				event.preventDefault();
				if (!open) show();
				else active = Math.min(active + 1, last);
				break;
			case 'ArrowUp':
				event.preventDefault();
				if (!open) show();
				else active = Math.max(active - 1, 0);
				break;
			case 'Home':
				if (open) {
					event.preventDefault();
					active = 0;
				}
				break;
			case 'End':
				if (open) {
					event.preventDefault();
					active = last;
				}
				break;
			case 'Enter':
			case ' ':
				event.preventDefault();
				if (open) pick(active);
				else show();
				break;
			case 'Escape':
				// Close the list only: a dialog around it must stay open.
				if (open) {
					event.preventDefault();
					event.stopPropagation();
					open = false;
				}
				break;
			case 'Tab':
				open = false;
				break;
		}
	}

	function onwindowclick(event: MouseEvent) {
		if (open && root && !root.contains(event.target as Node)) open = false;
	}

	$effect(() => {
		if (open) list?.children[active]?.scrollIntoView?.({ block: 'nearest' });
	});
</script>

<svelte:window onclick={onwindowclick} />

<div class="relative flex flex-col gap-1 {className}" bind:this={root}>
	{#if label}
		<label for={buttonId} class="text-[11px] font-semibold text-text-muted">{label}</label>
	{/if}
	<button
		id={buttonId}
		type="button"
		role="combobox"
		aria-haspopup="listbox"
		aria-expanded={open}
		aria-controls={listId}
		aria-activedescendant={open ? `${listId}-${active}` : undefined}
		class="flex h-9 w-full cursor-pointer items-center gap-2 rounded-md border border-border bg-bg-panel px-2.5 text-left text-xs text-text-main outline-none transition-colors duration-150 focus:border-primary"
		onclick={() => (open ? (open = false) : show())}
		{onkeydown}
	>
		{#if selected}
			<KindIcon kind={selected.kind} family="engine" />
			<span class="min-w-0 flex-1 truncate">{selected.label}</span>
		{:else}
			<span class="flex-1"></span>
		{/if}
		<svg viewBox="0 0 24 24" class="h-3.5 w-3.5 shrink-0 text-text-muted" aria-hidden="true"
			><path d="M6 9l6 6 6-6" fill="none" stroke="currentColor" stroke-width="2" /></svg
		>
	</button>
	{#if open}
		<ul
			bind:this={list}
			id={listId}
			role="listbox"
			aria-label={label}
			class="absolute left-0 right-0 top-full z-50 mt-1 max-h-60 overflow-y-auto rounded-md border border-border bg-bg-panel py-1 shadow-lg"
		>
			{#each options as option, index (option.value)}
				<!-- svelte-ignore a11y_click_events_have_key_events -->
				<li
					id="{listId}-{index}"
					role="option"
					aria-selected={option.value === value}
					class="flex cursor-pointer items-center gap-2 px-2.5 py-1.5 text-xs text-text-main {index ===
					active
						? 'bg-bg-toolbar'
						: ''} {option.value === value ? 'font-semibold' : ''}"
					onclick={() => pick(index)}
					onmouseenter={() => (active = index)}
				>
					<KindIcon kind={option.kind} family="engine" />
					<span class="min-w-0 flex-1 truncate">{option.label}</span>
				</li>
			{/each}
		</ul>
	{/if}
</div>
