<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import {
		SPLIT_DEFAULT,
		SPLIT_MAX,
		SPLIT_MIN,
		saveSplit,
		splitFromKey,
		splitFromPointer
	} from './split';

	// The separator above the results (#395): drag it, use ↑/↓/Home/End, or
	// double-click to reset. `value` is the results' share of `container`.
	let {
		value = $bindable(SPLIT_DEFAULT),
		container
	}: { value?: number; container: () => HTMLElement | undefined } = $props();

	let dragging = $state(false);

	function set(next: number) {
		value = next;
		saveSplit(next);
	}

	function onpointerdown(event: PointerEvent) {
		if (event.button !== 0) return;
		event.preventDefault();
		(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
		dragging = true;
	}

	function onpointermove(event: PointerEvent) {
		const box = container()?.getBoundingClientRect();
		if (!dragging || !box) return;
		value = splitFromPointer(event.clientY, box);
	}

	function onpointerup(event: PointerEvent) {
		if (!dragging) return;
		(event.currentTarget as HTMLElement).releasePointerCapture(event.pointerId);
		dragging = false;
		saveSplit(value);
	}

	function onkeydown(event: KeyboardEvent) {
		const next = splitFromKey(value, event.key);
		if (next === null) return;
		event.preventDefault();
		set(next);
	}
</script>

<!-- A focusable separator with a value is the WAI-ARIA window splitter, an
     interactive widget; Svelte's lint only knows the static separator. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div
	role="separator"
	aria-orientation="horizontal"
	aria-label={m.workbench_split_resize_results()}
	aria-valuenow={value}
	aria-valuemin={SPLIT_MIN}
	aria-valuemax={SPLIT_MAX}
	tabindex="0"
	title={m.workbench_split_hint()}
	data-testid="results-split"
	class="group relative z-10 -my-[3px] h-[7px] shrink-0 cursor-row-resize touch-none outline-none"
	{onpointerdown}
	{onpointermove}
	{onpointerup}
	onpointercancel={onpointerup}
	ondblclick={() => set(SPLIT_DEFAULT)}
	{onkeydown}
>
	<div
		class="absolute inset-x-0 top-[3px] h-px bg-border transition-colors group-hover:bg-primary group-focus-visible:h-[2px] group-focus-visible:bg-primary {dragging
			? 'bg-primary'
			: ''}"
	></div>
</div>
