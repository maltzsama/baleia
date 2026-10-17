<script lang="ts">
	type Tab = { id: string; label: string };

	let {
		tabs,
		active = $bindable(),
		onchange
	}: {
		tabs: Tab[];
		active: string;
		onchange?: (id: string) => void;
	} = $props();

	function select(id: string) {
		active = id;
		onchange?.(id);
	}

	function onkeydown(event: KeyboardEvent, index: number) {
		const keys = ['ArrowRight', 'ArrowLeft', 'Home', 'End'];
		if (!keys.includes(event.key)) return;
		event.preventDefault();
		let next = index;
		if (event.key === 'ArrowRight') next = (index + 1) % tabs.length;
		if (event.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length;
		if (event.key === 'Home') next = 0;
		if (event.key === 'End') next = tabs.length - 1;
		select(tabs[next].id);
		const el = document.getElementById(`tab-${tabs[next].id}`);
		el?.focus();
	}
</script>

<div class="flex border-b border-border" role="tablist">
	{#each tabs as tab, index (tab.id)}
		<button
			id={`tab-${tab.id}`}
			type="button"
			role="tab"
			aria-selected={active === tab.id}
			tabindex={active === tab.id ? 0 : -1}
			class="-mb-px border-b-2 px-4 py-2.5 text-[13px] font-semibold transition-colors duration-150 {active ===
			tab.id
				? 'border-primary text-primary'
				: 'border-transparent text-text-muted hover:text-text-main'}"
			onclick={() => select(tab.id)}
			onkeydown={(event) => onkeydown(event, index)}
		>
			{tab.label}
		</button>
	{/each}
</div>
