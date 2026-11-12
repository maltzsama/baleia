<script lang="ts" module>
	export type MenuItem = {
		id: string;
		label: string;
		onselect: () => void;
		disabled?: boolean;
	};
</script>

<script lang="ts">
	import { tick } from 'svelte';

	let {
		label,
		items,
		title,
		disabled = false
	}: {
		/** What the button says; it also names the menu. */
		label: string;
		items: MenuItem[];
		title?: string;
		disabled?: boolean;
	} = $props();

	const menuId = $props.id();
	let open = $state(false);
	let root: HTMLDivElement | undefined = $state();
	let trigger: HTMLButtonElement | undefined = $state();
	let menu: HTMLDivElement | undefined = $state();

	function itemButtons(): HTMLButtonElement[] {
		return Array.from(
			menu?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') ?? []
		);
	}

	async function openMenu(focus: 'first' | 'last' = 'first') {
		open = true;
		await tick();
		const buttons = itemButtons();
		(focus === 'last' ? buttons[buttons.length - 1] : buttons[0])?.focus();
	}

	function closeMenu(returnFocus: boolean) {
		open = false;
		if (returnFocus) trigger?.focus();
	}

	function onTriggerKeydown(event: KeyboardEvent) {
		if (event.key === 'ArrowDown') {
			event.preventDefault();
			void openMenu('first');
		} else if (event.key === 'ArrowUp') {
			event.preventDefault();
			void openMenu('last');
		}
	}

	function onMenuKeydown(event: KeyboardEvent) {
		const buttons = itemButtons();
		const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
		switch (event.key) {
			case 'ArrowDown':
				event.preventDefault();
				buttons[(at + 1) % buttons.length]?.focus();
				break;
			case 'ArrowUp':
				event.preventDefault();
				buttons[(at - 1 + buttons.length) % buttons.length]?.focus();
				break;
			case 'Home':
				event.preventDefault();
				buttons[0]?.focus();
				break;
			case 'End':
				event.preventDefault();
				buttons[buttons.length - 1]?.focus();
				break;
			case 'Escape':
				event.preventDefault();
				closeMenu(true);
				break;
			case 'Tab':
				closeMenu(false);
				break;
		}
	}

	function choose(item: MenuItem) {
		if (item.disabled) return;
		closeMenu(true);
		item.onselect();
	}

	function onWindowPointerDown(event: PointerEvent) {
		if (open && root && !root.contains(event.target as Node)) closeMenu(false);
	}
</script>

<svelte:window onpointerdown={onWindowPointerDown} />

<div bind:this={root} class="relative">
	<button
		bind:this={trigger}
		type="button"
		{title}
		{disabled}
		aria-haspopup="menu"
		aria-expanded={open}
		aria-controls={open ? menuId : undefined}
		onclick={() => (open ? closeMenu(false) : void openMenu('first'))}
		onkeydown={onTriggerKeydown}
		class="flex items-center gap-[5px] py-1 px-2 text-xs font-medium rounded-[5px] cursor-pointer text-text-muted border border-border bg-transparent hover:border-border-hover hover:bg-border hover:text-text-main whitespace-nowrap disabled:opacity-50 disabled:cursor-not-allowed"
	>
		{label}
		<svg viewBox="0 0 24 24" class="h-3 w-3" aria-hidden="true"><path d="M6 9l6 6 6-6" /></svg>
	</button>
	{#if open}
		<div
			bind:this={menu}
			id={menuId}
			role="menu"
			aria-label={label}
			tabindex="-1"
			onkeydown={onMenuKeydown}
			class="absolute left-0 top-full z-30 mt-1 min-w-[160px] rounded-md border border-border bg-bg-panel py-1 shadow-lg"
		>
			{#each items as item (item.id)}
				<button
					type="button"
					role="menuitem"
					disabled={item.disabled}
					onclick={() => choose(item)}
					class="block w-full px-3 py-1.5 text-left text-xs text-text-main hover:bg-bg-toolbar focus:bg-bg-toolbar focus:outline-none disabled:opacity-50"
				>
					{item.label}
				</button>
			{/each}
		</div>
	{/if}
</div>
